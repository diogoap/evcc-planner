package app

import (
	"context"
	"math"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/diogoap/evcc-planner/internal/localtime"
	"github.com/diogoap/evcc-planner/internal/model"
)

type historyView struct {
	Today string           `json:"today"`
	First string           `json:"first"`
	Days  []model.DayStats `json:"days"`
}

func (ta *testApp) history(query string) historyView {
	ta.t.Helper()
	var v historyView
	ta.json(http.MethodGet, "/api/history"+query, nil, http.StatusOK, &v)
	return v
}

// stubDay summarizes the demo EVCC's prices for a local day.
func (ta *testApp) stubDay(date string) (low, avg, high float64) {
	ta.t.Helper()
	st, err := ta.app.Stub.State(context.Background())
	if err != nil {
		ta.t.Fatal(err)
	}
	low, high = math.Inf(1), math.Inf(-1)
	n := 0.0
	for _, r := range st.PriceSlots {
		if localtime.Date(r.Start, ta.app.Cfg.Location) == date {
			low, high, avg, n = math.Min(low, r.Value), math.Max(high, r.Value), avg+r.Value, n+1
		}
	}
	if n != 96 {
		ta.t.Fatalf("%s has %v demo slots, want 96", date, n)
	}
	return low, math.Round(avg/n*10000) / 10000, high
}

func TestHistorySavesEachDayAsItStartsAndInTheEvening(t *testing.T) {
	ta := newTestApp(t, "") // Monday 19:00
	ctx := context.Background()
	ams := ta.app.Cfg.Location
	save := func() historyView {
		t.Helper()
		if err := ta.app.Service.RecordHistory(ctx); err != nil {
			t.Fatal(err)
		}
		return ta.history("")
	}

	// The history starts empty; nothing is filled in for past days.
	if before := ta.history(""); before.Today != "2026-11-09" || before.First != "" || len(before.Days) != 0 {
		t.Fatalf("history before recording = %+v", before)
	}
	// The first check of the day saves today only (not tomorrow, although
	// its prices are published).
	got := save()
	if got.First != "2026-11-09" || len(got.Days) != 1 || got.Days[0].Date != "2026-11-09" {
		t.Fatalf("only today should be saved: %+v", got.Days)
	}
	mon := got.Days[0]
	low, avg, high := ta.stubDay("2026-11-09")
	if *mon.PriceMin != low || *mon.PriceAvg != avg || *mon.PriceMax != high || mon.PriceHours != 24 {
		t.Fatalf("prices = %v %v %v over %v h, want %v %v %v over 24 h",
			*mon.PriceMin, *mon.PriceAvg, *mon.PriceMax, mon.PriceHours, low, avg, high)
	}
	// Both school runs count although they are driven already (the plan
	// itself only counts trips still ahead).
	if *mon.DistanceKm != 24 || *mon.EnergyKWh <= 0 || ta.plan().Days[0].DistanceKm != 0 {
		t.Fatalf("driving = %v km, %v kWh", *mon.DistanceKm, *mon.EnergyKWh)
	}
	// The demo KNMI outlook for Monday: 20% sun, Bft 3.
	if *mon.SunshinePct != 20 || *mon.WindBft != 3 {
		t.Fatalf("weather = %d%% Bft %d", *mon.SunshinePct, *mon.WindBft)
	}

	// A trip added in the evening changes nothing until 23:45.
	var created struct{ Key string }
	ta.json("POST", "/api/trips", map[string]any{
		"title": "Late visit", "destination": "Parkweg 20, Demo Town", "date": "2026-11-09", "start": "20:00",
		"distanceOverrideKm": 30,
	}, http.StatusCreated, &created)
	p := ta.plan()
	i, ok := p.trip(created.Key)
	if !ok || p.Trips[i].DistanceKm == nil {
		t.Fatalf("the late visit is not planned: %+v", p.Trips)
	}
	visit := *p.Trips[i].DistanceKm
	ta.at(time.Date(2026, 11, 9, 23, 44, 0, 0, ams))
	if again := save(); !reflect.DeepEqual(again.Days, got.Days) {
		t.Fatalf("saved again before the evening: %+v", again.Days)
	}
	ta.at(time.Date(2026, 11, 9, 23, 45, 0, 0, ams))
	evening := save().Days[0]
	if *evening.DistanceKm != 24+visit || *evening.PriceAvg != avg || evening.PriceHours != 24 ||
		!evening.UpdatedAt.Equal(time.Date(2026, 11, 9, 23, 45, 0, 0, ams)) {
		t.Fatalf("evening save = %+v, want %v km", evening, 24+visit)
	}
	ta.at(time.Date(2026, 11, 9, 23, 59, 0, 0, ams))
	if last := save().Days[0]; !reflect.DeepEqual(last, evening) {
		t.Fatal("saved a third time")
	}

	// The next day: Tuesday starts, Monday no longer changes.
	ta.at(time.Date(2026, 11, 10, 0, 1, 0, 0, ams))
	next := save()
	if next.Today != "2026-11-10" || len(next.Days) != 2 || !reflect.DeepEqual(next.Days[0], evening) {
		t.Fatalf("after midnight: today %s, days %+v", next.Today, next.Days)
	}
	if tue := next.Days[1]; tue.Date != "2026-11-10" || tue.PriceHours != 24 || *tue.WindBft != 5 || *tue.DistanceKm != 100 {
		t.Fatalf("Tuesday = %+v (school runs and the office visit: 100 km; windy)", tue)
	}
	if fromTue := ta.history("?from=2026-11-10&to=2026-11-30"); len(fromTue.Days) != 1 || fromTue.First != "2026-11-09" {
		t.Fatalf("from Tuesday = %+v", fromTue)
	}
	if code, _ := ta.do(http.MethodGet, "/api/history?from=9-11-2026", nil); code != http.StatusBadRequest {
		t.Fatalf("a bad date = %d, want 400", code)
	}
}

func TestHistoryWaitsForTheFirstEVCCCheck(t *testing.T) {
	ta := openTestApp(t, "", fixedNow) // just started: EVCC not checked yet
	ctx := context.Background()
	if err := ta.app.Service.RecordHistory(ctx); err != nil {
		t.Fatal(err)
	}
	if h := ta.history(""); len(h.Days) != 0 {
		t.Fatalf("saved before EVCC was checked: %+v", h.Days)
	}
	if err := ta.app.Service.PollEVCC(ctx); err != nil {
		t.Fatal(err)
	}
	if err := ta.app.Service.RecordHistory(ctx); err != nil {
		t.Fatal(err)
	}
	if h := ta.history(""); len(h.Days) != 1 || h.Days[0].PriceHours != 24 {
		t.Fatalf("after the first EVCC check: %+v", h.Days)
	}
}
