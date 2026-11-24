package service

import (
	"testing"
	"time"

	"github.com/diogoap/evcc-planner/internal/evcc"
	"github.com/diogoap/evcc-planner/internal/localtime"
	"github.com/diogoap/evcc-planner/internal/model"
)

func TestDayPricesCoverTheLocalDay(t *testing.T) {
	ams, _ := time.LoadLocation("Europe/Amsterdam")
	// The clocks go back: 25 hours of quarter-hour slots, priced by hour.
	day, _ := localtime.ParseDate(ams, "2026-10-25")
	end := localtime.AddDays(day, 1)
	var slots []evcc.Rate
	for t := day.Add(-time.Hour); t.Before(end.Add(time.Hour)); t = t.Add(15 * time.Minute) {
		slots = append(slots, evcc.Rate{Start: t, End: t.Add(15 * time.Minute), Value: float64(10+t.Sub(day)/time.Hour) / 100})
	}
	low, avg, high, hours := dayPrices(slots, day, end)
	if hours != 25 || *low != 0.1 || *high != 0.34 || *avg != 0.22 {
		t.Fatalf("DST day = %v %v %v over %v h, want 0.1 0.22 0.34 over 25 h", *low, *avg, *high, hours)
	}
	// Only the rest of the day listed (EVCC drops past slots).
	low, avg, high, hours = dayPrices(slots, day.Add(20*time.Hour), end)
	if hours != 5 || *low != 0.3 || *high != 0.34 || *avg != 0.32 {
		t.Fatalf("rest of the day = %v %v %v over %v h", *low, *avg, *high, hours)
	}
	if low, avg, high, hours := dayPrices(slots, end.Add(2*time.Hour), end.Add(26*time.Hour)); low != nil || avg != nil || high != nil || hours != 0 {
		t.Fatal("a day without slots has no prices")
	}
}

func TestMergeStatsKeepsWhatIsKnown(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	n := func(v int) *int { return &v }
	saved := &model.DayStats{
		Date: "2026-11-10", PriceMin: f(0.15), PriceAvg: f(0.25), PriceMax: f(0.4), PriceHours: 24,
		DistanceKm: f(12.8), EnergyKWh: f(2.3), WindBft: n(5), SunshinePct: n(30), SolarKWh: f(4.2),
	}
	// After midnight EVCC lists only the rest of the day, and KNMI is down.
	later := model.DayStats{
		Date: "2026-11-10", PriceMin: f(0.2), PriceAvg: f(0.3), PriceMax: f(0.4), PriceHours: 9.5,
		DistanceKm: f(40), EnergyKWh: f(7.2),
	}
	got := mergeStats(saved, later)
	if *got.PriceAvg != 0.25 || got.PriceHours != 24 || *got.PriceMin != 0.15 {
		t.Fatalf("prices covering less of the day replaced the saved ones: %+v", got)
	}
	if *got.DistanceKm != 40 || *got.EnergyKWh != 7.2 {
		t.Fatalf("new driving figures should win: %+v", got)
	}
	if *got.WindBft != 5 || *got.SunshinePct != 30 || *got.SolarKWh != 4.2 {
		t.Fatalf("unknown weather or solar replaced the saved values: %+v", got)
	}
	// Complete prices again (e.g. corrected): the newest wins.
	later.PriceHours, later.WindBft = 24, n(6)
	if got := mergeStats(saved, later); *got.PriceAvg != 0.3 || *got.WindBft != 6 {
		t.Fatalf("complete new figures should win: %+v", got)
	}
	if got := mergeStats(nil, later); got.PriceHours != 24 || got.SunshinePct != nil {
		t.Fatalf("first save = %+v", got)
	}
}
