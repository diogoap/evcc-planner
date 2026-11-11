// Package service implements the planner's use cases on top of storage,
// EVCC, routing, calendar sync, and the pure planner.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/diogoap/evcc-planner/internal/audit"
	"github.com/diogoap/evcc-planner/internal/calendar"
	"github.com/diogoap/evcc-planner/internal/clock"
	"github.com/diogoap/evcc-planner/internal/config"
	"github.com/diogoap/evcc-planner/internal/evcc"
	"github.com/diogoap/evcc-planner/internal/homeassistant"
	"github.com/diogoap/evcc-planner/internal/localtime"
	"github.com/diogoap/evcc-planner/internal/model"
	"github.com/diogoap/evcc-planner/internal/store"
)

// WindowDays is the length of the rolling plan.
const WindowDays = 7

// ValidationError is a user-facing input problem (HTTP 400).
type ValidationError = store.ValidationError

// ErrNotFound is returned for unknown records (HTTP 404).
var ErrNotFound = store.ErrNotFound

func invalid(format string, args ...any) error {
	return ValidationError{Msg: fmt.Sprintf(format, args...)}
}

// Service is the application core. LoadSettings must run before use.
type Service struct {
	Cfg      *config.Config
	Store    *store.Store
	Clock    clock.Clock
	Calendar *calendar.Syncer
	Log      *slog.Logger
	// Audit records the activity log; every change goes through it.
	Audit *audit.Recorder
	// Build creates the integrations for connection settings; it runs at
	// start and on every save.
	Build func(model.ConnectionSettings) Integrations

	conn atomic.Pointer[connState]

	// Settings are read from the database once and kept until saved.
	cacheMu  sync.Mutex
	planning *model.Settings
	advice   *model.AdviceSettings

	forecastMu sync.Mutex
	adjustMu   sync.Mutex
	autoPlanMu sync.Mutex

	mu        sync.RWMutex
	evccState *evcc.State
	evccErr   error
	evccAt    time.Time // last attempt
	evccOKAt  time.Time // last success
	haReading *homeassistant.Reading
	haErr     error
	haOKAt    time.Time
}

func (s *Service) loc() *time.Location { return s.Cfg.Location }

func (s *Service) now() time.Time { return s.Clock.Now() }

// window returns local midnight today and seven days later.
func (s *Service) window() (time.Time, time.Time) {
	from := localtime.StartOfDay(s.now(), s.loc())
	return from, localtime.AddDays(from, WindowDays)
}

// ---- EVCC polling ----

// PollEVCC refreshes the cached EVCC state and records the SoC whenever EVCC
// knows it (only while the car is plugged in). When the car is unplugged,
// EVCC's last reading is dated to the last poll that saw it plugged in. A
// result from before a settings change is dropped.
func (s *Service) PollEVCC(ctx context.Context) error {
	cs := s.state()
	st, err := cs.EVCC.State(ctx)
	now := s.now()
	s.mu.Lock()
	if s.state().gen != cs.gen {
		s.mu.Unlock()
		return nil
	}
	var before *model.SoCReading // the reading of a plugged-in car at the previous poll
	if prev := s.evccState; prev != nil {
		if lp, ok := prev.Loadpoint(cs.settings.EVCCLoadpoint); ok && lp.Connected && lp.VehicleSoc > 0 {
			before = &model.SoCReading{
				Percent: lp.VehicleSoc, RangeKm: positive(lp.VehicleRange), ObservedAt: s.evccOKAt, Source: model.SoCSourceEVCC,
			}
		}
	}
	s.evccAt = now
	s.evccErr = err
	if err == nil {
		st.RetrievedAt = now
		s.evccState = st
		s.evccOKAt = now
	}
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if lp, ok := st.Loadpoint(cs.settings.EVCCLoadpoint); ok && lp.Connected && lp.VehicleSoc > 0 {
		return s.recordSoC(model.SoCReading{
			Percent: lp.VehicleSoc, RangeKm: positive(lp.VehicleRange), ObservedAt: now, Source: model.SoCSourceEVCC,
		})
	}
	if before == nil {
		return nil
	}
	// Just unplugged: unchanged readings are saved only every five minutes,
	// so date the last one to when the car was last seen plugged in.
	last, err := s.Store.SoCReading(model.SoCSourceEVCC)
	if err != nil || (last != nil && !last.ObservedAt.Before(before.ObservedAt)) {
		return err
	}
	return s.Store.SaveSoC(*before)
}

// PollHomeAssistant reads the car's SoC from Home Assistant, which has it
// even while the car is unplugged, and records it with the time the car
// reported it.
func (s *Service) PollHomeAssistant(ctx context.Context) error {
	cs := s.state()
	if cs.HA == nil {
		return nil
	}
	r, err := cs.HA.SoC(ctx)
	now := s.now()
	s.mu.Lock()
	if s.state().gen != cs.gen {
		s.mu.Unlock()
		return nil
	}
	s.haErr = err
	if err == nil {
		s.haReading, s.haOKAt = r, now
	}
	s.mu.Unlock()
	if err != nil {
		return err
	}
	at := r.ObservedAt
	if at.After(now) {
		at = now
	}
	return s.recordSoC(model.SoCReading{
		Percent: r.Percent, RangeKm: r.RangeKm, ObservedAt: at, ChangedAt: r.ChangedAt, Source: model.SoCSourceHomeAssistant,
	})
}

// recordSoC persists a source's reading (with its range, if known);
// unchanged values are written at most every five minutes to keep the
// timestamp meaningful without constant writes.
func (s *Service) recordSoC(r model.SoCReading) error {
	last, err := s.Store.SoCReading(r.Source)
	if err != nil {
		return err
	}
	if last != nil && last.Percent == r.Percent && sameRange(last.RangeKm, r.RangeKm) &&
		last.ChangedAt.Unix() == r.ChangedAt.Unix() && r.ObservedAt.Sub(last.ObservedAt) < 5*time.Minute {
		return nil
	}
	return s.Store.SaveSoC(r)
}

// positive returns v, or nil for 0 (EVCC's "unknown").
func positive(v float64) *float64 {
	if v <= 0 {
		return nil
	}
	return &v
}

func sameRange(a, b *float64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

type evccSnapshot struct {
	state *evcc.State
	err   error
	at    time.Time
	okAt  time.Time
}

func (s *Service) snapshot() evccSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return evccSnapshot{state: s.evccState, err: s.evccErr, at: s.evccAt, okAt: s.evccOKAt}
}

// ---- settings ----

// Settings returns effective planning settings.
func (s *Service) Settings() (model.Settings, error) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	if s.planning == nil {
		v, _, err := s.Store.Settings(s.Cfg.Defaults.Settings())
		if err != nil {
			return v, err
		}
		s.planning = &v
	}
	return *s.planning, nil
}

// SaveSettings validates and stores planning settings.
func (s *Service) SaveSettings(ctx context.Context, v model.Settings) (model.Settings, error) {
	v.HomeAddress = strings.TrimSpace(v.HomeAddress)
	if err := v.Validate(); err != nil {
		return v, ValidationError{Msg: err.Error()}
	}
	ev := model.AuditEvent{Action: "settings.update", SubjectType: "settings", SubjectID: "planning"}
	err := s.change(ctx, ev, func(tx *store.Store, e *model.AuditEvent) error {
		before, _, err := tx.Settings(s.Cfg.Defaults.Settings())
		if err != nil {
			return err
		}
		if err := tx.SaveSettings(v); err != nil {
			return err
		}
		e.Before, e.After = changes(before, v)
		e.Summary = settingsSummary("settings", e.After)
		return nil
	})
	if err == nil {
		s.cacheMu.Lock()
		s.planning = &v
		s.cacheMu.Unlock()
	}
	return v, err
}

func settingsSummary(what string, diff []byte) string {
	if fields := fieldList(diff, settingLabels); fields != "" {
		return "Changed " + what + ": " + fields
	}
	return "Saved " + what + " (no changes)"
}

// ---- places ----

// Places lists saved places.
func (s *Service) Places() ([]model.Place, error) { return s.Store.Places() }

func cleanPlace(p model.Place) (model.Place, error) {
	p.Name = strings.TrimSpace(p.Name)
	p.Address = strings.TrimSpace(p.Address)
	if p.Name == "" || p.Address == "" {
		return p, invalid("a place needs a name and an address")
	}
	return p, nil
}

func placeOf(p model.Place) placeRecord { return placeRecord{Name: p.Name, Address: p.Address} }

// CreatePlace adds a saved place.
func (s *Service) CreatePlace(ctx context.Context, p model.Place) (model.Place, error) {
	p, err := cleanPlace(p)
	if err != nil {
		return p, err
	}
	err = s.change(ctx, model.AuditEvent{Action: "place.create", SubjectType: "place"},
		func(tx *store.Store, e *model.AuditEvent) error {
			if p, err = tx.CreatePlace(p); err != nil {
				return err
			}
			e.SubjectID = strconv.FormatInt(p.ID, 10)
			e.Summary = "Added place " + p.Name
			e.After = audit.Marshal(placeOf(p))
			return nil
		})
	return p, err
}

// UpdatePlace edits a saved place.
func (s *Service) UpdatePlace(ctx context.Context, p model.Place) (model.Place, error) {
	p, err := cleanPlace(p)
	if err != nil {
		return p, err
	}
	ev := model.AuditEvent{Action: "place.update", SubjectType: "place", SubjectID: strconv.FormatInt(p.ID, 10)}
	return p, s.change(ctx, ev, func(tx *store.Store, e *model.AuditEvent) error {
		before, err := tx.Place(p.ID)
		if err != nil {
			return err
		}
		if err := tx.UpdatePlace(p); err != nil {
			return err
		}
		e.Before, e.After = changes(placeOf(before), placeOf(p))
		e.Summary = "Edited place " + p.Name
		return nil
	})
}

// DeletePlace removes a saved place.
func (s *Service) DeletePlace(ctx context.Context, id int64) error {
	ev := model.AuditEvent{Action: "place.delete", SubjectType: "place", SubjectID: strconv.FormatInt(id, 10)}
	return s.change(ctx, ev, func(tx *store.Store, e *model.AuditEvent) error {
		before, err := tx.Place(id)
		if err != nil {
			return err
		}
		if err := tx.DeletePlace(id); err != nil {
			return err
		}
		e.Before = audit.Marshal(placeOf(before))
		e.Summary = "Deleted place " + before.Name
		return nil
	})
}

// ---- bootstrap ----

// Bootstrap seeds saved places from private config once.
func (s *Service) Bootstrap(ctx context.Context) error {
	if len(s.Cfg.Places) == 0 {
		return nil
	}
	done, err := s.Store.Meta("places_seeded")
	if err != nil || done != "" {
		return err
	}
	existing, err := s.Store.Places()
	if err != nil {
		return err
	}
	if len(existing) == 0 {
		for _, p := range s.Cfg.Places {
			if _, err := s.CreatePlace(ctx, model.Place{Name: p.Name, Address: p.Address}); err != nil {
				return fmt.Errorf("seed place %q: %w", p.Name, err)
			}
		}
	}
	return s.Store.SetMeta("places_seeded", "1")
}

// IsNotFound reports whether err means a missing record.
func IsNotFound(err error) bool { return errors.Is(err, ErrNotFound) }

func round(v float64, places int) float64 {
	f := math.Pow(10, float64(places))
	return math.Round(v*f) / f
}
