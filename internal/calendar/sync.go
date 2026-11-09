package calendar

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/diogoap/evcc-planner/internal/clock"
	"github.com/diogoap/evcc-planner/internal/config"
	"github.com/diogoap/evcc-planner/internal/localtime"
	"github.com/diogoap/evcc-planner/internal/model"
)

// Source fetches the raw iCal data of one calendar.
type Source interface {
	Fetch(ctx context.Context, cal config.CalendarSource) ([]byte, error)
}

// HTTPSource downloads secret iCal URLs.
type HTTPSource struct{ HTTP *http.Client }

// Fetch downloads the feed. webcal:// URLs are fetched over HTTPS.
func (s HTTPSource) Fetch(ctx context.Context, cal config.CalendarSource) ([]byte, error) {
	if cal.URL == "" {
		return nil, errors.New("iCal URL not set")
	}
	u := cal.URL
	if strings.HasPrefix(u, "webcal://") {
		u = "https://" + strings.TrimPrefix(u, "webcal://")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, errors.New("invalid iCal URL")
	}
	resp, err := s.HTTP.Do(req)
	if err != nil {
		// The URL is a secret; never include it in errors or logs.
		return nil, errors.New("calendar feed unreachable")
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("calendar feed returned %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 20<<20))
}

// Store is the persistence the syncer needs.
type Store interface {
	ReplaceCalendarEvents(calendarID, name string, events []model.CalendarEvent) error
	SaveSyncStatus(st model.CalendarSyncStatus) error
	RemoveCalendarsExcept(ids []string) error
	SyncStatuses() (map[string]model.CalendarSyncStatus, error)
}

// Syncer refreshes stored calendar events.
type Syncer struct {
	Store     Store
	Source    Source
	Calendars []config.CalendarSource
	Clock     clock.Clock
	Location  *time.Location
	Log       *slog.Logger
	Days      int // sync horizon; the plan shows seven days, one spare covers late trips

	mu sync.Mutex
}

// SyncAll syncs every configured calendar. A failing calendar keeps its last
// successfully synced events.
func (s *Syncer) SyncAll(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.Calendars))
	for _, c := range s.Calendars {
		ids = append(ids, c.ID)
	}
	if err := s.Store.RemoveCalendarsExcept(ids); err != nil {
		return err
	}
	var errs []error
	for _, c := range s.Calendars {
		if err := s.syncOne(ctx, c); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", c.Label, err))
		}
	}
	return errors.Join(errs...)
}

func (s *Syncer) syncOne(ctx context.Context, c config.CalendarSource) error {
	now := s.Clock.Now()
	status := model.CalendarSyncStatus{CalendarID: c.ID, Name: c.Label, LastAttemptAt: &now}
	days := s.Days
	if days == 0 {
		days = 8
	}
	from := localtime.StartOfDay(now, s.Location)
	to := localtime.AddDays(from, days)

	data, err := s.Source.Fetch(ctx, c)
	var events []model.CalendarEvent
	var name string
	if err == nil {
		events, name, err = Parse(data, c.ID, s.Location, from, to)
	}
	if name != "" {
		status.Name = name
	}
	for i := range events {
		events[i].Calendar = status.Name
	}
	if err == nil {
		err = s.Store.ReplaceCalendarEvents(c.ID, status.Name, events)
	}
	if err != nil {
		status.LastError = err.Error()
		s.Log.Warn("calendar sync failed", "calendar", status.Name, "err", err)
		return errors.Join(err, s.saveFailed(status))
	}
	status.LastSuccessAt = &now
	status.EventCount = len(events)
	s.Log.Info("calendar synced", "calendar", status.Name, "events", len(events))
	return s.Store.SaveSyncStatus(status)
}

// saveFailed records a failed attempt, keeping the last success time and
// event count so the UI can show how old the retained events are.
func (s *Syncer) saveFailed(st model.CalendarSyncStatus) error {
	if prev, err := s.Store.SyncStatuses(); err == nil {
		if p, ok := prev[st.CalendarID]; ok {
			st.LastSuccessAt = p.LastSuccessAt
			st.EventCount = p.EventCount
			if p.Name != "" {
				st.Name = p.Name
			}
		}
	}
	return s.Store.SaveSyncStatus(st)
}
