package advice

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func published(v float64) *DayPrice {
	return &DayPrice{Value: v, Basis: BasisPublished, Detail: "published"}
}
func estimate(v float64) *DayPrice {
	return &DayPrice{Value: v, Basis: BasisEstimate, Detail: "outlook"}
}
func weekly(v float64) *DayPrice { return &DayPrice{Value: v, Basis: BasisWeekly, Detail: "weekly"} }
func bft(v int) *int             { return &v }

var dates = []string{"2026-11-09", "2026-11-10", "2026-11-11", "2026-11-12", "2026-11-13", "2026-11-14", "2026-11-15"}
var labels = []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}

func week(prices ...*DayPrice) []Day {
	days := make([]Day, len(prices))
	for i, p := range prices {
		days[i] = Day{Date: dates[i], Label: labels[i], Price: p, HomeHours: 16}
	}
	return days
}

func dep(day int, charge, energy float64) Departure {
	t, _ := time.Parse("2006-01-02", dates[day])
	return Departure{Date: dates[day], DepartAt: t.Add(8 * time.Hour), ChargePct: charge, EnergyPct: energy}
}

func TestChargeAheadOnACheapPublishedDay(t *testing.T) {
	in := Input{
		Days:       week(published(0.27), published(0.218), weekly(0.25), estimate(0.298), weekly(0.25), nil, nil),
		Departures: []Departure{dep(1, 4, 10), dep(2, 3, 4), dep(3, 8, 8), dep(4, 6, 6)},
		SoCKnown:   true,
	}
	got := Advise(in)
	tue := got[1]
	// Weekly averages are not day-specific but still count as later price
	// levels: Tuesday is compared with Wednesday's, so confidence is low.
	if tue.Recommendation != ChargeAhead || tue.Confidence != 1 || tue.CompareDate != "2026-11-11" {
		t.Fatalf("Tuesday = %+v", tue)
	}
	// The outlook naming Tuesday the week's cheapest day makes it medium.
	in.Days[1].Level = "cheapest"
	got = Advise(in)
	tue = got[1]
	if tue.Confidence != 2 {
		t.Fatalf("ranked Tuesday = %+v", tue)
	}
	// 0.25 vs 0.218 is 14.7% → little; later trips need 17% → medium cap.
	if tue.Indicator != "little" || tue.LaterChargePct != 17 {
		t.Errorf("indicator %s, later %.1f", tue.Indicator, tue.LaterChargePct)
	}
	if !strings.Contains(strings.Join(tue.Rationale, " "), "need about +17% SoC") {
		t.Errorf("rationale = %v", tue.Rationale)
	}
	// Monday: Tuesday's prices (including its night, before the 08:00
	// departure) are cheaper and no departure falls before Tuesday.
	if mon := got[0]; mon.Recommendation != Defer || mon.DeferUntil != "2026-11-10" || mon.Confidence != 3 {
		t.Errorf("Monday = %+v", mon)
	}
	if wed := got[2]; !wed.NoSuggestion || !strings.Contains(wed.Reason, "weekly average") || wed.Confidence != 0 {
		t.Errorf("Wednesday = %+v", wed)
	}
	if sat := got[5]; !sat.NoSuggestion || !strings.Contains(sat.Reason, "No published prices") {
		t.Errorf("Saturday = %+v", sat)
	}
}

func TestKeepThePlanWhenTripsBeforeTheCheaperDayNeedCharge(t *testing.T) {
	in := Input{
		Days:       week(published(0.30), published(0.31), estimate(0.24)),
		Departures: []Departure{dep(1, 5, 5), dep(2, 6, 10)},
		SoCKnown:   true,
	}
	if mon := Advise(in)[0]; mon.Recommendation != None || mon.CompareDate != "2026-11-11" || !strings.Contains(mon.Summary, "Keep the plan") {
		t.Errorf("Monday = %+v", mon)
	}
}

func TestDeferIgnoresTripsBeforeTheDay(t *testing.T) {
	// Thursday is dear and Friday cheaper; Tuesday's trip needs charge, but
	// that happens before Thursday and does not block deferring Thursday.
	in := Input{
		Days:       week(published(0.25), published(0.26), estimate(0.27), estimate(0.30), estimate(0.24)),
		Departures: []Departure{dep(1, 8, 8), dep(3, 0, 5), dep(4, 5, 5)},
		SoCKnown:   true,
	}
	if thu := Advise(in)[3]; thu.Recommendation != Defer || thu.DeferUntil != "2026-11-13" {
		t.Errorf("Thursday = %+v", thu)
	}
}

func TestDeferWhenTheChargeCoversTheTripsUntilACheaperDay(t *testing.T) {
	in := Input{
		Days:       week(published(0.30), published(0.31), estimate(0.24), estimate(0.26)),
		Departures: []Departure{dep(1, 0, 5), dep(2, 0, 5), dep(3, 6, 10)},
		SoCKnown:   true,
	}
	total, home := 9.5, 6.2
	in.Days[2].SolarKWh, in.Days[2].SolarHomeKWh = &total, &home
	in.Days[2].Solar = &Solar{Pct: 6, Price: 0.15, GridPrice: 0.26}
	got := Advise(in)
	if mon := got[0]; mon.Recommendation != Defer || mon.DeferUntil != "2026-11-11" || mon.Confidence != 2 {
		t.Fatalf("Monday = %+v", mon)
	}
	if !strings.Contains(strings.Join(got[0].Rationale, " "), "about 6 kWh of local solar while the car is home") {
		t.Errorf("rationale should mention the local solar: %v", got[0].Rationale)
	}
	if !strings.Contains(strings.Join(got[2].Rationale, " "), "Sun: 9.5 kWh local solar forecast (EVCC), 6.2 kWh while the car is home.") {
		t.Errorf("the day's own rationale should show its solar: %v", got[2].Rationale)
	}

	// Sun too weak to reach the charger's minimum: no claim that it may
	// cover the charging, and the day says why.
	in.Days[2].Solar, in.Days[2].Name, in.SolarMinKW = nil, "Wednesday", 1.38
	got = Advise(in)
	if strings.Contains(strings.Join(got[0].Rationale, " "), "local solar") {
		t.Errorf("weak sun should not count in the deferral: %v", got[0].Rationale)
	}
	if want := "Wednesday: the sun stays below what the charger needs (about 1.4 kW above household use), so it can't charge the car."; !slices.Contains(got[2].Rationale, want) {
		t.Errorf("the weak sun should be explained: %v", got[2].Rationale)
	}

	// After EVCC's forecast, KNMI's sunshine stands in.
	in.Days[2].SolarKWh, in.Days[2].SolarHomeKWh, in.Days[2].SunshinePct = nil, nil, bft(60)
	got = Advise(in)
	if !strings.Contains(strings.Join(got[0].Rationale, " "), "Wed is sunny (60% of possible sunshine, KNMI)") {
		t.Errorf("KNMI fallback in the deferral: %v", got[0].Rationale)
	}
	if !strings.Contains(strings.Join(got[2].Rationale, " "), "Sun: 60% of possible sunshine (KNMI national outlook") {
		t.Errorf("KNMI fallback for the day: %v", got[2].Rationale)
	}
	if got[0].Indicator != "" {
		t.Errorf("defer has no charge indicator: %q", got[0].Indicator)
	}
	// With an unknown SoC the charge cannot be shown to cover the trips.
	in.SoCKnown = false
	if mon := Advise(in)[0]; mon.Recommendation != None || !strings.Contains(mon.Summary, "SoC is unknown") {
		t.Errorf("unknown SoC = %+v", mon)
	}
}

func TestSimilarPricesAndNoComparison(t *testing.T) {
	in := Input{Days: week(published(0.25), published(0.26)), Departures: []Departure{dep(1, 5, 5)}, SoCKnown: true}
	got := Advise(in)
	if got[0].Recommendation != None || got[0].NoSuggestion || got[0].Confidence != 3 || !strings.Contains(got[0].Summary, "similar") {
		t.Errorf("similar = %+v", got[0])
	}
	if !got[1].NoSuggestion || !strings.Contains(got[1].Reason, "compare") {
		t.Errorf("last priced day = %+v", got[1])
	}
}

func TestCheapDayWithoutLaterNeed(t *testing.T) {
	in := Input{Days: week(published(0.20), published(0.30)), Departures: []Departure{dep(1, 0, 5)}, SoCKnown: true}
	if mon := Advise(in)[0]; mon.Recommendation != None || !strings.Contains(mon.Summary, "need no extra charge") {
		t.Errorf("Monday = %+v", mon)
	}
	// An unknown SoC counts the later trips' energy.
	in.SoCKnown = false
	if mon := Advise(in)[0]; mon.Recommendation != ChargeAhead || mon.LaterChargePct != 5 {
		t.Errorf("unknown SoC = %+v", mon)
	}
}

func TestConfidenceAndIndicator(t *testing.T) {
	// A calm day estimated as cheap is doubtful: medium becomes low.
	in := Input{Days: week(estimate(0.20), estimate(0.30)), Departures: []Departure{dep(1, 30, 30)}, SoCKnown: true}
	if c := Advise(in)[0].Confidence; c != 2 {
		t.Fatalf("estimate vs estimate = %d", c)
	}
	in.Days[0].WindBft = bft(2)
	if a := Advise(in)[0]; a.Confidence != 1 || a.Indicator != "high" {
		t.Errorf("calm cheap estimate = %+v", a)
	}
	in.Days[0].WindBft = nil
	in.Days[1].Price.Stale = true
	if c := Advise(in)[0].Confidence; c != 1 {
		t.Errorf("stale = %d", c)
	}
	cases := []struct {
		diff, need float64
		want       string
	}{{0.10, 30, "little"}, {0.20, 30, "medium"}, {0.30, 30, "high"}, {0.30, 20, "medium"}, {0.30, 5, "little"}}
	for _, c := range cases {
		if got := indicator(c.diff, c.need); got != c.want {
			t.Errorf("indicator(%.2f, %.0f) = %s, want %s", c.diff, c.need, got, c.want)
		}
	}
}

func TestAwayAllDay(t *testing.T) {
	in := Input{Days: week(published(0.20), published(0.30)), Departures: []Departure{dep(1, 10, 10)}, SoCKnown: true}
	in.Days[0].HomeHours = 0.5
	if mon := Advise(in)[0]; mon.Recommendation != None || mon.NoSuggestion || !strings.Contains(mon.Summary, "Little time at home") {
		t.Errorf("away = %+v", mon)
	}
}

// Friday at noon, plugged in at 29%: Friday's published prices average
// €0.42, Saturday has only the weekly average, Sunday an estimate of €0.33.
// The charge covers Friday and Saturday; Monday needs +16%.
func solarFriday() Input {
	day := func(date, label, name string, p *DayPrice) Day {
		return Day{Date: date, Label: label, Name: name, Price: p, HomeHours: 16}
	}
	monday := time.Date(2026, 11, 16, 7, 30, 0, 0, time.UTC)
	return Input{
		Days: []Day{
			day("2026-11-13", "Fri", "Friday", published(0.42)),
			day("2026-11-14", "Sat", "Saturday", weekly(0.46)),
			day("2026-11-15", "Sun", "Sunday", estimate(0.33)),
			day("2026-11-16", "Mon", "Monday", nil),
		},
		Departures: []Departure{{Date: "2026-11-16", DepartAt: monday, ChargePct: 16, EnergyPct: 22}},
		SoCKnown:   true,
	}
}

func withSun(in Input, s *Solar) Input {
	in.Days[0].Solar = s
	return in
}

func TestChargeAheadFromSolarWhenTheSunBeatsLaterDays(t *testing.T) {
	from, until := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC), time.Date(2026, 11, 13, 15, 0, 0, 0, time.UTC)
	got := Advise(withSun(solarFriday(), &Solar{Pct: 4, Price: 0.27, GridPrice: 0.38, From: from, Until: until}))[0]
	if got.Recommendation != ChargeAhead || !got.Solar || got.CompareDate != "2026-11-15" {
		t.Fatalf("Friday = %+v", got)
	}
	// Sunday is an estimate: medium confidence; the sun covers 4 of the 16%.
	if got.Confidence != 2 || got.Indicator != "little" || got.SolarPct != 4 || *got.SolarPrice != 0.27 || *got.SolarGridPrice != 0.38 {
		t.Errorf("confidence %d, indicator %s, solar %.1f%% at %v", got.Confidence, got.Indicator, got.SolarPct, *got.SolarPrice)
	}
	if !got.SolarFrom.Equal(from) || !got.SolarUntil.Equal(until) {
		t.Errorf("solar window %v–%v", got.SolarFrom, got.SolarUntil)
	}
	// The grid price leads; the decision counts it without the tax.
	if want := "Today's sun charges at about €0.38/kWh, or €0.27 without the tax you don't pay on own solar: " +
		"cheaper than Sunday (about €0.33). Keep the car plugged in."; got.Summary != want {
		t.Errorf("summary = %q", got.Summary)
	}
}

func TestDeferOnlyWhenALaterDayBeatsTheSun(t *testing.T) {
	// The sun at €0.32 is not clearly cheaper than Sunday's €0.33: Sunday
	// still beats Friday's grid, so defer, and say the sun still charges.
	got := Advise(withSun(solarFriday(), &Solar{Pct: 4, Price: 0.32}))[0]
	if got.Recommendation != Defer || got.Solar || got.DeferUntil != "2026-11-15" {
		t.Fatalf("Friday = %+v", got)
	}
	if want := "Sunday looks cheaper and your charge covers the trips until then. Solar still charges while the car is plugged in."; got.Summary != want {
		t.Errorf("summary = %q", got.Summary)
	}
	// No sun: the plain defer, with Sunday's full name.
	got = Advise(solarFriday())[0]
	if got.Recommendation != Defer || got.Summary != "Sunday looks cheaper and your charge covers the trips until then." {
		t.Errorf("without sun: %+v", got)
	}
	// A sliver of sun (under 1%) is not worth advising on.
	got = Advise(withSun(solarFriday(), &Solar{Pct: 0.5, Price: 0.2}))[0]
	if got.Solar || got.Recommendation != Defer {
		t.Errorf("sliver of sun: %+v", got)
	}
}
