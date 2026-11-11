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
	"time"

	"github.com/diogoap/evcc-planner/internal/audit"
	"github.com/diogoap/evcc-planner/internal/calendar"
	"github.com/diogoap/evcc-planner/internal/clock"
	"github.com/diogoap/evcc-planner/internal/config"
	"github.com/diogoap/evcc-planner/internal/evcc"
	"github.com/diogoap/evcc-planner/internal/localtime"
	"github.com/diogoap/evcc-planner/internal/model"
	"github.com/diogoap/evcc-planner/internal/routes"
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

// Service is the application core.
type Service struct {
	Cfg      *config.Config
	Store    *store.Store
	Clock    clock.Clock
	EVCC     evcc.Client
	Routes   *routes.Resolver
	Calendar *calendar.Syncer
	Log      *slog.Logger
	// Audit records the activity log; every change goes through it.
	Audit *audit.Recorder

	// RoutesNote explains a stub fallback (e.g. missing API key).
	RoutesNote string
	Forecasts  Forecasts

	forecastMu sync.Mutex
	adjustMu   sync.Mutex
	autoPlanMu sync.Mutex

	mu        sync.RWMutex
	evccState *evcc.State
	evccErr   error
	evccAt    time.Time // last attempt
	evccOKAt  time.Time // last success
	socSaved  time.Time
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
// knows it (only while the car is plugged in).
func (s *Service) PollEVCC(ctx context.Context) error {
	st, err := s.EVCC.State(ctx)
	now := s.now()
	s.mu.Lock()
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
	if lp, ok := st.Loadpoint(s.Cfg.EVCC.Loadpoint); ok && lp.Connected && lp.VehicleSoc > 0 {
		return s.recordSoC(lp.VehicleSoc, positive(lp.VehicleRange), now)
	}
	return nil
}

// recordSoC persists the reading (with EVCC's range, if known); unchanged
// values are written at most every five minutes to keep the timestamp
// meaningful without constant writes.
func (s *Service) recordSoC(soc float64, rangeKm *float64, at time.Time) error {
	last, err := s.Store.LastSoC()
	if err != nil {
		return err
	}
	s.mu.Lock()
	recent := at.Sub(s.socSaved) < 5*time.Minute
	s.mu.Unlock()
	if last != nil && last.Percent == soc && sameRange(last.RangeKm, rangeKm) && recent {
		return nil
	}
	if err := s.Store.SaveSoC(model.SoCReading{Percent: soc, RangeKm: rangeKm, ObservedAt: at, Source: "evcc"}); err != nil {
		return err
	}
	s.mu.Lock()
	s.socSaved = at
	s.mu.Unlock()
	return nil
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
	v, _, err := s.Store.Settings(s.Cfg.Defaults.Settings())
	return v, err
}

// SaveSettings validates and stores planning settings.
func (s *Service) SaveSettings(ctx context.Context, v model.Settings) (model.Settings, error) {
	v.HomeAddress = strings.TrimSpace(v.HomeAddress)
	if err := v.Validate(); err != nil {
		return v, ValidationError{Msg: err.Error()}
	}
	ev := model.AuditEvent{Action: "settings.update", SubjectType: "settings", SubjectID: "planning"}
	return v, s.change(ctx, ev, func(tx *store.Store, e *model.AuditEvent) error {
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
