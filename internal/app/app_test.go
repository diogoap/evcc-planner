package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/diogoap/evcc-planner/internal/config"
)

const fixedNow = "2026-11-09T19:00:00+01:00" // Monday evening, as in the UI mockup

type testApp struct {
	t   *testing.T
	app *App
	dir string
}

func newTestApp(t *testing.T, dir string) *testApp {
	t.Helper()
	if dir == "" {
		dir = t.TempDir()
	}
	cfgPath := filepath.Join(dir, "config.yaml")
	yaml := "demo: true\nfixed_time: " + fixedNow + "\ndata_dir: " + filepath.Join(dir, "data") +
		"\nserver:\n  web_dir: " + filepath.Join(dir, "web") + "\n"
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(config.Options{Path: cfgPath, Getenv: func(string) string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	ctx := context.Background()
	if err := a.Service.PollEVCC(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.Syncer.SyncAll(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.Service.RefreshForecasts(ctx, true); err != nil {
		t.Fatal(err)
	}
	return &testApp{t: t, app: a, dir: dir}
}

func (ta *testApp) do(method, path string, body any) (int, []byte) {
	ta.t.Helper()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			ta.t.Fatal(err)
		}
		r = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, r)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	ta.app.Handler.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

func (ta *testApp) json(method, path string, body any, wantStatus int, out any) {
	ta.t.Helper()
	code, data := ta.do(method, path, body)
	if code != wantStatus {
		ta.t.Fatalf("%s %s = %d, want %d: %s", method, path, code, wantStatus, data)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			ta.t.Fatalf("decode %s: %v", data, err)
		}
	}
}

type plan struct {
	SoC struct {
		Percent *float64 `json:"percent"`
		Live    bool     `json:"live"`
	} `json:"soc"`
	Days []struct {
		Date        string   `json:"date"`
		DistanceKm  float64  `json:"distanceKm"`
		EnergyKWh   float64  `json:"energyKWh"`
		TripCount   int      `json:"tripCount"`
		ReviewCount int      `json:"reviewCount"`
		DoneCount   int      `json:"doneCount"`
		Status      string   `json:"status"`
		RequiredSoC *float64 `json:"requiredSoc"`
		TargetSoC   *int     `json:"targetSoc"`
		ChargePct   float64  `json:"chargePct"`
		FirstDepart string   `json:"firstDepartAt"`
	} `json:"days"`
	Trips []struct {
		Key         string   `json:"key"`
		Title       string   `json:"title"`
		Date        string   `json:"date"`
		ArriveLocal string   `json:"arriveLocal"`
		EndLocal    string   `json:"endLocal"`
		DepartLocal string   `json:"departLocal"`
		Enabled     bool     `json:"enabled"`
		NeedsReview bool     `json:"needsReview"`
		Overridden  []string `json:"overridden"`
		DistanceKm  *float64 `json:"distanceKm"`
		Disabled    string   `json:"disabledReason"`
	} `json:"trips"`
	Ignored []struct {
		Reason string `json:"reason"`
	} `json:"ignored"`
	Warnings []string `json:"warnings"`
	Blocks   []struct {
		ChargePct float64 `json:"chargePct"`
		DepartAt  string  `json:"departAt"`
		Status    string  `json:"status"`
	} `json:"blocks"`
	Next *struct {
		FirstTitle string  `json:"firstTitle"`
		TargetSoC  int     `json:"targetSoc"`
		Status     string  `json:"status"`
		DepartAt   string  `json:"departAt"`
		Required   float64 `json:"requiredSoc"`
	} `json:"next"`
}

func (ta *testApp) plan() plan {
	var p plan
	ta.json("GET", "/api/plan", nil, http.StatusOK, &p)
	return p
}

func (p plan) trip(key string) (int, bool) {
	for i, t := range p.Trips {
		if t.Key == key {
			return i, true
		}
	}
	return -1, false
}

func TestDemoPlanMatchesHandCalculation(t *testing.T) {
	ta := newTestApp(t, "")
	p := ta.plan()
	if p.SoC.Percent == nil || *p.SoC.Percent != 38 || p.SoC.Live {
		t.Fatalf("demo SoC should be a last-known 38%%: %+v", p.SoC)
	}
	if len(p.Days) != 7 || p.Days[0].Date != "2026-11-09" || p.Days[0].Status != "completed" {
		t.Fatalf("days = %+v", p.Days)
	}
	tue := p.Days[1]
	if tue.DistanceKm != 100 || tue.EnergyKWh != 18 || tue.TripCount != 3 {
		t.Errorf("Tuesday = %+v, want 100 km / 18 kWh / 3 trips", tue)
	}
	// Drop-off (12 km) and the office (76 km) leave too little time at home
	// in between to charge; the afternoon pickup is recharged at midday.
	if p.Next == nil || p.Next.FirstTitle != "School drop-off" || p.Next.TargetSoC != 42 || p.Next.Status != "charge" {
		t.Fatalf("next departure = %+v", p.Next)
	}
	if !strings.HasPrefix(p.Next.DepartAt, "2026-11-10T07:48:00") {
		t.Errorf("departure %s, want 07:48 (08:15 minus 12 min drive and 15 min buffer)", p.Next.DepartAt)
	}
	if len(p.Ignored) != 1 || p.Ignored[0].Reason != "Online meeting" {
		t.Errorf("ignored = %+v", p.Ignored)
	}
	if p.Days[5].ReviewCount != 1 {
		t.Errorf("Saturday's unclear calendar location should need review: %+v", p.Days[5])
	}
}

func TestManualTripLifecycle(t *testing.T) {
	ta := newTestApp(t, "")
	var created struct{ Key string }
	ta.json("POST", "/api/trips", map[string]any{
		"title": "Hardware store", "destination": "Bouwmarkt 5, Demo Town", "date": "2026-11-15", "start": "10:00",
	}, http.StatusCreated, &created)
	if created.Key == "" {
		t.Fatal("no key returned")
	}
	p := ta.plan()
	i, ok := p.trip(created.Key)
	if !ok || p.Trips[i].EndLocal != "10:15" || p.Trips[i].DistanceKm == nil {
		t.Fatalf("created trip = %+v (default stay is 15 minutes)", p.Trips[i])
	}
	sunday := p.Days[6].EnergyKWh

	ta.json("PATCH", "/api/trips/"+created.Key, map[string]any{"start": "11:00", "end": "13:30", "distanceOverrideKm": 50}, http.StatusOK, nil)
	p = ta.plan()
	i, _ = p.trip(created.Key)
	if tr := p.Trips[i]; tr.ArriveLocal != "11:00" || tr.EndLocal != "13:30" || *tr.DistanceKm != 50 {
		t.Fatalf("edited trip = %+v", tr)
	}

	ta.json("PATCH", "/api/trips/"+created.Key, map[string]any{"enabled": false}, http.StatusOK, nil)
	p = ta.plan()
	if p.Days[6].EnergyKWh != 0 || sunday == 0 {
		t.Errorf("disabled trip still counted: before %.2f, after %.2f kWh", sunday, p.Days[6].EnergyKWh)
	}

	ta.json("DELETE", "/api/trips/"+created.Key, nil, http.StatusNoContent, nil)
	if _, ok := ta.plan().trip(created.Key); ok {
		t.Fatal("deleted trip still listed")
	}

	ta.json("POST", "/api/trips", map[string]any{"date": "2026-11-15", "start": "10:00"}, http.StatusBadRequest, nil)
	ta.json("POST", "/api/trips", map[string]any{"destination": "X", "date": "2026-11-15", "start": "25:00"}, http.StatusBadRequest, nil)
	ta.json("DELETE", "/api/trips/recurring:1:2026-11-10", nil, http.StatusBadRequest, nil)
	ta.json("PATCH", "/api/trips/manual:999", map[string]any{"enabled": false}, http.StatusNotFound, nil)
	ta.json("PATCH", "/api/trips/nonsense", map[string]any{"enabled": false}, http.StatusBadRequest, nil)
}

func TestOccurrenceEditsSurviveTemplateChanges(t *testing.T) {
	ta := newTestApp(t, "")
	key := "recurring:1:2026-11-12" // Thursday drop-off
	ta.json("PATCH", "/api/trips/"+key, map[string]any{"start": "08:45", "end": "08:50"}, http.StatusOK, nil)

	var tpls []struct {
		ID    int64  `json:"id"`
		Title string `json:"title"`
	}
	ta.json("GET", "/api/recurring", nil, http.StatusOK, &tpls)
	ta.json("PUT", "/api/recurring/1", map[string]any{
		"title": "School run", "destination": "School", "weekdays": []int{1, 2, 3, 4, 5},
		"start": "08:10", "end": "08:20", "enabled": true, "exceptions": []any{},
	}, http.StatusOK, nil)

	p := ta.plan()
	i, ok := p.trip(key)
	if !ok || p.Trips[i].Title != "School run" || p.Trips[i].ArriveLocal != "08:45" {
		t.Fatalf("edited date should keep 08:45 and take the new title: %+v", p.Trips[i])
	}
	j, _ := p.trip("recurring:1:2026-11-13")
	if p.Trips[j].ArriveLocal != "08:10" {
		t.Errorf("unedited date should follow the template: %+v", p.Trips[j])
	}

	// Disabling and re-enabling leaves no override behind.
	fri := "recurring:2:2026-11-13"
	ta.json("PATCH", "/api/trips/"+fri, map[string]any{"enabled": false}, http.StatusOK, nil)
	ta.json("PATCH", "/api/trips/"+fri, map[string]any{"enabled": true}, http.StatusOK, nil)
	p = ta.plan()
	k, _ := p.trip(fri)
	if len(p.Trips[k].Overridden) != 0 {
		t.Errorf("re-enabled date should have no overrides: %+v", p.Trips[k])
	}

	ta.json("POST", "/api/trips/"+key+"/reset", map[string]any{}, http.StatusNoContent, nil)
	p = ta.plan()
	i, _ = p.trip(key)
	if p.Trips[i].ArriveLocal != "08:10" {
		t.Errorf("reset should restore the template time: %+v", p.Trips[i])
	}

	ta.json("POST", "/api/recurring", map[string]any{"title": "Bad", "destination": "X", "weekdays": []int{}, "start": "08:00", "end": "09:00"}, http.StatusBadRequest, nil)
	ta.json("DELETE", "/api/recurring/1", nil, http.StatusNoContent, nil)
	if _, ok := ta.plan().trip("recurring:1:2026-11-10"); ok {
		t.Error("deleted schedule still produces trips")
	}
}

func TestCalendarTripReview(t *testing.T) {
	ta := newTestApp(t, "")
	p := ta.plan()
	var key string
	for _, tr := range p.Trips {
		if tr.NeedsReview {
			key = tr.Key
		}
	}
	if key == "" {
		t.Fatal("demo calendar should include a trip needing review")
	}
	ta.json("PATCH", "/api/trips/"+key, map[string]any{"reviewed": true}, http.StatusOK, nil)
	// A sync must not undo the local review.
	if err := ta.app.Syncer.SyncAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	p = ta.plan()
	i, _ := p.trip(key)
	if p.Trips[i].NeedsReview {
		t.Errorf("review lost after sync: %+v", p.Trips[i])
	}
}

func TestSettingsPersistAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	ta := newTestApp(t, dir)
	var s map[string]any
	ta.json("GET", "/api/settings", nil, http.StatusOK, &s)
	if s["usableCapacityKWh"] != 59.0 || s["homeAddress"] != "Demo Home 1, Demo Town" {
		t.Fatalf("settings = %+v", s)
	}
	s["consumptionKWhPer100Km"] = 20.0
	ta.json("PUT", "/api/settings", s, http.StatusOK, nil)
	if got := ta.plan().Days[1].EnergyKWh; got != 20 {
		t.Errorf("Tuesday energy = %.2f, want 20 kWh at 20 kWh/100 km", got)
	}
	s["chargingEfficiencyPct"] = 120.0
	ta.json("PUT", "/api/settings", s, http.StatusBadRequest, nil)
	ta.json("PUT", "/api/settings", map[string]any{"unknown": 1}, http.StatusBadRequest, nil)
	ta.app.Close()

	again := newTestApp(t, dir)
	again.json("GET", "/api/settings", nil, http.StatusOK, &s)
	if s["consumptionKWhPer100Km"] != 20.0 {
		t.Errorf("settings not persisted: %+v", s)
	}
}

func TestSendPlanToEVCC(t *testing.T) {
	ta := newTestApp(t, "")
	var prev struct {
		Available bool   `json:"available"`
		Reason    string `json:"reason"`
		Slots     []any  `json:"slots"`
	}
	ta.json("GET", "/api/evcc/preview?soc=42&time=2026-11-10T06:48:00Z", nil, http.StatusOK, &prev)
	if prev.Available || prev.Reason == "" {
		t.Fatalf("unplugged car must not get an EVCC preview: %+v", prev)
	}

	ta.json("POST", "/api/demo/vehicle", map[string]any{"connected": true, "soc": 38}, http.StatusOK, nil)
	ta.json("GET", "/api/evcc/preview?soc=42&time=2026-11-10T06:48:00Z", nil, http.StatusOK, &prev)
	if !prev.Available || len(prev.Slots) == 0 {
		t.Fatalf("plugged-in preview = %+v", prev)
	}
	if p := ta.plan(); !p.SoC.Live {
		t.Error("plugged-in SoC should be live")
	}

	body := map[string]any{"soc": 42, "time": "2026-11-10T06:48:00Z", "tripKey": "recurring:1:2026-11-10"}
	ta.json("POST", "/api/evcc/plan", body, http.StatusBadRequest, nil)

	req := httptest.NewRequest("POST", "/api/evcc/plan", strings.NewReader(`{"soc":42}`))
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	ta.app.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("non-JSON write = %d, want 415", rec.Code)
	}

	body["confirm"] = true
	ta.json("POST", "/api/evcc/plan", body, http.StatusOK, nil)
	var status struct {
		Plan *struct {
			SoC          float64 `json:"soc"`
			SetByPlanner bool    `json:"setByPlanner"`
		} `json:"plan"`
	}
	ta.json("GET", "/api/status", nil, http.StatusOK, &status)
	if status.Plan == nil || status.Plan.SoC != 42 || !status.Plan.SetByPlanner {
		t.Fatalf("EVCC plan after write = %+v", status.Plan)
	}

	body["time"] = "2026-11-09T10:00:00Z"
	ta.json("POST", "/api/evcc/plan", body, http.StatusBadRequest, nil)
}

func TestHealthConnectionsAndStatic(t *testing.T) {
	ta := newTestApp(t, "")
	ta.json("GET", "/healthz", nil, http.StatusOK, nil)
	ta.json("GET", "/readyz", nil, http.StatusOK, nil)
	ta.json("GET", "/api/nope", nil, http.StatusNotFound, nil)

	var conns []struct{ Name, Status string }
	ta.json("POST", "/api/calendar/sync", map[string]any{}, http.StatusOK, &conns)
	if len(conns) < 3 || conns[0].Name != "EVCC" || conns[0].Status != "stub" || conns[1].Status != "stub" {
		t.Errorf("connections = %+v", conns)
	}

	code, body := ta.do("GET", "/trips", nil)
	if code != http.StatusNotFound || !strings.Contains(string(body), "UI not built") {
		t.Errorf("missing UI = %d %s", code, body)
	}
	web := filepath.Join(ta.dir, "web")
	os.MkdirAll(filepath.Join(web, "assets"), 0o755)
	os.WriteFile(filepath.Join(web, "index.html"), []byte("<html>app</html>"), 0o644)
	os.WriteFile(filepath.Join(web, "assets", "a.js"), []byte("js"), 0o644)
	if code, body := ta.do("GET", "/recurring", nil); code != 200 || string(body) != "<html>app</html>" {
		t.Errorf("SPA fallback = %d %s", code, body)
	}
	if code, body := ta.do("GET", "/assets/a.js", nil); code != 200 || string(body) != "js" {
		t.Errorf("asset = %d %s", code, body)
	}
	os.WriteFile(filepath.Join(web, "manifest.webmanifest"), []byte(`{"name":"x"}`), 0o644)
	req := httptest.NewRequest("GET", "/manifest.webmanifest", nil)
	rec := httptest.NewRecorder()
	ta.app.Handler.ServeHTTP(rec, req)
	if ct := rec.Header().Get("Content-Type"); rec.Code != 200 || ct != "application/manifest+json" {
		t.Errorf("manifest = %d %q", rec.Code, ct)
	}
}

func TestDemoReset(t *testing.T) {
	ta := newTestApp(t, "")
	ta.json("POST", "/api/trips", map[string]any{"destination": "X 1", "date": "2026-11-15", "start": "10:00"}, http.StatusCreated, nil)
	ta.json("POST", "/api/demo/vehicle", map[string]any{"connected": true, "soc": 70}, http.StatusOK, nil)
	ta.json("POST", "/api/demo/reset", map[string]any{}, http.StatusNoContent, nil)
	p := ta.plan()
	if p.Days[6].TripCount != 0 || p.SoC.Live || p.Next == nil || p.Next.TargetSoC != 42 {
		t.Fatalf("reset did not restore the seeded demo: %+v %+v", p.Days[6], p.SoC)
	}
}

func TestEmptyTitleFallsBackToSource(t *testing.T) {
	ta := newTestApp(t, "")
	key := "recurring:2:2026-11-10"
	ta.json("PATCH", "/api/trips/"+key, map[string]any{"title": "Pickup + groceries"}, http.StatusOK, nil)
	ta.json("PATCH", "/api/trips/"+key, map[string]any{"title": "  "}, http.StatusOK, nil)
	p := ta.plan()
	i, _ := p.trip(key)
	if p.Trips[i].Title != "School pickup" || len(p.Trips[i].Overridden) != 0 {
		t.Fatalf("cleared title should restore the schedule title: %+v", p.Trips[i])
	}
}

func TestTripsWithoutRouteAreDisabled(t *testing.T) {
	ta := newTestApp(t, "")
	p := ta.plan()
	var key string
	for _, tr := range p.Trips {
		if tr.Title == "Piano lesson" {
			key = tr.Key
		}
	}
	i, ok := p.trip(key)
	if !ok {
		t.Fatal("calendar event without location should be listed as a trip")
	}
	if tr := p.Trips[i]; tr.Enabled || tr.Disabled != "No location" || tr.DistanceKm != nil {
		t.Fatalf("no-location trip = %+v, want disabled with reason", tr)
	}
	if fri := p.Days[4]; fri.TripCount != 2 || fri.EnergyKWh != 4.32 {
		t.Errorf("Friday should count only the school runs: %+v", fri)
	}
	if len(p.Warnings) != 0 {
		t.Errorf("expected no warnings, got %v", p.Warnings)
	}

	// Adding a destination gives it a route and enables it.
	ta.json("PATCH", "/api/trips/"+key, map[string]any{"destination": "School"}, http.StatusOK, nil)
	p = ta.plan()
	i, _ = p.trip(key)
	if tr := p.Trips[i]; !tr.Enabled || tr.Disabled != "" || tr.DistanceKm == nil || *tr.DistanceKm != 12 {
		t.Fatalf("trip with destination = %+v", tr)
	}

	// Without a home address no route can be calculated: everything is off.
	var s map[string]any
	ta.json("GET", "/api/settings", nil, http.StatusOK, &s)
	s["homeAddress"] = ""
	ta.json("PUT", "/api/settings", s, http.StatusOK, nil)
	p = ta.plan()
	for _, tr := range p.Trips {
		if tr.Enabled || tr.Disabled != "Home address not set" {
			t.Fatalf("trip without home = %+v", tr)
		}
	}
	if p.Next != nil || len(p.Warnings) != 1 {
		t.Errorf("want no departure and only the home warning: next=%+v warnings=%v", p.Next, p.Warnings)
	}
}

func TestPastTripsHiddenAndDayTargetMatchesFirstDeparture(t *testing.T) {
	ta := newTestApp(t, "")
	p := ta.plan()
	// At Monday 19:00 both school runs are back home: hidden, day completed.
	for _, tr := range p.Trips {
		if tr.Date == "2026-11-09" {
			t.Errorf("past trip still listed: %+v", tr)
		}
	}
	if mon := p.Days[0]; mon.Status != "completed" || mon.DoneCount != 2 || mon.DistanceKm != 0 || mon.TripCount != 0 {
		t.Errorf("Monday = %+v", mon)
	}
	tue := p.Days[1]
	sum := 0.0
	for _, b := range p.Blocks {
		if strings.HasPrefix(b.DepartAt, "2026-11-10") && b.Status != "done" {
			sum += b.ChargePct
		}
	}
	if sum <= 0 || math.Abs(tue.ChargePct-sum) > 0.15 {
		t.Errorf("Tuesday charge = %.1f, want the sum of its departures' charge %.1f", tue.ChargePct, sum)
	}
	if tue.TargetSoC == nil || *tue.TargetSoC != p.Next.TargetSoC || tue.FirstDepart != p.Next.DepartAt {
		t.Errorf("Tuesday target %v at %s should match the next departure %d%% at %s",
			tue.TargetSoC, tue.FirstDepart, p.Next.TargetSoC, p.Next.DepartAt)
	}

	// Sunday: a short morning trip, then a long evening trip with time to
	// charge in between. The day shows the morning target, not the larger
	// evening one.
	ta.json("POST", "/api/trips", map[string]any{"destination": "Short 1, Demo Town", "date": "2026-11-15",
		"start": "09:00", "distanceOverrideKm": 10}, http.StatusCreated, nil)
	ta.json("POST", "/api/trips", map[string]any{"destination": "Far 2, Demo Town", "date": "2026-11-15",
		"start": "18:00", "end": "20:00", "distanceOverrideKm": 80}, http.StatusCreated, nil)
	sun := ta.plan().Days[6]
	morning := 10*0.18/59*100 + 15
	if sun.TargetSoC == nil || *sun.TargetSoC != int(math.Ceil(morning)) || sun.RequiredSoC == nil || *sun.RequiredSoC > 25 {
		t.Errorf("Sunday target = %v (%v), want the morning trip's %.0f%%", sun.TargetSoC, sun.RequiredSoC, math.Ceil(morning))
	}
}
