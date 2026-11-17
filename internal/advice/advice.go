// Package advice produces one charging recommendation per day of the plan
// window. It is pure: no I/O and no clock access.
//
// Charging has three layers: the next departure's target (always), later
// trips charged ahead (the trip plans), and charge extra on top: this
// advice, to build a reserve or use cheap prices. Only days with published
// prices get a recommendation, as only they have time blocks to plan with;
// later days show what the outlook expects.
//
// For a published day:
//
//   - defer: the next day is published and its lowest price is at least
//     Threshold below the day's lowest price at home, and the projected SoC
//     covers the departures until then. Charge extra waits, and the plan
//     charges only the trips (and the sun, when that is cheaper still);
//   - charge extra: the day's blocks at home are cheaper than the reference
//     price (the next day's lowest published price, else the past week's
//     average lowest price) by the discount steps of the level matching the
//     indication of cheaper days ahead (the weekly outlook, wind, and sun);
//     each step charges to its target, as far as its blocks can;
//   - charge extra from solar: the sun at home costs no more than the
//     reference (its slot price minus the tax not paid on own solar): keep
//     the car plugged in, as EVCC charges from it by itself;
//   - none: otherwise.
//
// Confidence (1–3) follows the reference: 3 against published prices, 2
// against a week of history, 1 against less.
package advice

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// Recommendations.
const (
	None        = "none"
	ChargeExtra = "charge_extra"
	Defer       = "defer"
)

// Price bases, strongest first.
const (
	BasisPublished = "published"
	BasisEstimate  = "estimate"
	BasisWeekly    = "weekly"
)

// Reference bases: the next day's lowest published price, or the past
// days' average lowest price.
const (
	RefPublished = "published"
	RefHistory   = "history"
)

// Indications of cheaper days ahead.
const (
	IndicationLittle = "little"
	IndicationMedium = "medium"
	IndicationHigh   = "high"
)

// Threshold is the relative price difference that makes a day cheaper or
// dearer than another.
const Threshold = 0.08

// minHomeHours is the time at home that counts as a charging opportunity.
const minHomeHours = 1.0

// minSolarPct is the solar charge (SoC points) worth advising on.
const minSolarPct = 1.0

// minWeakSunKWh is the local solar while the car is home (kWh) worth
// explaining when none of it can charge the car.
const minWeakSunKWh = 3.0

// minHistoryDays is how many past days the history reference needs.
const minHistoryDays = 3

// minReference keeps discounts finite for references near zero (EUR/kWh).
const minReference = 0.01

// DayPrice is a date's expected price level.
type DayPrice struct {
	Value  float64 `json:"value"` // EUR/kWh incl. VAT, daily average
	Basis  string  `json:"basis"`
	Detail string  `json:"detail"`
	Stale  bool    `json:"stale"`
	// Published days: the lowest slot (for today, of the remaining ones).
	Min   *float64   `json:"min,omitempty"`
	MinAt *time.Time `json:"minAt,omitempty"`
}

// Block is charging capacity in part of a published slot: the SoC points
// it can add and what a kWh from the wall costs (solar at its slot price
// minus the tax not paid on own solar).
type Block struct {
	Start     time.Time
	End       time.Time
	Cost      float64
	SlotPrice float64
	Solar     bool
	Pct       float64
}

// Away is a trip on a day, with the charging capacity the car misses.
type Away struct {
	Title  string
	Start  time.Time
	End    time.Time
	Blocks []Block
}

// Span is a time range of a day, e.g. where prices are negative while the
// sun shines, or cheap blocks charge.
type Span struct {
	From  time.Time `json:"from"`
	Until time.Time `json:"until"`
	Price float64   `json:"price"`         // EUR/kWh (the average where merged)
	Pct   float64   `json:"pct,omitempty"` // SoC points it charges
	Home  bool      `json:"-"`
}

// Day is one date's inputs.
type Day struct {
	Date         string
	Label        string // e.g. "Tue"
	Name         string // e.g. "Tuesday", for summaries
	Price        *DayPrice
	HomeHours    float64 // time the car is home (from now, for today)
	WindBft      *int    // KNMI, national land wind
	Level        string  // outlook's stated rank: cheapest, most_expensive, ...
	MarketWind   string  // outlook: low, normal, high, unknown
	MarketWindGW *float64
	// Local PV from EVCC's solar forecast (kWh, before household use): the
	// whole (remaining) day, and while the car is home. Nil beyond the
	// forecast (EVCC covers today and tomorrow).
	SolarKWh     *float64
	SolarHomeKWh *float64
	// KNMI's national sunshine (% of possible), used after EVCC's forecast.
	SunshinePct *int
	// Solar is what the forecast solar surplus can charge while the car is
	// home, and at what price; nil beyond EVCC's forecast.
	Solar *Solar

	// Published days only. Blocks is the charging capacity at home from
	// now (for today) until the deadline, cheapest first; Away the trips.
	Published bool
	Blocks    []Block
	Away      []Away
	// Lowest is the day's lowest published price, when all of the day is
	// published (the reference for the day before).
	Lowest *float64
	// StartSoC is the projected SoC when the day's charging can start (nil:
	// unknown); TripTarget the next departure's target when the window ends
	// at it (with later trips charged ahead), else 0.
	StartSoC   *float64
	TripTarget float64
	// NegativeSun lists the times prices are negative while the sun makes
	// more than the household uses.
	NegativeSun []Span
	// HomeLowest is the lowest grid price while the car is home on the day
	// (all of it, not only until the deadline); nil: use Blocks.
	HomeLowest *float64
}

// Solar is a day's usable solar surplus while the car is home: SoC points,
// EUR/kWh (the slot prices minus the tax not paid on own solar), the slots'
// grid price itself, and the first and last slot it charges.
type Solar struct {
	Pct       float64
	Price     float64
	GridPrice float64
	From      time.Time
	Until     time.Time
}

// Departure is an upcoming away period from the minimum-charge plan.
type Departure struct {
	Date      string // local date of departure
	DepartAt  time.Time
	ChargePct float64 // SoC points the minimum plan adds before it
	EnergyPct float64 // SoC points the trip uses
}

// Step charges extra up to TargetSoC with blocks at least MinDiscountPct
// cheaper than the reference.
type Step struct {
	MinDiscountPct float64
	TargetSoC      float64
}

// Level is charge extra at one indication of cheaper days ahead.
type Level struct {
	Enabled bool
	Steps   []Step
}

// History is the past days' average lowest price.
type History struct {
	LowAverage float64
	Days       int
}

// Input is everything the advice needs.
type Input struct {
	Days       []Day // the window, today first
	Departures []Departure
	SoCKnown   bool
	// SolarMinKW is the solar surplus (kW, after household use) the charger
	// needs to charge at all.
	SolarMinKW float64
	// History is the past week's average lowest price, when known.
	History *History
	// Levels holds charge extra per indication (little, medium, high).
	Levels map[string]Level
}

// Reference is the price a day's blocks are compared with.
type Reference struct {
	Value  float64 `json:"value"` // EUR/kWh incl. VAT
	Basis  string  `json:"basis"` // published or history
	Days   int     `json:"days,omitempty"`
	Detail string  `json:"detail"`
}

// Advice is one date's recommendation.
type Advice struct {
	Date           string   `json:"date"`
	Recommendation string   `json:"recommendation"`
	NoSuggestion   bool     `json:"noSuggestion"` // weak or missing evidence
	Reason         string   `json:"reason,omitempty"`
	Confidence     int      `json:"confidence"` // 1 low, 2 medium, 3 high; 0 without a suggestion
	Summary        string   `json:"summary"`
	Rationale      []string `json:"rationale"`
	CompareDate    string   `json:"compareDate,omitempty"` // the next day, when it is the reference
	DeferUntil     string   `json:"deferUntil,omitempty"`  // defer: the cheaper day
	LaterChargePct float64  `json:"laterChargePct"`        // charge the plan adds after this day
	// Indication is how strongly the coming days look cheaper (little,
	// medium, high); it picks the charge-extra steps.
	Indication string     `json:"indication,omitempty"`
	Reference  *Reference `json:"reference,omitempty"`
	// Charge extra: the SoC to charge to, the discount of the step that
	// sets it, the SoC points above what the trips need, and the blocks it
	// charges in.
	TargetSoC       int     `json:"targetSoc,omitempty"`
	StepDiscountPct float64 `json:"stepDiscountPct,omitempty"`
	ExtraPct        float64 `json:"extraPct,omitempty"`
	Blocks          []Span  `json:"blocks,omitempty"`
	// AwayHint suggests moving a trip that misses very cheap blocks;
	// ExportHint says what to do with solar while prices are negative.
	AwayHint   string `json:"awayHint,omitempty"`
	ExportHint string `json:"exportHint,omitempty"`
	// Outlook is how a day without published prices compares with today:
	// cheaper, dearer, or similar (by the outlook's estimate).
	Outlook string `json:"outlook,omitempty"`
	// Solar marks charge extra from solar: keep the car plugged in while the
	// sun shines (SolarFrom to SolarUntil), for up to SolarPct. It counts at
	// SolarPrice (the grid price minus the tax not paid on own solar), while
	// the grid charges SolarGridPrice in those slots.
	Solar          bool       `json:"solar,omitempty"`
	SolarPct       float64    `json:"solarPct,omitempty"`
	SolarPrice     *float64   `json:"solarPrice,omitempty"`
	SolarGridPrice *float64   `json:"solarGridPrice,omitempty"`
	SolarFrom      *time.Time `json:"solarFrom,omitempty"`
	SolarUntil     *time.Time `json:"solarUntil,omitempty"`
}

// Advise returns advice for every day of the input window.
func Advise(in Input) []Advice {
	out := make([]Advice, 0, len(in.Days))
	for i := range in.Days {
		out = append(out, adviseDay(in, i))
	}
	return out
}

func adviseDay(in Input, i int) Advice {
	d := in.Days[i]
	a := Advice{Date: d.Date, Recommendation: None, Rationale: []string{}}
	if d.Price != nil {
		a.Rationale = append(a.Rationale, priceLine(d))
	}
	a.Rationale = append(a.Rationale, weatherLines(d)...)
	if line := weakSun(in, d); line != "" {
		a.Rationale = append(a.Rationale, line)
	}
	a.LaterChargePct = round1(laterNeed(in, d.Date))
	if !d.Published {
		return outlookDay(in, i, a)
	}
	a.ExportHint = exportHint(d)
	if d.HomeHours < minHomeHours || len(d.Blocks) == 0 {
		a.Summary = "Little time at home to charge on this day."
		return a
	}

	var next *Day
	if i+1 < len(in.Days) && in.Days[i+1].Published && in.Days[i+1].Lowest != nil {
		next = &in.Days[i+1]
	}
	if next != nil {
		if out, ok := deferDay(in, i, a, next); ok {
			return out
		}
	}

	ref := reference(in, next)
	if ref == nil {
		days := 0
		if in.History != nil {
			days = in.History.Days
		}
		return noSuggestion(a, fmt.Sprintf("Nothing to compare with yet: tomorrow's prices aren't published, and the price history has %d of %d days.",
			days, minHistoryDays))
	}
	a.Reference = ref
	if ref.Basis == RefPublished {
		a.CompareDate = next.Date
	}
	a.Confidence = refConfidence(ref)
	a.Rationale = append(a.Rationale, "Compared with "+ref.Detail+".")
	ind, lines := indication(in, i, next != nil)
	a.Indication = ind
	a.Rationale = append(a.Rationale, lines...)
	level := in.Levels[ind]

	solar := solarCharge(d.Blocks, ref.Value)
	if level.Enabled {
		if s, ok := chargeExtra(d, level, ref.Value); ok {
			if s.solarOnly {
				return solarExtra(i, a, d, solar, ref)
			}
			a.Recommendation = ChargeExtra
			a.TargetSoC, a.StepDiscountPct, a.ExtraPct, a.Blocks = s.target, s.discount, round1(s.extra), s.spans
			a.Rationale = append(a.Rationale, blocksLine(s, ref))
			if d.StartSoC == nil {
				a.Rationale = append(a.Rationale, "SoC unknown: the target is the step's, not checked against what the blocks can charge.")
			}
			a.Summary = fmt.Sprintf("Charge extra to %d%%: %s, %.0f%% or more below %s.", a.TargetSoC,
				spansSummary(s.spans), s.discount, refShort(ref))
			a.AwayHint = awayHint(d, level, ref.Value)
			return a
		}
	}
	if solar.Pct >= minSolarPct {
		return solarExtra(i, a, d, solar, ref)
	}
	a.AwayHint = awayHint(d, in.Levels[IndicationLittle], ref.Value)
	switch {
	case !level.Enabled:
		a.Summary = fmt.Sprintf("The coming days look cheaper (%s indication), so no extra charge.", ind)
	default:
		a.Summary = fmt.Sprintf("No blocks at home are cheap enough to charge extra (%.0f%% below %s).",
			level.Steps[0].MinDiscountPct, refShort(ref))
	}
	return a
}

// outlookDay describes a day without published prices: what the outlook
// expects, compared with today. Only published days get a recommendation.
func outlookDay(in Input, i int, a Advice) Advice {
	d := in.Days[i]
	a = noSuggestion(a, "Prices for this day aren't published yet (around 14:00 the day before).")
	today := in.Days[0]
	if d.Price == nil || d.Price.Basis != BasisEstimate || today.Price == nil || today.Price.Basis != BasisPublished {
		return a
	}
	diff := (d.Price.Value - today.Price.Value) / math.Max(math.Abs(today.Price.Value), minReference)
	switch {
	case diff <= -Threshold:
		a.Outlook = "cheaper"
		a.Summary = fmt.Sprintf("Not published yet. The outlook expects about €%.2f/kWh, %.0f%% below today.", d.Price.Value, -diff*100)
	case diff >= Threshold:
		a.Outlook = "dearer"
		a.Summary = fmt.Sprintf("Not published yet. The outlook expects about €%.2f/kWh, %.0f%% above today.", d.Price.Value, diff*100)
	default:
		a.Outlook = "similar"
		a.Summary = fmt.Sprintf("Not published yet. The outlook expects about €%.2f/kWh, like today.", d.Price.Value)
	}
	return a
}

// deferDay advises waiting for the next day when its lowest price is
// clearly below this day's lowest at home. It also applies when trips need
// charge before then (keep the plan): charge extra waits either way.
func deferDay(in Input, i int, a Advice, next *Day) (Advice, bool) {
	d := in.Days[i]
	low := lowestSlot(d.Blocks)
	if *next.Lowest > low-Threshold*math.Abs(low) {
		return a, false
	}
	a.CompareDate = next.Date
	a.Confidence = 3
	a.Reference = &Reference{Value: *next.Lowest, Basis: RefPublished, Detail: name(*next) + "'s lowest published price"}
	a.Rationale = append(a.Rationale, fmt.Sprintf("%s's lowest published price (€%.3f) is %.0f%% below %s's lowest while the car is home (€%.3f).",
		name(*next), *next.Lowest, (low-*next.Lowest)/math.Max(math.Abs(low), minReference)*100, dayWord(i, d), low))
	switch need := needBetween(in, d.Date, next.Date); {
	case !in.SoCKnown:
		a.Summary = fmt.Sprintf("%s is cheaper, but the SoC is unknown: keep the plan.", name(*next))
		return a, true
	case need >= 1:
		a.Summary = fmt.Sprintf("Keep the plan: %s is cheaper, but trips before then need +%.0f%%.", name(*next), need)
		return a, true
	}
	a.AwayHint = awayHint(d, in.Levels[IndicationLittle], *next.Lowest)
	a.Recommendation = Defer
	a.DeferUntil = next.Date
	a.Rationale = append(a.Rationale, "The current charge covers every departure until then.")
	a.Summary = fmt.Sprintf("%s is cheaper (lowest %s vs %s) and your charge covers the trips until then.",
		name(*next), money(*next.Lowest), money(low))
	if s := solarCharge(d.Blocks, math.Inf(1)); s.Pct >= minSolarPct {
		if s.Price <= *next.Lowest {
			a.Summary += fmt.Sprintf(" The sun still charges cheaper: keep the car plugged in from %s to %s.", clock(s.From), clock(s.Until))
			a.Rationale = append(a.Rationale, "Postponing pauses only cheap-grid charging: EVCC still charges from the sun.")
		} else {
			a.Summary += " No need to keep it plugged in for the sun either."
			a.Rationale = append(a.Rationale, fmt.Sprintf("Even the sun (€%.2f without the tax not paid on own solar) costs more than %s's lowest price.",
				s.Price, name(*next)))
		}
	}
	return a, true
}

// reference is the price a day's blocks are compared with: the next day's
// lowest published price, else the past week's average lowest price.
func reference(in Input, next *Day) *Reference {
	if next != nil {
		return &Reference{Value: *next.Lowest, Basis: RefPublished, Detail: fmt.Sprintf("%s's lowest published price (€%.3f)", name(*next), *next.Lowest)}
	}
	if h := in.History; h != nil && h.Days >= minHistoryDays {
		return &Reference{Value: h.LowAverage, Basis: RefHistory, Days: h.Days,
			Detail: fmt.Sprintf("the average lowest price of the past %d days (€%.3f)", h.Days, h.LowAverage)}
	}
	return nil
}

func refConfidence(r *Reference) int {
	switch {
	case r.Basis == RefPublished:
		return 3
	case r.Days >= 7:
		return 2
	}
	return 1
}

// indication is how strongly the days after the reference look cheaper,
// from the weekly outlook and the wind and sun forecasts. A day signals
// cheaper prices with an estimate at least Threshold below this day's
// published average (or ranked the week's cheapest), or with wind (Bft 5,
// or high market wind) or sun (60% of possible sunshine, or 5% of SoC from
// the local forecast) unless its estimate is dearer. Both together make a
// high indication, one a medium, none a little.
func indication(in Input, i int, nextPublished bool) (string, []string) {
	d := in.Days[i]
	from := i + 1
	if nextPublished {
		from = i + 2
	}
	level := 0
	var lines []string
	for _, l := range in.Days[min(from, len(in.Days)):] {
		if l.HomeHours < minHomeHours {
			continue
		}
		cheaper, dearer := l.Level == "cheapest", false
		if l.Price != nil && l.Price.Basis == BasisEstimate && d.Price != nil {
			margin := Threshold * math.Abs(d.Price.Value)
			cheaper = cheaper || l.Price.Value <= d.Price.Value-margin
			dearer = l.Price.Value >= d.Price.Value+margin
		}
		var why []string
		if cheaper && l.Price != nil {
			why = append(why, fmt.Sprintf("estimate about €%.2f", l.Price.Value))
		}
		windy := (l.WindBft != nil && *l.WindBft >= 5) || l.MarketWind == "high"
		sunny := (l.SunshinePct != nil && *l.SunshinePct >= 60) || (l.Solar != nil && l.Solar.Pct >= 5)
		if windy && !dearer {
			why = append(why, "windy")
		}
		if sunny && !dearer {
			why = append(why, "sunny")
		}
		lv := 0
		switch {
		case cheaper && (windy || sunny):
			lv = 2
		case cheaper || ((windy || sunny) && !dearer):
			lv = 1
		}
		if lv > 0 {
			lines = append(lines, fmt.Sprintf("%s looks cheaper: %s.", name(l), strings.Join(why, ", ")))
		}
		level = max(level, lv)
	}
	ind := []string{IndicationLittle, IndicationMedium, IndicationHigh}[level]
	if level == 0 {
		lines = append(lines, "No sign of cheaper days ahead in the outlook, wind, or sun.")
	}
	return ind, lines
}

// step is a charge-extra decision: the target it reaches, its step's
// discount, the SoC above what the trips need, the blocks it uses, and
// whether they are all solar.
type step struct {
	target    int
	discount  float64
	extra     float64
	spans     []Span
	solarOnly bool
}

// chargeExtra picks the step that reaches the highest target: each charges
// up to its target with the blocks at least its discount below the
// reference (any block at or below €0 counts for every step), as far as
// those blocks can. It applies when that target is at least a point above
// both the start and the trips' target.
func chargeExtra(d Day, level Level, ref float64) (step, bool) {
	var best step
	bestReach := -1.0
	for _, st := range level.Steps {
		var qual []Block
		total := 0.0
		for _, b := range d.Blocks {
			if qualifies(b.Cost, ref, st.MinDiscountPct) {
				qual = append(qual, b)
				total += b.Pct
			}
		}
		if total < minSolarPct {
			continue
		}
		reach := st.TargetSoC
		if d.StartSoC != nil {
			reach = math.Min(st.TargetSoC, *d.StartSoC+total)
		}
		// On a tie the deeper discount says more.
		if reach < bestReach-1e-9 {
			continue
		}
		bestReach = reach
		best = step{target: int(math.Floor(reach + 1e-9)), discount: st.MinDiscountPct}
		best.spans, best.solarOnly = use(qual, need(d, reach))
	}
	if bestReach < 0 {
		return best, false
	}
	floor := d.TripTarget
	if d.StartSoC != nil {
		floor = math.Max(floor, *d.StartSoC)
	}
	best.extra = float64(best.target) - floor
	return best, best.extra >= 1
}

// need is the charge to reach a target from the day's start (the whole
// target with an unknown start).
func need(d Day, target float64) float64 {
	if d.StartSoC == nil {
		return target
	}
	return math.Max(0, target-*d.StartSoC)
}

// qualifies reports whether a block costing cost is at least discount
// percent below the reference; at or below €0 always.
func qualifies(cost, ref, discount float64) bool {
	return cost <= 0 || (ref-cost)/math.Max(math.Abs(ref), minReference)*100 >= discount-1e-9
}

// use takes the cheapest blocks for points SoC points and returns them as
// merged time ranges, and whether they are all solar.
func use(blocks []Block, points float64) ([]Span, bool) {
	var used []Block
	solarOnly := true
	for _, b := range blocks {
		if points <= 1e-9 {
			break
		}
		n := math.Min(b.Pct, points)
		points -= n
		b.Pct = n
		used = append(used, b)
		solarOnly = solarOnly && b.Solar
	}
	return merge(used), solarOnly && len(used) > 0
}

// merge joins blocks into time ranges where they touch, with the
// energy-weighted average price and the SoC points they charge.
func merge(blocks []Block) []Span {
	sorted := append([]Block(nil), blocks...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Start.Before(sorted[j].Start) })
	var out []Span
	var weight []float64
	for _, b := range sorted {
		if n := len(out); n > 0 && !b.Start.After(out[n-1].Until) {
			s := &out[n-1]
			if b.End.After(s.Until) {
				s.Until = b.End
			}
			s.Price = (s.Price*weight[n-1] + b.Cost*b.Pct) / (weight[n-1] + b.Pct)
			weight[n-1] += b.Pct
			s.Pct += b.Pct
			continue
		}
		out = append(out, Span{From: b.Start, Until: b.End, Price: b.Cost, Pct: b.Pct})
		weight = append(weight, b.Pct)
	}
	for i := range out {
		out[i].Price, out[i].Pct = math.Round(out[i].Price*1000)/1000, round1(out[i].Pct)
	}
	return out
}

// solarCharge is what the sun at home can charge at no more than limit
// EUR/kWh (its slot price minus the tax not paid on it).
func solarCharge(blocks []Block, limit float64) Solar {
	var s Solar
	cost, grid := 0.0, 0.0
	for _, b := range blocks {
		if !b.Solar || b.Cost > limit {
			continue
		}
		s.Pct += b.Pct
		cost += b.Cost * b.Pct
		grid += b.SlotPrice * b.Pct
		if s.From.IsZero() || b.Start.Before(s.From) {
			s.From = b.Start
		}
		if b.End.After(s.Until) {
			s.Until = b.End
		}
	}
	if s.Pct > 0 {
		s.Price, s.GridPrice = cost/s.Pct, grid/s.Pct
	}
	return s
}

// solarExtra advises charging extra from the day's sun: EVCC charges from
// it by itself in Smart mode, so the car only needs to be plugged in while
// the sun shines.
func solarExtra(i int, a Advice, d Day, s Solar, ref *Reference) Advice {
	a.Recommendation = ChargeExtra
	a.Solar = true
	a.SolarPct, a.SolarPrice, a.SolarGridPrice = round1(s.Pct), ptrRound3(s.Price), ptrRound3(s.GridPrice)
	a.SolarFrom, a.SolarUntil = &s.From, &s.Until
	a.Rationale = append(a.Rationale,
		fmt.Sprintf("%s: local solar while the car is home can add about %.0f%% SoC at about €%.2f/kWh, or €%.2f without the tax not paid on own solar.",
			name(d), s.Pct, s.GridPrice, s.Price),
		"EVCC charges from the sun by itself in Smart mode: keep the car plugged in, no plan needed.")
	sun := d.Name + "'s sun"
	switch i {
	case 0:
		sun = "Today's sun"
	case 1:
		sun = "Tomorrow's sun"
	}
	a.Summary = fmt.Sprintf("%s charges at about €%.2f/kWh, or €%.2f without the tax you don't pay on own solar: no more than %s. Keep the car plugged in.",
		sun, s.GridPrice, s.Price, refShort(ref))
	return a
}

// awayHint suggests moving a trip that misses blocks at or below €0, or
// blocks as cheap as the level's top step and clearly cheaper than any at
// home.
func awayHint(d Day, level Level, ref float64) string {
	top := 100.0
	if len(level.Steps) > 0 {
		top = level.Steps[len(level.Steps)-1].MinDiscountPct
	}
	home := math.Inf(1)
	for _, b := range d.Blocks {
		if !b.Solar {
			home = math.Min(home, b.Cost)
		}
	}
	if d.HomeLowest != nil {
		home = math.Min(home, *d.HomeLowest)
	}
	for _, t := range d.Away {
		var cheap []Block
		for _, b := range t.Blocks {
			if !b.Solar && (b.Cost <= 0 || (qualifies(b.Cost, ref, top) && b.Cost <= home-Threshold*math.Abs(home))) {
				cheap = append(cheap, b)
			}
		}
		if len(cheap) == 0 {
			continue
		}
		spans := merge(cheap)
		s := spans[0]
		for _, o := range spans[1:] {
			if o.Price < s.Price {
				s = o
			}
		}
		return fmt.Sprintf("%s/kWh %s–%s while you're away (%s): consider moving the trip to charge then.",
			money(s.Price), clock(s.From), clock(s.Until), t.Title)
	}
	return ""
}

// exportHint says what to do with solar while prices are negative:
// exporting it costs money, so use it or switch the panels off.
func exportHint(d Day) string {
	if len(d.NegativeSun) == 0 {
		return ""
	}
	s := d.NegativeSun[0]
	when := fmt.Sprintf("Prices are negative %s–%s (%s/kWh), so exporting solar costs money.", clock(s.From), clock(s.Until), money(s.Price))
	if s.Home {
		return when + " Keep the car plugged in to use the sun, and switch the panels off if nothing can use it."
	}
	return when + " The car is away then: use the solar another way, or switch the panels off."
}

// lowestSlot is the lowest grid price among blocks.
func lowestSlot(blocks []Block) float64 {
	low := math.Inf(1)
	for _, b := range blocks {
		low = math.Min(low, b.SlotPrice)
	}
	return low
}

func blocksLine(s step, ref *Reference) string {
	return fmt.Sprintf("Blocks at home at least %.0f%% below %s: %s, about +%.0f%% SoC.",
		s.discount, refShort(ref), spansText(s.spans), sumPct(s.spans))
}

// spansText is "02:00–03:30 (€0.11), 04:15–05:00 (€0.12)", the first three.
func spansText(spans []Span) string {
	parts := make([]string, 0, len(spans))
	for _, s := range spans {
		parts = append(parts, fmt.Sprintf("%s–%s (%s)", clock(s.From), clock(s.Until), money(s.Price)))
	}
	if len(parts) > 3 {
		parts = append(parts[:3], fmt.Sprintf("%d more", len(spans)-3))
	}
	return strings.Join(parts, ", ")
}

// spansSummary is "00:00–06:00 averages €0.17", or for several pieces
// "about 4 h between 00:15 and 05:00 average €0.17".
func spansSummary(spans []Span) string {
	if len(spans) == 1 {
		s := spans[0]
		return fmt.Sprintf("%s–%s averages %s", clock(s.From), clock(s.Until), money(s.Price))
	}
	var hours, price, pct float64
	for _, s := range spans {
		hours += s.Until.Sub(s.From).Hours()
		price += s.Price * s.Pct
		pct += s.Pct
	}
	if pct > 0 {
		price /= pct
	}
	return fmt.Sprintf("about %s between %s and %s average %s", hoursText(hours), clock(spans[0].From),
		clock(spans[len(spans)-1].Until), money(price))
}

// hoursText is "45 min" or "4 h" (to the half hour).
func hoursText(h float64) string {
	if h < 1 {
		return fmt.Sprintf("%.0f min", h*60)
	}
	return strings.TrimSuffix(fmt.Sprintf("%.1f", math.Round(h*2)/2), ".0") + " h"
}

func sumPct(spans []Span) float64 {
	sum := 0.0
	for _, s := range spans {
		sum += s.Pct
	}
	return sum
}

// refShort is the reference in a few words.
func refShort(r *Reference) string {
	if r.Basis == RefPublished {
		return fmt.Sprintf("tomorrow's lowest (%s)", money(r.Value))
	}
	return fmt.Sprintf("this week's lowest prices (%s)", money(r.Value))
}

// money is "€0.12", or "−€0.04" below zero.
func money(v float64) string {
	if v < -0.005 {
		return fmt.Sprintf("−€%.2f", -v)
	}
	return fmt.Sprintf("€%.2f", math.Abs(v))
}

func clock(t time.Time) string { return t.Format("15:04") }

// dayWord is "today", "tomorrow", or the day's name.
func dayWord(i int, d Day) string {
	switch i {
	case 0:
		return "today"
	case 1:
		return "tomorrow"
	}
	return name(d)
}

// weakSun says when a day has sun while the car is home but none of it
// reaches the charger's minimum above household use, so it cannot charge.
func weakSun(in Input, d Day) string {
	if d.Price == nil || d.SolarHomeKWh == nil || *d.SolarHomeKWh < minWeakSunKWh || d.Solar != nil || in.SolarMinKW <= 0 {
		return ""
	}
	return fmt.Sprintf("%s: the sun stays below what the charger needs (about %.1f kW above household use), so it can't charge the car.",
		d.Name, in.SolarMinKW)
}

// name is a day's full weekday name for summaries.
func name(d Day) string {
	if d.Name != "" {
		return d.Name
	}
	return d.Label
}

func ptrRound3(v float64) *float64 {
	r := math.Round(v*1000) / 1000
	return &r
}

func noSuggestion(a Advice, reason string) Advice {
	a.NoSuggestion = true
	a.Reason = reason
	a.Confidence = 0
	a.Summary = reason
	return a
}

// laterNeed is the charge the minimum plan adds for departures after the
// day; with an unknown SoC it falls back to those trips' energy.
func laterNeed(in Input, date string) float64 {
	sum := 0.0
	for _, dep := range in.Departures {
		if dep.Date > date {
			if in.SoCKnown {
				sum += dep.ChargePct
			} else {
				sum += dep.EnergyPct
			}
		}
	}
	return sum
}

// needBetween is the charge the minimum plan adds for departures from the
// day `from` until (not including) the day `until`.
func needBetween(in Input, from, until string) float64 {
	sum := 0.0
	for _, dep := range in.Departures {
		if dep.Date >= from && dep.Date < until {
			sum += dep.ChargePct
		}
	}
	return sum
}

func priceLine(d Day) string {
	// The full day name: "Sun: €0.30" would read like the solar line.
	return fmt.Sprintf("%s: €%.3f/kWh average incl. VAT (%s).", name(d), d.Price.Value, d.Price.Detail)
}

func weatherLines(d Day) []string {
	var out []string
	wind := ""
	if d.WindBft != nil {
		wind = fmt.Sprintf("KNMI Bft %d", *d.WindBft)
	}
	if d.MarketWind != "" && d.MarketWind != "unknown" {
		if wind != "" {
			wind += "; "
		}
		wind += "market wind " + d.MarketWind
		if d.MarketWindGW != nil {
			wind += fmt.Sprintf(" (%.1f GW)", *d.MarketWindGW)
		}
	}
	if wind != "" {
		out = append(out, "Wind: "+wind+".")
	}
	switch {
	case d.SolarKWh != nil:
		line := fmt.Sprintf("Sun: %.1f kWh local solar forecast (EVCC)", *d.SolarKWh)
		if d.SolarHomeKWh != nil {
			line += fmt.Sprintf(", %.1f kWh while the car is home", *d.SolarHomeKWh)
		}
		out = append(out, line+".")
	case d.SunshinePct != nil:
		out = append(out, fmt.Sprintf("Sun: %d%% of possible sunshine (KNMI national outlook; EVCC's solar forecast does not reach this day).",
			*d.SunshinePct))
	}
	return out
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }
