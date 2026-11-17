package store

import (
	"testing"
	"time"

	"github.com/diogoap/evcc-planner/internal/model"
)

func TestDayStatsRoundTripAndKeptForever(t *testing.T) {
	s := open(t)
	if first, err := s.FirstDayStats(); err != nil || first != "" {
		t.Fatalf("empty history: first = %q, %v", first, err)
	}
	at := time.Date(2024, 3, 1, 22, 50, 0, 0, time.UTC)
	f := func(v float64) *float64 { return &v }
	n := func(v int) *int { return &v }
	full := model.DayStats{
		Date: "2024-03-01", PriceMin: f(0.18), PriceAvg: f(0.2512), PriceMax: f(0.39), PriceHours: 24,
		DistanceKm: f(12.8), EnergyKWh: f(2.3), WindBft: n(4), SunshinePct: n(35), UpdatedAt: at,
	}
	for _, d := range []model.DayStats{
		full,
		{Date: "2024-03-02", PriceHours: 0, DistanceKm: f(0), EnergyKWh: f(0), UpdatedAt: at}, // nothing else known
		{Date: "2024-03-03", PriceMin: f(0.2), PriceAvg: f(0.2), PriceMax: f(0.2), PriceHours: 1, UpdatedAt: at},
	} {
		if err := s.SaveDayStats(d); err != nil {
			t.Fatal(err)
		}
	}
	all, err := s.DayStats("", "")
	if err != nil || len(all) != 3 || all[0].Date != "2024-03-01" || all[2].Date != "2024-03-03" {
		t.Fatalf("all days = %+v, %v", all, err)
	}
	got := all[0]
	if *got.PriceMin != 0.18 || *got.PriceAvg != 0.2512 || *got.PriceMax != 0.39 || got.PriceHours != 24 ||
		*got.DistanceKm != 12.8 || *got.EnergyKWh != 2.3 || *got.WindBft != 4 || *got.SunshinePct != 35 || !got.UpdatedAt.Equal(at) {
		t.Fatalf("round trip = %+v", got)
	}
	if d := all[1]; d.PriceAvg != nil || d.WindBft != nil || d.SunshinePct != nil || d.DistanceKm == nil || *d.DistanceKm != 0 {
		t.Fatalf("unknown values must stay unknown and zero stay zero: %+v", d)
	}
	if r, _ := s.DayStats("2024-03-02", ""); len(r) != 2 || r[0].Date != "2024-03-02" {
		t.Fatalf("from = %+v", r)
	}
	if r, _ := s.DayStats("2024-03-02", "2024-03-02"); len(r) != 1 {
		t.Fatalf("one day = %+v", r)
	}

	full.PriceAvg, full.WindBft = f(0.3), nil
	if err := s.SaveDayStats(full); err != nil {
		t.Fatal(err)
	}
	if d, _ := s.DayStat("2024-03-01"); d == nil || *d.PriceAvg != 0.3 || d.WindBft != nil {
		t.Fatalf("saving again replaces the day: %+v", d)
	}
	if d, err := s.DayStat("2024-03-09"); d != nil || err != nil {
		t.Fatalf("missing day = %+v, %v", d, err)
	}

	// The retention cleanup leaves the history alone.
	if _, err := s.Cleanup(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.UTC); err != nil {
		t.Fatal(err)
	}
	if first, _ := s.FirstDayStats(); first != "2024-03-01" {
		t.Fatalf("history after cleanup starts %q", first)
	}
	if err := s.Reset(); err != nil {
		t.Fatal(err)
	}
	if first, _ := s.FirstDayStats(); first != "" {
		t.Fatalf("a demo reset clears the history, first = %q", first)
	}
}
