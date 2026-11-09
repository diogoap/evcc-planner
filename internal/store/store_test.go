package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/diogoap/evcc-planner/internal/model"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSettingsMergeOntoDefaults(t *testing.T) {
	s := open(t)
	defaults := model.Settings{UsableCapacityKWh: 59, ConsumptionKWhPer100Km: 18, DefaultStayMin: 15}
	got, saved, err := s.Settings(defaults)
	if err != nil || saved || got != defaults {
		t.Fatalf("unsaved settings = %+v %v %v", got, saved, err)
	}
	if err := s.SaveSettings(model.Settings{HomeAddress: "Home 1", UsableCapacityKWh: 40, ConsumptionKWhPer100Km: 20}); err != nil {
		t.Fatal(err)
	}
	got, saved, _ = s.Settings(defaults)
	if !saved || got.HomeAddress != "Home 1" || got.UsableCapacityKWh != 40 {
		t.Fatalf("saved settings = %+v", got)
	}

	// Settings saved before a field existed keep that field's default.
	if _, err := s.db.Exec(`UPDATE settings SET value = '{"homeAddress":"Old"}' WHERE key = 'planning'`); err != nil {
		t.Fatal(err)
	}
	got, _, _ = s.Settings(defaults)
	if got.HomeAddress != "Old" || got.DefaultStayMin != 15 || got.UsableCapacityKWh != 59 {
		t.Fatalf("partial settings = %+v", got)
	}
}

func TestReopenKeepsDataAndSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreatePlace(model.Place{Name: "School", Address: "A 1"}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	places, _ := s.Places()
	if len(places) != 1 {
		t.Fatalf("places after reopen = %+v", places)
	}
	if _, err := s.CreatePlace(model.Place{Name: "school", Address: "B"}); !errors.As(err, &ValidationError{}) {
		t.Fatalf("duplicate name (case-insensitive) should be a validation error, got %v", err)
	}
}

func TestOverridesRoundTripAndDeleteWhenEmpty(t *testing.T) {
	s := open(t)
	off := false
	start := time.Date(2026, 11, 12, 7, 45, 0, 0, time.UTC)
	km := 30.0
	o := model.Override{TripKey: "recurring:1:2026-11-12", Enabled: &off, Start: &start, DistanceOverrideKm: &km}
	if err := s.SaveOverride(o); err != nil {
		t.Fatal(err)
	}
	all, _ := s.Overrides()
	got := all[o.TripKey]
	if got.Enabled == nil || *got.Enabled || !got.Start.Equal(start) || *got.DistanceOverrideKm != 30 || got.Title != nil {
		t.Fatalf("round trip = %+v", got)
	}
	if err := s.SaveOverride(model.Override{TripKey: o.TripKey}); err != nil {
		t.Fatal(err)
	}
	if all, _ := s.Overrides(); len(all) != 0 {
		t.Fatalf("empty override should be deleted, got %+v", all)
	}
}

func TestDeleteTemplateRemovesItsOverrides(t *testing.T) {
	s := open(t)
	tpl, err := s.CreateTemplate(model.Template{Title: "Run", Destination: "School", Weekdays: []int{1}, Start: "08:00", End: "08:10", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	off := false
	s.SaveOverride(model.Override{TripKey: "recurring:1:2026-11-09", Enabled: &off})
	s.SaveOverride(model.Override{TripKey: "recurring:11:2026-11-09", Enabled: &off})
	if err := s.DeleteTemplate(tpl.ID); err != nil {
		t.Fatal(err)
	}
	all, _ := s.Overrides()
	if _, ok := all["recurring:11:2026-11-09"]; !ok || len(all) != 1 {
		t.Fatalf("only template 1's overrides should go: %+v", all)
	}
	if err := s.DeleteTemplate(tpl.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete = %v, want ErrNotFound", err)
	}
}

func TestManualTripsWindow(t *testing.T) {
	s := open(t)
	d := func(day, h int) time.Time { return time.Date(2026, 11, day, h, 0, 0, 0, time.UTC) }
	for _, m := range []model.ManualTrip{
		{Title: "before", Destination: "x", Start: d(8, 9), End: d(8, 10), Enabled: true},
		{Title: "inside", Destination: "x", Start: d(10, 9), End: d(10, 10), Enabled: true},
		{Title: "overnight", Destination: "x", Start: d(8, 22), End: d(9, 2), Enabled: true},
	} {
		if _, err := s.CreateManualTrip(m); err != nil {
			t.Fatal(err)
		}
	}
	list, err := s.ManualTrips(d(9, 0), d(16, 0))
	if err != nil || len(list) != 2 || list[0].Title != "overnight" || list[1].Title != "inside" {
		t.Fatalf("window = %+v %v", list, err)
	}
}

func TestRouteCacheIsScopedByProvider(t *testing.T) {
	s := open(t)
	now := time.Now().UTC().Truncate(time.Second)
	if err := s.PutRoute("home", "school", "any", CachedRoute{DistanceM: 6000, DurationS: 720, Provider: "stub", FetchedAt: now}); err != nil {
		t.Fatal(err)
	}
	if r, _ := s.Route("home", "school", "any", "google"); r != nil {
		t.Fatalf("stub route must not be served for google: %+v", r)
	}
	r, err := s.AnyRoute("home", "school", "stub")
	if err != nil || r == nil || r.DistanceM != 6000 || !r.FetchedAt.Equal(now) {
		t.Fatalf("any route = %+v %v", r, err)
	}
}

func TestCalendarReplaceAndSyncStatus(t *testing.T) {
	s := open(t)
	ev := model.CalendarEvent{Key: "k1", UID: "u", Title: "T", Location: "L",
		Start: time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC), End: time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)}
	s.ReplaceCalendarEvents("a1", "Family", []model.CalendarEvent{ev})
	s.ReplaceCalendarEvents("b2", "Work", []model.CalendarEvent{{Key: "k2", UID: "u2", Start: ev.Start, End: ev.End}})
	s.ReplaceCalendarEvents("a1", "Family", nil)
	list, _ := s.CalendarEvents(ev.Start.Add(-time.Hour), ev.End)
	if len(list) != 1 || list[0].Key != "k2" || list[0].CalendarID != "b2" || list[0].Calendar != "Work" {
		t.Fatalf("after replace = %+v", list)
	}
	now := time.Now().UTC().Truncate(time.Second)
	s.SaveSyncStatus(model.CalendarSyncStatus{CalendarID: "b2", Name: "Work", LastAttemptAt: &now, LastSuccessAt: &now, EventCount: 1})
	if st, _ := s.SyncStatuses(); st["b2"].Name != "Work" {
		t.Fatalf("sync status = %+v", st)
	}
	if err := s.RemoveCalendarsExcept([]string{"a1"}); err != nil {
		t.Fatal(err)
	}
	list, _ = s.CalendarEvents(ev.Start.Add(-time.Hour), ev.End)
	st, _ := s.SyncStatuses()
	if len(list) != 0 || len(st) != 0 {
		t.Fatalf("removed calendar left data: %+v %+v", list, st)
	}
}

func TestSoCAndPlanWrites(t *testing.T) {
	s := open(t)
	if r, err := s.LastSoC(); r != nil || err != nil {
		t.Fatalf("empty soc = %+v %v", r, err)
	}
	at := time.Date(2026, 11, 9, 18, 42, 0, 0, time.UTC)
	s.SaveSoC(model.SoCReading{Percent: 38, ObservedAt: at, Source: "evcc"})
	s.SaveSoC(model.SoCReading{Percent: 41, ObservedAt: at.Add(time.Hour), Source: "evcc"})
	r, _ := s.LastSoC()
	if r.Percent != 41 || !r.ObservedAt.Equal(at.Add(time.Hour)) {
		t.Fatalf("last soc = %+v", r)
	}
	w, err := s.AddPlanWrite(model.PlanWrite{Vehicle: "db:3", SoC: 46, TargetTime: at, WrittenAt: at})
	if err != nil || w.ID == 0 {
		t.Fatal(err)
	}
	last, _ := s.LastPlanWrite()
	if last.SoC != 46 || last.Vehicle != "db:3" {
		t.Fatalf("last write = %+v", last)
	}
	empty, _ := s.IsEmpty()
	if !empty {
		t.Error("SoC and plan writes are not personal data for seeding purposes")
	}
	s.SetMeta("x", "1")
	if empty, _ := s.IsEmpty(); !empty {
		t.Error("meta flags must not count as personal data")
	}
}
