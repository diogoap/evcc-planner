package store

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/diogoap/evcc-planner/internal/model"
)

var ams, _ = time.LoadLocation("Europe/Amsterdam")

func event(at time.Time, source, subjectType, subjectID string) model.AuditEvent {
	return model.AuditEvent{
		At: at, Type: model.EventAction, Source: source, Action: subjectType + ".update", SubjectType: subjectType,
		SubjectID: subjectID, Summary: "changed", Outcome: model.OutcomeSuccess,
	}
}

func TestAuditEventsFilterAndPage(t *testing.T) {
	s := open(t)
	day := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	e := event(day, model.SourceUI, "trip", "recurring:1:2026-11-10")
	e.Related = "schedule:1"
	e.Before, e.After = json.RawMessage(`{"enabled":true}`), json.RawMessage(`{"enabled":false}`)
	saved, err := s.AddAuditEvent(e)
	if err != nil || saved.ID == 0 {
		t.Fatal(err)
	}
	s.AddAuditEvent(event(day.Add(time.Hour), model.SourceUI, "schedule", "1"))
	s.AddAuditEvent(event(day.Add(24*time.Hour), model.SourceJob, "adjustment", "4"))
	call := event(day.Add(25*time.Hour), model.SourceCalendarSync, "api", "")
	call.Type, call.Service, call.Outcome, call.Bytes, call.DurationMs = model.EventAPICall, "calendar", model.OutcomeFailure, 10, 300
	s.AddAuditEvent(call)

	all, err := s.AuditEvents(AuditFilter{})
	if err != nil || len(all) != 4 || all[0].Service != "calendar" || all[3].ID != saved.ID {
		t.Fatalf("all = %+v, %v (newest first)", all, err)
	}
	if string(all[3].After) != `{"enabled":false}` || all[3].Related != "schedule:1" || all[1].Before != nil {
		t.Errorf("round trip = %+v / %+v", all[3], all[1])
	}
	count := func(f AuditFilter) int {
		t.Helper()
		list, err := s.AuditEvents(f)
		if err != nil {
			t.Fatal(err)
		}
		return len(list)
	}
	if n := count(AuditFilter{Subject: "schedule:1"}); n != 2 {
		t.Errorf("schedule history = %d, want its own event and its date's", n)
	}
	if n := count(AuditFilter{Subject: "trip:recurring:1:2026-11-10"}); n != 1 {
		t.Errorf("trip history = %d", n)
	}
	if n := count(AuditFilter{Subject: "schedule"}); n != 2 {
		t.Errorf("all schedule history = %d", n)
	}
	if n := count(AuditFilter{SubjectTypes: []string{"plan", "adjustment"}}); n != 1 {
		t.Errorf("by type = %d", n)
	}
	if n := count(AuditFilter{Source: model.SourceUI}); n != 2 {
		t.Errorf("by source = %d", n)
	}
	if n := count(AuditFilter{From: day.Add(23 * time.Hour), To: day.Add(48 * time.Hour)}); n != 2 {
		t.Errorf("by date = %d", n)
	}
	page, _ := s.AuditEvents(AuditFilter{Limit: 2})
	older, _ := s.AuditEvents(AuditFilter{BeforeID: page[1].ID})
	if len(page) != 2 || len(older) != 2 || older[0].ID >= page[1].ID {
		t.Errorf("paging = %+v then %+v", page, older)
	}

	usage, err := s.APIUsage(day, day.Add(48*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if u := usage["calendar"]; u.Calls != 1 || u.Failures != 1 || u.Bytes != 10 || u.DurationMs != 300 || len(usage) != 1 {
		t.Errorf("usage = %+v", usage)
	}
}

func TestAuditEventsAreAppendOnly(t *testing.T) {
	s := open(t)
	s.AddAuditEvent(event(time.Now(), model.SourceUI, "place", "1"))
	if _, err := s.q.Exec(`UPDATE audit_events SET summary = 'rewritten'`); err == nil {
		t.Fatal("updating an audit event must fail")
	}
}

func TestInTxRollsBackChangeAndEventTogether(t *testing.T) {
	s := open(t)
	boom := errors.New("boom")
	err := s.InTx(func(tx *Store) error {
		if _, err := tx.CreatePlace(model.Place{Name: "School", Address: "A 1"}); err != nil {
			return err
		}
		if _, err := tx.AddAuditEvent(event(time.Now(), model.SourceUI, "place", "1")); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	places, _ := s.Places()
	events, _ := s.AuditEvents(AuditFilter{})
	if len(places) != 0 || len(events) != 0 {
		t.Fatalf("rolled back: places %v, events %v", places, events)
	}
	// Committed together; store methods that use their own transaction join it.
	err = s.InTx(func(tx *Store) error {
		tpl, err := tx.CreateTemplate(model.Template{Title: "Run", Destination: "School", Weekdays: []int{1}, Start: "08:00", End: "08:10", Enabled: true})
		if err != nil {
			return err
		}
		if err := tx.DeleteTemplate(tpl.ID); err != nil {
			return err
		}
		_, err = tx.AddAuditEvent(event(time.Now(), model.SourceUI, "schedule", "1"))
		return err
	})
	if events, _ := s.AuditEvents(AuditFilter{}); err != nil || len(events) != 1 {
		t.Fatalf("commit: %v, %d events", err, len(events))
	}
}

func TestCleanupDeletesOldHistoryOnly(t *testing.T) {
	s := open(t)
	cutoff := time.Date(2026, 8, 11, 0, 0, 0, 0, ams)
	old, recent := cutoff.Add(-time.Hour), cutoff.Add(time.Hour)

	s.AddAuditEvent(event(old, model.SourceUI, "trip", "manual:1"))
	s.AddAuditEvent(event(recent, model.SourceUI, "trip", "manual:2"))
	s.CreateManualTrip(model.ManualTrip{Title: "Old", Destination: "A", Start: old.Add(-2 * time.Hour), End: old, Enabled: true})
	s.CreateManualTrip(model.ManualTrip{Title: "New", Destination: "B", Start: old, End: recent, Enabled: true})
	off := false
	s.SaveOverride(model.Override{TripKey: "recurring:1:2026-08-10", Enabled: &off})
	s.SaveOverride(model.Override{TripKey: "recurring:1:2026-08-11", Enabled: &off})
	s.ReplaceCalendarEvents("c1", "Family", []model.CalendarEvent{{Key: "live", UID: "u", Title: "Dentist", Start: recent, End: recent}})
	s.SaveOverride(model.Override{TripKey: "calendar:live", Enabled: &off})
	s.SaveOverride(model.Override{TripKey: "calendar:gone", Enabled: &off}) // edited just now: kept for now
	s.PutRoute("home", "school", "any", CachedRoute{DistanceM: 6000, DurationS: 600, Provider: "google", FetchedAt: old})
	s.PutRoute("home", "office", "any", CachedRoute{DistanceM: 38000, DurationS: 2100, Provider: "google", FetchedAt: recent})
	s.AddPlanWrite(model.PlanWrite{Vehicle: "leaf", SoC: 40, TargetTime: old, WrittenAt: old})
	s.AddPlanWrite(model.PlanWrite{Vehicle: "leaf", SoC: 50, TargetTime: old, WrittenAt: old.Add(time.Minute)})
	s.Dismiss("2026-08-10", "defer", old)
	s.Dismiss("2026-08-11", "defer", old)
	s.AddAdjustment(model.Adjustment{Kind: model.AdjustPostpone, AdviceDate: "2026-08-10", Status: model.AdjustRestored, ExpiresAt: old})
	s.AddAdjustment(model.Adjustment{Kind: model.AdjustPostpone, AdviceDate: "2026-08-09", Status: model.AdjustActive, ExpiresAt: recent})
	s.CreateTemplate(model.Template{Title: "Run", Destination: "School", Weekdays: []int{1}, Start: "08:00", End: "08:10", Enabled: true})

	counts, err := s.Cleanup(cutoff, ams)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int64{"activity": 1, "manual_trips": 1, "trip_edits": 1, "routes": 1, "plan_writes": 1, "dismissals": 1, "adjustments": 1}
	for k, v := range want {
		if counts[k] != v {
			t.Errorf("%s: deleted %d, want %d (all: %v)", k, counts[k], v, counts)
		}
	}
	if events, _ := s.AuditEvents(AuditFilter{}); len(events) != 1 || events[0].SubjectID != "manual:2" {
		t.Errorf("events left = %+v", events)
	}
	if trips, _ := s.ManualTrips(old.Add(-48*time.Hour), recent.Add(time.Hour)); len(trips) != 1 || trips[0].Title != "New" {
		t.Errorf("trips left = %+v", trips)
	}
	ov, _ := s.Overrides()
	if len(ov) != 3 || ov["recurring:1:2026-08-10"].TripKey != "" {
		t.Errorf("overrides left = %v", ov)
	}
	if w, _ := s.LastPlanWrite(); w == nil || w.SoC != 50 {
		t.Errorf("the latest plan write must be kept: %+v", w)
	}
	if d, _ := s.Dismissals(); len(d) != 1 {
		t.Errorf("dismissals left = %v", d)
	}
	if a, _ := s.Adjustments("2000-01-01"); len(a) != 1 || a[0].Status != model.AdjustActive {
		t.Errorf("adjustments left = %+v (active ones are kept)", a)
	}
	if tpl, _ := s.Templates(); len(tpl) != 1 {
		t.Error("schedules are kept")
	}
	if again, _ := s.Cleanup(cutoff, ams); len(again) != 0 {
		t.Errorf("second run deleted %v", again)
	}
}
