package advice

import (
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
func bft(v int) *int             { return &v }
func f(v float64) *float64       { return &v }
func sunshine(v int) *int        { return &v }
func sameDay(h, m int) time.Time { return time.Date(2026, 11, 9, h, m, 0, 0, time.UTC) }

var dates = []string{"2026-11-09", "2026-11-10", "2026-11-11", "2026-11-12", "2026-11-13", "2026-11-14", "2026-11-15"}
var names = []string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"}

// week is seven days with published prices on the first `published` days
// and outlook estimates after, all at home.
func week(published int, prices ...*DayPrice) []Day {
	days := make([]Day, 7)
	for i := range days {
		days[i] = Day{Date: dates[i], Label: names[i][:3], Name: names[i], HomeHours: 16, Published: i < published}
		if i < len(prices) {
			days[i].Price = prices[i]
		}
	}
	return days
}

// blocks are quarter-hours from 00:00 at the given grid prices, each
// adding 1.5% (about 3.68 kW at 90% into 59 kWh).
func blocks(prices ...float64) []Block {
	out := make([]Block, len(prices))
	for i, p := range prices {
		start := sameDay(0, 0).Add(time.Duration(i) * 15 * time.Minute)
		out[i] = Block{Start: start, End: start.Add(15 * time.Minute), Cost: p, SlotPrice: p, Pct: 1.5}
	}
	return sortedBlocks(out)
}

func sortedBlocks(b []Block) []Block {
	for i := 1; i < len(b); i++ {
		for j := i; j > 0 && b[j].Cost < b[j-1].Cost; j-- {
			b[j], b[j-1] = b[j-1], b[j]
		}
	}
	return b
}

// repeat is n times the price.
func repeat(n int, p float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = p
	}
	return out
}

func levels() map[string]Level {
	return map[string]Level{
		IndicationLittle: {Enabled: true, Steps: []Step{{20, 50}, {30, 80}, {50, 100}}},
		IndicationMedium: {Enabled: true, Steps: []Step{{30, 40}, {45, 60}, {60, 80}}},
		IndicationHigh:   {Enabled: false, Steps: []Step{{40, 30}, {60, 50}, {75, 70}}},
	}
}

// input is today published at home from 40%, tomorrow published with the
// given lowest price (nil: not published yet).
func input(today []Block, tomorrowLowest *float64) Input {
	days := week(2, published(0.30), published(0.28), estimate(0.29), estimate(0.30), estimate(0.31))
	days[0].Blocks, days[0].StartSoC = today, f(40)
	days[1].Lowest = tomorrowLowest
	days[1].Blocks = blocks(0.28)
	if tomorrowLowest == nil {
		days[1].Published = false
		days[1].Price = estimate(0.28)
	}
	return Input{Days: days, SoCKnown: true, Levels: levels()}
}

func TestDeferWhenTomorrowIsCheaper(t *testing.T) {
	in := input(blocks(0.25, 0.30, 0.32), f(0.15))
	a := Advise(in)[0]
	if a.Recommendation != Defer || a.DeferUntil != "2026-11-10" || a.Confidence != 3 || a.Reference.Basis != RefPublished {
		t.Fatalf("today = %+v", a)
	}
	if !strings.Contains(a.Summary, "Tuesday is cheaper (lowest €0.15 vs €0.25)") {
		t.Errorf("summary = %q", a.Summary)
	}
	// Trips before then need charge: keep the plan, and no charge extra.
	in.Departures = []Departure{{Date: dates[0], ChargePct: 5}}
	if a := Advise(in)[0]; a.Recommendation != None || !strings.HasPrefix(a.Summary, "Keep the plan: Tuesday is cheaper") {
		t.Fatalf("with a trip to charge for = %+v", a)
	}
	in.Departures, in.SoCKnown = nil, false
	if a := Advise(in)[0]; a.Recommendation != None || !strings.Contains(a.Summary, "SoC is unknown") {
		t.Fatalf("SoC unknown = %+v", a)
	}
	// A little cheaper is not enough (8%).
	if a := Advise(input(blocks(0.25, 0.30), f(0.24))); a[0].Recommendation == Defer {
		t.Fatalf("4%% cheaper deferred: %+v", a[0])
	}
}

func TestDeferWithOrWithoutTheSun(t *testing.T) {
	sunny := func(solarCost float64) Input {
		today := blocks(0.25, 0.30)
		today = append(today, Block{Start: sameDay(12, 0), End: sameDay(12, 15), Cost: solarCost, SlotPrice: solarCost + 0.11, Solar: true, Pct: 3})
		return input(sortedBlocks(today), f(0.15))
	}
	if a := Advise(sunny(0.10))[0]; a.Recommendation != Defer || !strings.Contains(a.Summary, "The sun still charges cheaper: keep the car plugged in from 12:00 to 12:15.") {
		t.Fatalf("sun cheaper than tomorrow = %q", a.Summary)
	}
	// Tomorrow's lowest is below even the sun: no need to stay plugged in.
	if a := Advise(sunny(0.20))[0]; a.Recommendation != Defer || !strings.Contains(a.Summary, "No need to keep it plugged in for the sun either.") {
		t.Fatalf("tomorrow cheaper than the sun = %q", a.Summary)
	}
}

func TestChargeExtraOnlyAsFarAsTheCheapBlocksGo(t *testing.T) {
	// Four quarter-hours at €0.10, half of tomorrow's lowest: the top step
	// (100%) applies, but those blocks add only 6%.
	in := input(blocks(append(repeat(4, 0.10), repeat(20, 0.30)...)...), f(0.20))
	a := Advise(in)[0]
	if a.Recommendation != ChargeExtra || a.TargetSoC != 46 || a.ExtraPct != 6 || a.StepDiscountPct != 50 || a.Confidence != 3 ||
		a.Indication != IndicationLittle || a.CompareDate != "2026-11-10" {
		t.Fatalf("today = %+v", a)
	}
	if len(a.Blocks) != 1 || a.Blocks[0].From != sameDay(0, 0) || a.Blocks[0].Until != sameDay(1, 0) || a.Blocks[0].Price != 0.10 || a.Blocks[0].Pct != 6 {
		t.Fatalf("blocks = %+v", a.Blocks)
	}
	if a.Summary != "Charge extra to 46%: 00:00–01:00 averages €0.10, 50% or more below tomorrow's lowest (€0.20)." {
		t.Errorf("summary = %q", a.Summary)
	}
	// Cheap pieces apart: summed up, each listed in Why?.
	split := input(blocks(0.10, 0.30, 0.10, 0.30, 0.10), f(0.20))
	if a := Advise(split)[0]; a.Summary != "Charge extra to 44%: about 45 min between 00:00 and 01:15 average €0.10, 50% or more below tomorrow's lowest (€0.20)." ||
		len(a.Blocks) != 3 {
		t.Errorf("split summary = %q (%d blocks)", a.Summary, len(a.Blocks))
	}
	// The trips' target covers it already: nothing extra.
	in.Days[0].TripTarget = 50
	if a := Advise(in)[0]; a.Recommendation != None {
		t.Fatalf("below the trip target = %+v", a)
	}
}

func TestStepsFollowTheIndicationOfCheaperDaysAhead(t *testing.T) {
	// Plenty of blocks 35% below tomorrow's lowest (€0.20), from 30%.
	in := input(blocks(repeat(60, 0.13)...), f(0.20))
	in.Days[0].StartSoC = f(30)
	if a := Advise(in)[0]; a.TargetSoC != 80 || a.StepDiscountPct != 30 || a.Indication != IndicationLittle {
		t.Fatalf("little indication = %+v", a)
	}
	// A windy Thursday: medium, so the same blocks charge only to 40%.
	in.Days[3].WindBft = bft(5)
	a := Advise(in)[0]
	if a.Indication != IndicationMedium || a.TargetSoC != 40 || a.StepDiscountPct != 30 {
		t.Fatalf("medium indication = %+v", a)
	}
	if !strings.Contains(strings.Join(a.Rationale, " "), "Thursday looks cheaper: windy.") {
		t.Errorf("rationale = %v", a.Rationale)
	}
	// A windy Thursday whose estimate is also cheaper: high, switched off.
	in.Days[3].Price = estimate(0.25)
	a = Advise(in)[0]
	if a.Indication != IndicationHigh || a.Recommendation != None || a.Summary != "The coming days look cheaper (high indication), so no extra charge." {
		t.Fatalf("high indication = %+v", a)
	}
	// A dearer estimate takes away the wind's signal.
	in.Days[3].Price = estimate(0.40)
	if a := Advise(in)[0]; a.Indication != IndicationLittle {
		t.Fatalf("windy but dearer = %+v", a)
	}
}

func TestHistoryIsTheReferenceWithoutAnOutlook(t *testing.T) {
	in := input(blocks(repeat(12, 0.18)...), nil)
	in.Days[1].Price = nil
	if a := Advise(in)[0]; !a.NoSuggestion || a.Summary != "Nothing to compare with yet: tomorrow's prices aren't published or estimated, and the price history has 0 of 3 days." {
		t.Fatalf("no history = %+v", a)
	}
	in.History = &History{LowAverage: 0.30, Days: 7}
	a := Advise(in)[0]
	if a.Recommendation != ChargeExtra || a.Confidence != 2 || a.Reference.Basis != RefHistory || a.Reference.Days != 7 || a.CompareDate != "" {
		t.Fatalf("a week of history = %+v", a)
	}
	// 40% below: little step 2, as far as 12 blocks (18%) go.
	if a.TargetSoC != 58 || a.StepDiscountPct != 30 || !strings.Contains(a.Summary, "below this week's lowest prices (€0.30)") {
		t.Fatalf("target = %d (%v): %q", a.TargetSoC, a.StepDiscountPct, a.Summary)
	}
	in.History.Days = 4
	if a := Advise(in)[0]; a.Confidence != 1 {
		t.Fatalf("four days of history = %+v", a)
	}
	// Tomorrow's sun (EVCC's forecast) is a sign of cheaper days ahead.
	in.Days[1].Solar = &Solar{Pct: 8}
	if a := Advise(in)[0]; a.Indication != IndicationMedium {
		t.Fatalf("a sunny tomorrow = %+v", a)
	}
}

func TestTheOutlookIsTheReferenceBeforeTomorrowIsPublished(t *testing.T) {
	// Today averages €0.30 with a lowest of €0.18, so tomorrow's estimate
	// of €0.40 means a lowest of about €0.24: the €0.18 blocks are 25%
	// below it (little step 1: 50%).
	in := input(blocks(append(repeat(12, 0.18), repeat(20, 0.33)...)...), nil)
	in.Days[0].Lowest, in.Days[0].Average = f(0.18), f(0.30)
	in.Days[1].Price = estimate(0.40)
	a := Advise(in)[0]
	r := a.Reference
	if a.Recommendation != ChargeExtra || a.Confidence != 2 || r.Basis != RefOutlook || r.Source != BasisEstimate || r.Value != 0.24 ||
		r.Date != "2026-11-10" || a.CompareDate != "2026-11-10" || a.TargetSoC != 50 || a.StepDiscountPct != 20 {
		t.Fatalf("tomorrow's estimate = %+v (%+v)", a, r)
	}
	if !strings.HasSuffix(a.Summary, "20% or more below Tuesday's expected lowest (€0.24).") ||
		!strings.Contains(strings.Join(a.Rationale, " "),
			"Compared with Tuesday's expected lowest price (€0.240: the outlook's estimate of €0.400 × Monday's lowest-to-average ratio 0.60).") {
		t.Errorf("summary = %q, rationale = %v", a.Summary, a.Rationale)
	}
	// Tomorrow's estimate beats a week of history; a stale outlook is
	// weaker.
	in.History = &History{LowAverage: 0.30, Days: 7}
	if a := Advise(in)[0]; a.Reference.Basis != RefOutlook || a.Confidence != 2 {
		t.Fatalf("estimate and history = %+v", a)
	}
	in.Days[1].Price.Stale = true
	if a := Advise(in)[0]; a.Reference.Basis != RefOutlook || a.Confidence != 1 {
		t.Fatalf("stale estimate = %+v", a)
	}
	// Only the week's average: a week of history is stronger, a shorter
	// one is not.
	in.Days[1].Price = &DayPrice{Value: 0.40, Basis: BasisWeekly}
	if a := Advise(in)[0]; a.Reference.Basis != RefHistory || a.Confidence != 2 {
		t.Fatalf("weekly average and a week of history = %+v", a)
	}
	in.History.Days = 4
	if a := Advise(in)[0]; a.Reference.Basis != RefOutlook || a.Reference.Source != BasisWeekly || a.Confidence != 2 ||
		!strings.Contains(strings.Join(a.Rationale, " "), "the outlook's weekly average of €0.400") {
		t.Fatalf("weekly average and four days of history = %+v", a)
	}
	// Tomorrow is the reference, so the cheaper days ahead start after it.
	in.Days[1].Solar = &Solar{Pct: 8}
	if a := Advise(in)[0]; a.Indication != IndicationLittle {
		t.Fatalf("a sunny reference day = %+v", a)
	}
	// Negative lows keep the expected lowest positive: the ratio stays
	// between 0.3 and 1.
	in.Days[0].Lowest = f(-0.05)
	if a := Advise(in)[0]; a.Reference.Value != 0.12 {
		t.Fatalf("negative lowest = %+v", a.Reference)
	}
	in.Days[0].Lowest, in.Days[0].Average = nil, nil
	in.Days[0].Price.Min = f(0.33)
	if a := Advise(in)[0]; a.Reference.Value != 0.40 {
		t.Fatalf("today's remaining prices, flat = %+v", a.Reference)
	}
}

// A cheap night before dearer days: today's charge waits for it, and
// tomorrow's advice charges extra against the outlook while Thursday's
// prices aren't published and there is no price history yet.
func TestCheapNightBeforeDearerDays(t *testing.T) {
	weekly := &DayPrice{Value: 0.40, Basis: BasisWeekly, Detail: "weekly average"}
	days := week(2, published(0.36), published(0.30), weekly, estimate(0.43), weekly, weekly)
	// Today until the next departure costs €0.36; back home tonight, the
	// lowest is €0.27.
	days[0].Blocks, days[0].StartSoC, days[0].HomeLowest = blocks(repeat(4, 0.36)...), f(37), f(0.27)
	night := append(append(repeat(4, 0.25), repeat(4, 0.23)...), append(repeat(8, 0.22), repeat(8, 0.26)...)...)
	days[1].Blocks, days[1].StartSoC, days[1].TripTarget = blocks(night...), f(24), 23
	days[1].Lowest, days[1].Average = f(0.22), f(0.30)
	in := Input{Days: days, SoCKnown: true, Levels: levels()}
	out := Advise(in)

	if a := out[0]; a.Recommendation != Defer ||
		a.Summary != "Tuesday is cheaper (lowest €0.22 vs €0.27) and your charge covers the trips until then." {
		t.Fatalf("today = %+v", a)
	}
	// Thursday's €0.40 weekly average × 0.22/0.30: an expected lowest of
	// about €0.29. The €0.22–0.23 hours are at least 20% below it.
	a := out[1]
	if a.Recommendation != ChargeExtra || a.Confidence != 2 || a.Reference.Basis != RefOutlook || a.Reference.Value != 0.2933 ||
		a.Indication != IndicationLittle || a.TargetSoC != 42 || a.StepDiscountPct != 20 {
		t.Fatalf("tomorrow = %+v (%+v)", a, a.Reference)
	}
	if a.Summary != "Charge extra to 42%: 01:00–04:00 averages €0.22, 20% or more below Wednesday's expected lowest (€0.29)." {
		t.Errorf("summary = %q", a.Summary)
	}
}

func TestNegativeAndNearZeroPrices(t *testing.T) {
	// Tomorrow's lowest is negative, but today goes lower still: no defer,
	// and blocks at or below €0 count for the top step.
	in := input(blocks(-0.20, -0.20, 0.10, 0.20), f(-0.10))
	a := Advise(in)[0]
	if a.Recommendation != ChargeExtra || a.TargetSoC != 43 || a.StepDiscountPct != 50 || a.Blocks[0].Price != -0.2 {
		t.Fatalf("negative prices = %+v", a)
	}
	// A reference near €0: discounts are measured from €0.01, so €0.004
	// against €0.005 is 10%, not enough.
	in = input(blocks(0.004, 0.30), f(0.005))
	if a := Advise(in)[0]; a.Recommendation == ChargeExtra {
		t.Fatalf("near zero = %+v", a)
	}
}

func TestChargeExtraFromSolar(t *testing.T) {
	sun := func(cost float64, pct float64) Block {
		return Block{Start: sameDay(12, 0), End: sameDay(13, 0), Cost: cost, SlotPrice: cost + 0.11, Solar: true, Pct: pct}
	}
	// The sun alone covers the step: keep the car plugged in, no plan.
	in := input(sortedBlocks(append(blocks(0.30, 0.30), sun(0.05, 20))), f(0.20))
	a := Advise(in)[0]
	if a.Recommendation != ChargeExtra || !a.Solar || a.SolarPct != 20 || *a.SolarGridPrice != 0.16 || *a.SolarPrice != 0.05 ||
		!strings.Contains(a.Summary, "no more than tomorrow's lowest (€0.20). Keep the car plugged in.") {
		t.Fatalf("solar step = %+v", a)
	}
	// Sun at the reference price (no discount) still counts: it is never
	// dearer than charging later.
	in = input(sortedBlocks(append(blocks(0.21, 0.30), sun(0.20, 5))), f(0.20))
	if a := Advise(in)[0]; !a.Solar || a.SolarPct != 5 {
		t.Fatalf("solar at the reference = %+v", a)
	}
	// Dearer than the reference: nothing.
	in = input(sortedBlocks(append(blocks(0.21, 0.30), sun(0.25, 5))), f(0.20))
	if a := Advise(in)[0]; a.Solar || a.Recommendation != None {
		t.Fatalf("solar dearer than the reference = %+v", a)
	}
}

func TestHintsForTripsAndNegativeSolar(t *testing.T) {
	in := input(blocks(repeat(8, 0.30)...), f(0.29))
	in.Days[0].Away = []Away{{Title: "Dentist", Start: sameDay(13, 0), End: sameDay(14, 0), Blocks: []Block{
		{Start: sameDay(13, 0), End: sameDay(13, 15), Cost: -0.03, SlotPrice: -0.03, Pct: 1.5},
		{Start: sameDay(13, 15), End: sameDay(13, 30), Cost: -0.05, SlotPrice: -0.05, Pct: 1.5},
	}}}
	in.Days[0].NegativeSun = []Span{{From: sameDay(13, 0), Until: sameDay(13, 30), Price: -0.05}}
	a := Advise(in)[0]
	if a.AwayHint != "−€0.04/kWh 13:00–13:30 while you're away (Dentist): consider moving the trip to charge then." {
		t.Errorf("away hint = %q", a.AwayHint)
	}
	if a.ExportHint != "Prices are negative 13:00–13:30 (−€0.05/kWh), so exporting solar costs money. The car is away then: use the solar another way, or switch the panels off." {
		t.Errorf("export hint = %q", a.ExportHint)
	}
	// A block as cheap as the top step, but no cheaper than at home: no hint.
	in.Days[0].Away[0].Blocks = []Block{{Start: sameDay(13, 0), End: sameDay(13, 15), Cost: 0.14, SlotPrice: 0.14, Pct: 1.5}}
	in.Days[0].Blocks = blocks(0.14, 0.30)
	if a := Advise(in)[0]; a.AwayHint != "" {
		t.Errorf("away hint for a price the car also gets at home = %q", a.AwayHint)
	}
	in.Days[0].Away[0].Blocks[0].Cost = 0.10
	if a := Advise(in)[0]; !strings.HasPrefix(a.AwayHint, "€0.10/kWh 13:00–13:15 while you're away (Dentist)") {
		t.Errorf("away hint for a clearly cheaper block = %q", a.AwayHint)
	}
	// Unless the car gets it at home later that day (after the deadline).
	in.Days[0].HomeLowest = f(0.10)
	if a := Advise(in)[0]; a.AwayHint != "" {
		t.Errorf("away hint for a price at home later = %q", a.AwayHint)
	}
	in.Days[0].HomeLowest = nil
	in.Days[0].Away[0].Blocks = []Block{
		{Start: sameDay(13, 0), End: sameDay(13, 15), Cost: -0.03, SlotPrice: -0.03, Pct: 1.5},
		{Start: sameDay(13, 15), End: sameDay(13, 30), Cost: -0.05, SlotPrice: -0.05, Pct: 1.5},
	}
	in.Days[0].Blocks = blocks(repeat(8, 0.30)...)
	// Also when tomorrow is cheaper (defer).
	in.Days[1].Lowest = f(0.20)
	if a := Advise(in)[0]; a.Recommendation != Defer || !strings.Contains(a.AwayHint, "(Dentist)") {
		t.Errorf("away hint on a defer day = %+v", a)
	}
	in.Days[0].NegativeSun[0].Home = true
	if a := Advise(in)[0]; !strings.HasSuffix(a.ExportHint, "Keep the car plugged in to use the sun, and switch the panels off if nothing can use it.") {
		t.Errorf("export hint at home = %q", a.ExportHint)
	}
}

func TestUnknownSoCUsesTheStepsTarget(t *testing.T) {
	in := input(blocks(repeat(4, 0.10)...), f(0.20))
	in.Days[0].StartSoC = nil
	a := Advise(in)[0]
	if a.Recommendation != ChargeExtra || a.TargetSoC != 100 || !strings.Contains(strings.Join(a.Rationale, " "), "SoC unknown") {
		t.Fatalf("unknown SoC = %+v", a)
	}
}

func TestLaterDaysShowTheOutlook(t *testing.T) {
	in := input(blocks(0.30), f(0.28))
	days := Advise(in)
	if d := days[2]; !d.NoSuggestion || d.Outlook != "similar" || d.Recommendation != None {
		t.Fatalf("Wednesday = %+v", d)
	}
	in.Days[3].Price = estimate(0.25)
	if d := Advise(in)[3]; d.Outlook != "cheaper" || d.Summary != "Not published yet. The outlook expects about €0.25/kWh, 17% below today." {
		t.Fatalf("Thursday = %+v", d)
	}
	in.Days[4].Price = estimate(0.40)
	if d := Advise(in)[4]; d.Outlook != "dearer" {
		t.Fatalf("Friday = %+v", d)
	}
	in.Days[5].Price, in.Days[5].SunshinePct = nil, sunshine(70)
	if d := Advise(in)[5]; d.Outlook != "" || !d.NoSuggestion {
		t.Fatalf("Saturday without an estimate = %+v", d)
	}
}

func TestLittleTimeAtHome(t *testing.T) {
	in := input(blocks(0.10), f(0.20))
	in.Days[0].HomeHours = 0.5
	if a := Advise(in)[0]; a.Recommendation != None || a.Summary != "Little time at home to charge on this day." {
		t.Fatalf("little time at home = %+v", a)
	}
}
