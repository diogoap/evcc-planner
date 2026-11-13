// Package advice produces one qualitative charging recommendation per day of
// the plan window. It is pure: no I/O and no clock access.
//
// Each day's price level is its daily average in EUR/kWh incl. VAT, from the
// strongest available basis: published day-ahead prices, then a dated
// estimate from the weekly outlook, then only the outlook's weekly average.
// A day is compared with the later days the car can charge on:
//
//   - charge ahead from solar: the day's solar surplus while the car is home
//     (valued at its slot price minus the tax not paid on own solar) is
//     cheaper than every later day by at least Threshold and later trips will
//     need charging; EVCC charges from it by itself, so the car only needs to
//     be plugged in (no plan);
//   - charge ahead: the day is cheaper than every later day by at least
//     Threshold and later trips will need charging;
//   - defer: a later day is cheaper by at least Threshold and the projected
//     SoC covers every departure until then;
//   - none: otherwise (keep the plan).
//
// Days without a day-specific price, or with nothing to compare with, get no
// suggestion. Confidence (1–3) follows the weaker of the two compared bases,
// lowered for stale sources or contradicting weather. Indicators (little,
// medium, high) are qualitative and never prescribe an amount.
package advice

import (
	"fmt"
	"math"
	"time"
)

// Recommendations.
const (
	None        = "none"
	ChargeAhead = "charge_ahead"
	Defer       = "defer"
)

// Price bases, strongest first.
const (
	BasisPublished = "published"
	BasisEstimate  = "estimate"
	BasisWeekly    = "weekly"
)

// Threshold is the relative price difference that makes a recommendation.
const Threshold = 0.08

// minHomeHours is the time at home that counts as a charging opportunity.
const minHomeHours = 1.0

// minSolarPct is the solar charge (SoC points) worth advising on.
const minSolarPct = 1.0

// minWeakSunKWh is the local solar while the car is home (kWh) worth
// explaining when none of it can charge the car.
const minWeakSunKWh = 3.0

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

// Input is everything the advice needs.
type Input struct {
	Days       []Day // the window, today first
	Departures []Departure
	SoCKnown   bool
	// SolarMinKW is the solar surplus (kW, after household use) the charger
	// needs to charge at all.
	SolarMinKW float64
}

// Advice is one date's recommendation.
type Advice struct {
	Date           string   `json:"date"`
	Recommendation string   `json:"recommendation"`
	NoSuggestion   bool     `json:"noSuggestion"` // weak or missing evidence
	Reason         string   `json:"reason,omitempty"`
	Confidence     int      `json:"confidence"`          // 1 low, 2 medium, 3 high; 0 without a suggestion
	Indicator      string   `json:"indicator,omitempty"` // little, medium, high (charge ahead)
	Summary        string   `json:"summary"`
	Rationale      []string `json:"rationale"`
	CompareDate    string   `json:"compareDate,omitempty"` // the later day compared with
	DeferUntil     string   `json:"deferUntil,omitempty"`  // defer: the cheaper day
	LaterChargePct float64  `json:"laterChargePct"`        // charge the plan adds after this day
	// Solar marks charge ahead from solar: keep the car plugged in while the
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
	switch {
	case d.Price == nil:
		return noSuggestion(a, "No published prices or outlook estimate for this day.")
	case d.Price.Basis == BasisWeekly:
		return noSuggestion(a, "Only the weekly average covers this day, which is not enough to compare days.")
	}

	var later []Day
	for _, l := range in.Days[i+1:] {
		if l.Price != nil && l.HomeHours >= minHomeHours {
			later = append(later, l)
		}
	}
	if len(later) == 0 {
		return noSuggestion(a, "No later day in the window has prices to compare with.")
	}

	a.LaterChargePct = round1(laterNeed(in, d.Date))
	if d.HomeHours < minHomeHours {
		a.Confidence = basisScore(d.Price)
		a.Summary = "Little time at home to charge on this day."
		return a
	}

	cheapest := later[0]
	for _, l := range later[1:] {
		if l.Price.Value < cheapest.Price.Value {
			cheapest = l
		}
	}
	solar := d.Solar != nil && d.Solar.Pct >= minSolarPct
	if solar && a.LaterChargePct >= 1 {
		if sdiff := (cheapest.Price.Value - d.Solar.Price) / math.Abs(cheapest.Price.Value); sdiff >= Threshold {
			return solarAhead(in, i, a, cheapest, sdiff)
		}
	}
	// Positive: this day is cheaper than every later day.
	diff := (cheapest.Price.Value - d.Price.Value) / math.Abs(d.Price.Value)

	switch {
	case diff >= Threshold:
		a.CompareDate = cheapest.Date
		a.Confidence = confidence(d, cheapest, true)
		a.Rationale = append(a.Rationale, fmt.Sprintf("%.0f%% cheaper than the cheapest later day (%s, %s).",
			diff*100, cheapest.Label, basisShort(cheapest.Price)))
		if a.LaterChargePct < 1 {
			a.Summary = "Cheaper than later days, but later trips need no extra charge."
			return a
		}
		a.Recommendation = ChargeAhead
		a.Indicator = indicator(diff, a.LaterChargePct)
		a.Rationale = append(a.Rationale, fmt.Sprintf("Trips later in the window need about +%.0f%% SoC.", a.LaterChargePct))
		if !in.SoCKnown {
			a.Rationale = append(a.Rationale, "SoC unknown: the later need is estimated from trip energy.")
		}
		a.Summary = fmt.Sprintf("%s prices are cheaper than the rest of the window; charging ahead covers later trips.",
			titleBasis(d))
		return a
	case -diff >= Threshold:
		// The earliest later day that is clearly cheaper.
		var c Day
		for _, l := range later {
			if l.Price.Value <= d.Price.Value*(1-Threshold) {
				c = l
				break
			}
		}
		a.CompareDate = c.Date
		a.Confidence = confidence(d, c, false)
		a.Rationale = append(a.Rationale, fmt.Sprintf("%s looks %.0f%% cheaper (%s).",
			c.Label, (d.Price.Value-c.Price.Value)/math.Abs(d.Price.Value)*100, basisShort(c.Price)))
		switch {
		case c.SolarHomeKWh != nil:
			// Only sun strong enough to charge the car (see weakSun).
			if *c.SolarHomeKWh >= minWeakSunKWh && c.Solar != nil && c.Solar.Pct >= minSolarPct {
				a.Rationale = append(a.Rationale, fmt.Sprintf("%s: about %.0f kWh of local solar while the car is home (EVCC forecast, before household use) may cover part of the charging.",
					c.Label, *c.SolarHomeKWh))
			}
		case c.SunshinePct != nil && *c.SunshinePct >= 50 && c.HomeHours >= 4:
			a.Rationale = append(a.Rationale, fmt.Sprintf("%s is sunny (%d%% of possible sunshine, KNMI): solar may cover part of the charging.",
				c.Label, *c.SunshinePct))
		}
		switch need := needBetween(in, d.Date, c.Date); {
		case !in.SoCKnown:
			a.Summary = fmt.Sprintf("%s looks cheaper, but the SoC is unknown: keep the plan.", name(c))
		case need >= 1:
			a.Summary = fmt.Sprintf("Keep the plan: %s looks cheaper, but trips before then need +%.0f%%.", name(c), need)
		default:
			a.Recommendation = Defer
			a.DeferUntil = c.Date
			a.Rationale = append(a.Rationale, "The current charge covers every departure until then.")
			a.Summary = fmt.Sprintf("%s looks cheaper and your charge covers the trips until then.", name(c))
			if solar {
				a.Summary += " Solar still charges while the car is plugged in."
				a.Rationale = append(a.Rationale, "Postponing pauses only cheap-grid charging: EVCC still charges from the sun.")
			}
		}
		return a
	default:
		a.CompareDate = cheapest.Date
		a.Confidence = confidence(d, cheapest, true)
		a.Summary = "Prices are similar to the later days: keep the plan."
		return a
	}
}

// solarAhead advises charging ahead from the day's solar surplus: EVCC
// charges from it by itself in Smart mode, so the car only needs to be
// plugged in while the sun shines.
func solarAhead(in Input, i int, a Advice, cheapest Day, diff float64) Advice {
	d := in.Days[i]
	s := d.Solar
	a.Recommendation = ChargeAhead
	a.Solar = true
	a.CompareDate = cheapest.Date
	a.Confidence = confidence(d, cheapest, true)
	a.Indicator = indicator(diff, math.Min(s.Pct, a.LaterChargePct))
	a.SolarPct, a.SolarPrice, a.SolarGridPrice = round1(s.Pct), ptrRound3(s.Price), ptrRound3(s.GridPrice)
	a.SolarFrom, a.SolarUntil = &s.From, &s.Until
	a.Rationale = append(a.Rationale,
		fmt.Sprintf("%s: local solar while the car is home can add about %.0f%% SoC at about €%.2f/kWh, or €%.2f without the tax not paid on own solar.",
			d.Label, s.Pct, s.GridPrice, s.Price),
		fmt.Sprintf("Counted that way, %.0f%% cheaper than the cheapest later day (%s, %s).", diff*100, cheapest.Label, basisShort(cheapest.Price)),
		fmt.Sprintf("Trips later in the window need about +%.0f%% SoC.", a.LaterChargePct),
		"EVCC charges from the sun by itself in Smart mode: keep the car plugged in, no plan needed.")
	sun := d.Name + "'s sun"
	switch i {
	case 0:
		sun = "Today's sun"
	case 1:
		sun = "Tomorrow's sun"
	}
	than := fmt.Sprintf("€%.2f", cheapest.Price.Value)
	if cheapest.Price.Basis != BasisPublished {
		than = "about " + than
	}
	a.Summary = fmt.Sprintf("%s charges at about €%.2f/kWh, or €%.2f without the tax you don't pay on own solar: cheaper than %s (%s). Keep the car plugged in.",
		sun, s.GridPrice, s.Price, name(cheapest), than)
	return a
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

func basisScore(p *DayPrice) int {
	switch p.Basis {
	case BasisPublished:
		return 3
	case BasisEstimate:
		return 2
	}
	return 1
}

// confidence takes the weaker basis, minus one for stale sources and minus
// one when KNMI's wind contradicts an estimate (estimates assume wind drives
// prices: a calm "cheap" day or a windy "dear" day is doubtful). A weekly
// average counts as an estimate when the outlook ranks the other day as the
// week's cheapest or dearest.
func confidence(d, other Day, dCheaper bool) int {
	cheap, dear := d, other
	if !dCheaper {
		cheap, dear = other, d
	}
	sc, sd := basisScore(cheap.Price), basisScore(dear.Price)
	if dear.Price.Basis == BasisWeekly && cheap.Level == "cheapest" {
		sd = 2
	}
	if cheap.Price.Basis == BasisWeekly && dear.Level == "most_expensive" {
		sc = 2
	}
	c := min(sc, sd)
	if d.Price.Stale || other.Price.Stale {
		c--
	}
	switch {
	case cheap.Price.Basis != BasisPublished && cheap.WindBft != nil && *cheap.WindBft <= 2:
		c--
	case dear.Price.Basis != BasisPublished && dear.WindBft != nil && *dear.WindBft >= 5:
		c--
	}
	return max(1, min(3, c))
}

func indicator(diff, laterNeed float64) string {
	level := 0
	switch {
	case diff >= 0.25:
		level = 2
	case diff >= 0.15:
		level = 1
	}
	switch {
	case laterNeed < 10:
		level = min(level, 0)
	case laterNeed < 25:
		level = min(level, 1)
	}
	return []string{"little", "medium", "high"}[level]
}

func priceLine(d Day) string {
	return fmt.Sprintf("%s: €%.3f/kWh average incl. VAT (%s).", d.Label, d.Price.Value, d.Price.Detail)
}

func basisShort(p *DayPrice) string {
	s := fmt.Sprintf("€%.3f", p.Value)
	switch p.Basis {
	case BasisPublished:
		return s + " published"
	case BasisEstimate:
		return "about " + s + ", estimate"
	}
	return "about " + s + ", weekly average"
}

func titleBasis(d Day) string {
	if d.Price.Basis == BasisPublished {
		return "Published " + name(d)
	}
	return "Estimated " + name(d)
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
