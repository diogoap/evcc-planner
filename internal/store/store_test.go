package store

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
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
	if list, err := s.SoCReadings(); len(list) != 0 || err != nil {
		t.Fatalf("empty soc = %+v %v", list, err)
	}
	at := time.Date(2026, 11, 9, 18, 42, 0, 0, time.UTC)
	rangeKm := 107.0
	s.SaveSoC(model.SoCReading{Percent: 38, RangeKm: &rangeKm, ObservedAt: at, Source: model.SoCSourceEVCC})
	if r, _ := s.SoCReading(model.SoCSourceEVCC); r.RangeKm == nil || *r.RangeKm != 107 {
		t.Fatalf("range = %+v", r)
	}
	s.SaveSoC(model.SoCReading{Percent: 41, ObservedAt: at.Add(time.Hour), Source: model.SoCSourceEVCC})
	r, _ := s.SoCReading(model.SoCSourceEVCC)
	if r.Percent != 41 || !r.ObservedAt.Equal(at.Add(time.Hour)) || r.RangeKm != nil {
		t.Fatalf("last soc = %+v (a reading without range clears it)", r)
	}

	// Each source keeps its own reading; the newest comes first.
	s.SaveSoC(model.SoCReading{Percent: 35, ObservedAt: at.Add(2 * time.Hour), ChangedAt: at.Add(time.Hour), Source: model.SoCSourceHomeAssistant})
	list, err := s.SoCReadings()
	if err != nil || len(list) != 2 || list[0].Source != model.SoCSourceHomeAssistant || list[1].Percent != 41 ||
		!list[0].Changed().Equal(at.Add(time.Hour)) || !list[1].Changed().Equal(list[1].ObservedAt) {
		t.Fatalf("readings = %+v %v", list, err)
	}
	if r, _ := s.SoCReading(model.SoCSourceEVCC); r == nil || r.Percent != 41 {
		t.Fatalf("evcc reading = %+v", r)
	}
	if r, err := s.SoCReading("other"); r != nil || err != nil {
		t.Fatalf("unknown source = %+v %v", r, err)
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

func TestForecastKeepsLastPayloadOnFailure(t *testing.T) {
	s := open(t)
	if r, err := s.Forecast("tibber"); err != nil || r != nil {
		t.Fatalf("unknown source = %+v %v", r, err)
	}
	t1 := time.Date(2026, 11, 9, 18, 0, 0, 0, time.UTC)
	if err := s.SaveForecast("tibber", t1, []byte(`{"v":1}`), "h1", "pattern"); err != nil {
		t.Fatal(err)
	}
	t2 := t1.Add(24 * time.Hour)
	if err := s.SaveForecastFailure("tibber", t2, "page unreachable"); err != nil {
		t.Fatal(err)
	}
	r, _ := s.Forecast("tibber")
	if string(r.Payload) != `{"v":1}` || r.ContentHash != "h1" || r.LastError != "page unreachable" ||
		!r.LastSuccessAt.Equal(t1) || !r.LastAttemptAt.Equal(t2) {
		t.Fatalf("after failure = %+v", r)
	}
	t3 := t2.Add(time.Hour)
	s.TouchForecast("tibber", t3)
	r, _ = s.Forecast("tibber")
	if r.LastError != "" || !r.LastSuccessAt.Equal(t3) || string(r.Payload) != `{"v":1}` {
		t.Fatalf("after unchanged check = %+v", r)
	}
	// A first attempt that fails has no payload.
	s.SaveForecastFailure("knmi", t1, "no key")
	if r, _ := s.Forecast("knmi"); r.Payload != nil || r.LastSuccessAt != nil {
		t.Fatalf("failed first attempt = %+v", r)
	}
}

func TestAdviceSettingsDismissalsAndAdjustments(t *testing.T) {
	s := open(t)
	got, err := s.AdviceSettings(model.DefaultAdviceSettings())
	if err != nil || !got.Enabled || got.AutoApply || got.AutoMinConfidence != 3 {
		t.Fatalf("defaults = %+v %v", got, err)
	}
	target := 70
	got.TargetHigh = &target
	got.Enabled = false
	if err := s.SaveAdviceSettings(got); err != nil {
		t.Fatal(err)
	}
	got, _ = s.AdviceSettings(model.DefaultAdviceSettings())
	if got.Enabled || got.TargetHigh == nil || *got.TargetHigh != 70 {
		t.Fatalf("saved = %+v", got)
	}

	s.Dismiss("2026-11-10", "charge_ahead", time.Now())
	s.Dismiss("2026-11-12", "defer", time.Now())
	s.Undismiss("2026-11-12")
	if d, _ := s.Dismissals(); len(d) != 1 || d["2026-11-10"] != "charge_ahead" {
		t.Fatalf("dismissals = %v", d)
	}

	expires := time.Date(2026, 11, 10, 16, 0, 0, 0, time.UTC)
	a, err := s.AddAdjustment(model.Adjustment{
		Kind: model.AdjustExtraPlan, AdviceDate: "2026-11-10", Status: model.AdjustActive, ExpiresAt: expires,
		Plan: &model.PlanValue{SoC: 70, Time: expires}, PrevPlan: &model.PlanValue{SoC: 42, Time: expires.Add(15 * time.Hour)},
	})
	if err != nil || a.ID == 0 {
		t.Fatal(a, err)
	}
	old, _ := s.AddAdjustment(model.Adjustment{Kind: model.AdjustPostpone, AdviceDate: "2026-11-01", Status: model.AdjustRestored, ExpiresAt: expires})
	list, _ := s.Adjustments("2026-11-09")
	if len(list) != 1 || list[0].ID != a.ID || list[0].PrevPlan.SoC != 42 {
		t.Fatalf("recent adjustments = %+v", list)
	}
	old.Status = model.AdjustActive
	s.UpdateAdjustment(old)
	if list, _ := s.Adjustments("2026-11-09"); len(list) != 2 {
		t.Fatalf("active adjustments are always listed: %+v", list)
	}
	if _, err := s.Adjustment(99); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing adjustment: %v", err)
	}
}

func TestSoCMigrationKeepsTheLastReading(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	// The schema before per-source readings, with a demo reading in it.
	last := slices.IndexFunc(migrations, func(m string) bool { return strings.Contains(m, "CREATE TABLE soc_readings") })
	for _, m := range migrations[:last] {
		if _, err := db.Exec(m); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, last)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO vehicle_soc (id, soc, range_km, observed_at, source)
		VALUES (1, 38, 122, '2026-11-09T15:45:00Z', 'demo')`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r, err := s.SoCReading(model.SoCSourceEVCC)
	if err != nil || r == nil || r.Source != model.SoCSourceEVCC || r.Percent != 38 || r.RangeKm == nil || *r.RangeKm != 122 ||
		!r.ObservedAt.Equal(time.Date(2026, 11, 9, 15, 45, 0, 0, time.UTC)) {
		t.Fatalf("migrated reading = %+v %v", r, err)
	}
}

func TestConnectionSettingsAreSparse(t *testing.T) {
	s := open(t)
	defaults := model.DefaultConnectionSettings()
	if v, err := s.ConnectionSettings(defaults); err != nil || v != defaults {
		t.Fatalf("unsaved = %+v %v", v, err)
	}
	if err := s.SaveConnectionSettings(map[string]any{"haSocEntity": "sensor.leaf_battery", "evccPollIntervalSec": 30}); err != nil {
		t.Fatal(err)
	}
	// Fields that were not saved follow the defaults they are read with.
	seeded := defaults
	seeded.EVCCURL = "http://192.0.2.10:7070"
	v, err := s.ConnectionSettings(seeded)
	if err != nil || v.HASoCEntity != "sensor.leaf_battery" || v.EVCCPollIntervalSec != 30 || v.EVCCURL != seeded.EVCCURL ||
		v.RetentionDays != 90 {
		t.Fatalf("merged = %+v %v", v, err)
	}
}

func TestLegacyPlugInSwitchBecomesATripPlanMode(t *testing.T) {
	s := open(t)
	defaults := model.Settings{TripPlans: model.TripPlansAutomatic}
	for _, c := range []struct {
		saved string
		want  string
	}{
		{`{"autoPlanOnPlugIn": true}`, model.TripPlansAutomatic},
		{`{"autoPlanOnPlugIn": false}`, model.TripPlansManual},
		{`{"usableCapacityKWh": 40}`, model.TripPlansAutomatic},
		{`{"autoPlanOnPlugIn": false, "tripPlans": "plug_in"}`, model.TripPlansPlugIn},
	} {
		if _, err := s.db.Exec(`INSERT INTO settings (key, value, updated_at) VALUES ('planning', ?, '')
			ON CONFLICT(key) DO UPDATE SET value = excluded.value`, c.saved); err != nil {
			t.Fatal(err)
		}
		got, _, err := s.Settings(defaults)
		if err != nil || got.TripPlans != c.want {
			t.Errorf("%s: trip plans = %q (%v), want %q", c.saved, got.TripPlans, err, c.want)
		}
	}
}
