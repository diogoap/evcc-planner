package aiplan

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Macro is a placeholder in the prompt, such as {{trips}}, filled with the
// planner's data when the prompt is sent.
type Macro struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	render      func(Input) string
}

// Macros are the placeholders a prompt may use, in the order the default
// prompt uses them.
var Macros = []Macro{
	{"now", "The current date and time (Europe/Amsterdam).", renderNow},
	{"goals", "Your goals (the Goals field above); by default the planner's rules, from your settings.", renderGoals},
	{"vehicle", "Battery, charger, consumption, efficiency, and the solar settings.", renderVehicle},
	{"state", "The SoC, plugged in or not, where the car is, and what EVCC has now.", renderState},
	{"trips", "Trips of the next 7 days: name, times, distance, energy, and the SoC each needs. No places or addresses.", renderTrips},
	{"daily_needs", "Per day: driving, energy, and the charge its departures need.", renderDailyNeeds},
	{"energy_prices", "EVCC's published prices, every 15-minute slot, with each day's lowest, average, and highest.", renderPrices},
	{"solar_forecast", "EVCC's local solar forecast per hour (today and tomorrow), with the surplus the car can use and what its kWh costs (the hour's price minus the tax not paid on own solar).", renderSolar},
	{"tibber_outlook", "Tibber's weekly price outlook: estimates per day, wind, and sun.", renderOutlook},
	{"weather", "KNMI's weather outlook per day: wind, sunshine, temperature, rain.", renderWeather},
	{"history", "The past 30 days: lowest, average, and highest price, driving, energy, wind, and sun, with averages.", renderHistory},
	{"automatic_plan", "What the automatic rules would do (charge ahead and each day's advice), for comparison.", renderBaseline},
}

var macroRe = regexp.MustCompile(`\{\{\s*([a-zA-Z0-9_]+)\s*\}\}`)

func macro(name string) *Macro {
	for i := range Macros {
		if Macros[i].Name == name {
			return &Macros[i]
		}
	}
	return nil
}

// Unknown lists the placeholders in prompt that are not macros, in order
// of appearance, each once.
func Unknown(prompt string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range macroRe.FindAllStringSubmatch(prompt, -1) {
		if name := strings.ToLower(m[1]); macro(name) == nil && !seen[name] {
			seen[name] = true
			out = append(out, m[1])
		}
	}
	return out
}

// Used lists the macros prompt uses.
func Used(prompt string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range macroRe.FindAllStringSubmatch(prompt, -1) {
		if name := strings.ToLower(m[1]); macro(name) != nil && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// Render fills prompt's placeholders with the data. Unknown placeholders are
// an error.
func Render(prompt string, in Input) (string, error) {
	if u := Unknown(prompt); len(u) > 0 {
		return "", fmt.Errorf("unknown placeholders: {{%s}}", strings.Join(u, "}}, {{"))
	}
	cache := map[string]string{}
	return macroRe.ReplaceAllStringFunc(prompt, func(s string) string {
		name := strings.ToLower(macroRe.FindStringSubmatch(s)[1])
		if v, ok := cache[name]; ok {
			return v
		}
		v := strings.TrimRight(macro(name).render(in), "\n")
		cache[name] = v
		return v
	}), nil
}

// DefaultPrompt is the prompt until you change it under Settings → Advice → AI mode.
const DefaultPrompt = `You plan when and how much to charge an electric car at home for the coming week. EVCC charges the car and picks the cheapest slots before each ready-by time; you decide the targets, when to charge extra, and when to wait.

It is {{now}}.

## Goals
{{goals}}

## Car and charger
{{vehicle}}

## Right now
{{state}}

## Trips (next 7 days)
"needs" is the minimum SoC to leave with; the planner always charges that by itself.
{{trips}}

## Charge needed per day
{{daily_needs}}

## Published prices
{{energy_prices}}

## Solar forecast
{{solar_forecast}}

## Weekly price outlook (Tibber)
{{tibber_outlook}}

## Weather outlook (KNMI)
{{weather}}

## The past 30 days
{{history}}

## What to decide
1. charge_ahead: the SoC to have when the next trip leaves. Start from its minimum. Add later trips when charging for them before the next trip is clearly cheaper than at home just before them (count the time at home in between and the solar). Add a reserve when the coming days drive more than usual compared with the past 30 days, when trips take a large part of the battery, or when the days ahead look dearer. Stay at or below the charge-ahead cap.
2. One entry per day, from today. A window belongs to the day it starts on (a night from 23:00 to 05:00 is the first day's).
   - charge_extra: charge above what the trips need, in a window with published prices (from, until, target_soc). Only when that window is clearly cheaper than what the coming days will likely cost (published prices, the outlook, wind and sun, the past 30 days). How much: the bigger the window's discount against what the coming days will likely cost, the higher the target, as far as the window can charge. As a rough guide: about 20% cheaper → around 50%, 30% → around 80%, 50% or more (or negative prices) → up to 100%. Charge less when the coming days look cheap too (wind, sun, the outlook), more when they look dearer or drive more than usual. Say why.
   - solar: keep the car plugged in while the sun charges it cheaply (from, until). A solar kWh costs its hour's price minus the tax not paid on own solar (shown with the solar forecast). EVCC charges from the sun by itself.
   - defer: a later day is clearly cheaper and the charge covers the trips until then. Set postpone to true (today only) to pause EVCC's cheap-grid charging until defer_until.
   - none: otherwise. Days without published prices can only be none or defer; say in the summary what the outlook expects.
3. confidence: 3 against published prices, 2 against the outlook or a full price history, 1 otherwise.
4. Explain briefly in plain words, and list the inputs that mattered most with their values.`

// SystemPrompt is the fixed part of every request: the answer's format and
// what the planner checks. The editable prompt follows as the user message.
const SystemPrompt = `You are the decision step of a home EV charging planner. Answer only with JSON that follows the given schema.
- Use the trip IDs (T1, T2, ...) and dates exactly as the data gives them. Write times as "YYYY-MM-DD HH:MM", local time in Europe/Amsterdam.
- Use trip IDs only in trip_ids. In summaries and reasons, call trips by their names (e.g. "BSO pick-up"), not their IDs.
- Every price is in EUR/kWh including VAT. SoC values are whole percent of the usable battery.
- The planner checks your answer: it never plans below a departure's minimum SoC, keeps charge ahead at or below its cap, and ignores windows outside the published prices.
- charge_ahead is sent to EVCC by itself. Whether charge extra becomes a plan by itself or needs the driver's confirmation is in the data under "Right now"; if it needs confirmation, charge the trips' energy through charge_ahead rather than charge_extra.
- Write in English, in short, plain sentences.`

// ---- rendering ----

const (
	timeLayout = "2006-01-02 15:04"
	dayLayout  = "Mon 2006-01-02"
)

func (in Input) loc() *time.Location {
	if in.Location == nil {
		return time.UTC
	}
	return in.Location
}

func (in Input) at(t time.Time) string { return t.In(in.loc()).Format(timeLayout) }

func (in Input) clock(t time.Time) string { return t.In(in.loc()).Format("15:04") }

func (in Input) day(date string) string {
	t, err := time.ParseInLocation("2006-01-02", date, in.loc())
	if err != nil {
		return date
	}
	return t.Format(dayLayout)
}

func eur(v float64) string { return fmt.Sprintf("%.3f", v) }

func eurPtr(v *float64) string {
	if v == nil {
		return "?"
	}
	return eur(*v)
}

// num writes v with at most places decimals, without trailing zeros.
func num(v float64, places int) string {
	s := fmt.Sprintf("%.*f", places, v)
	if strings.Contains(s, ".") {
		s = strings.TrimSuffix(strings.TrimRight(s, "0"), ".")
	}
	if s == "-0" {
		return "0"
	}
	return s
}

func renderNow(in Input) string {
	return in.Now.In(in.loc()).Format("Monday 2006-01-02 15:04") + " (Europe/Amsterdam)"
}

func renderGoals(in Input) string {
	if g := strings.TrimSpace(in.Goals); g != "" {
		return g
	}
	return DefaultGoals(in)
}

// DefaultGoals are the goals until you write your own: the planner's rules
// with the values of your settings and EVCC's charge limit.
func DefaultGoals(in Input) string {
	v := in.Vehicle
	var b strings.Builder
	b.WriteString("- Every trip has enough charge: never below a departure's minimum SoC (\"needs\" in the trips). The planner charges that by itself, whatever you decide.\n")
	fmt.Fprintf(&b, "- Arrive home with at least %s%% (already in each minimum).\n", num(v.ReservePct, 1))
	b.WriteString("- Charge at the cheapest practical time; solar first.\n")
	fmt.Fprintf(&b, "- Charge ahead (the target for the next trip, with later trips and a reserve) stays at or below %d%%.\n", v.MaxAheadSoC)
	if v.LimitSoC > 0 {
		fmt.Fprintf(&b, "- Battery care: EVCC's charge limit is %d%%; go above it only when it clearly saves money or trips need it.\n", v.LimitSoC)
	} else {
		b.WriteString("- Battery care: stay at or below 80% unless it clearly saves money or trips need it.\n")
	}
	fmt.Fprintf(&b, "- The automatic rules charge later trips ahead when that saves at least €%.2f/kWh.", v.MinSaving)
	return b.String()
}

func renderVehicle(in Input) string {
	v := in.Vehicle
	return fmt.Sprintf(`- Usable battery: %s kWh (1%% = %s kWh)
- Charger: %s kW at the wall, %s%% efficient, so about %s%% per hour
- Consumption: %s kWh/100 km
- Charging must end %d min before each departure
- Solar: household use %s kW is subtracted from the forecast; the charger needs at least %s kW of surplus to charge from the sun
- A solar kWh counts as the grid price minus €%.2f (tax not paid on own solar)`,
		num(v.CapacityKWh, 1), num(v.CapacityKWh/100, 2), num(v.ChargeKW, 2), num(v.EfficiencyPct, 0),
		num(v.ChargeKW*v.EfficiencyPct/v.CapacityKWh, 1), num(v.ConsumptionKWhPer100Km, 1), v.ChargeBufferMin,
		num(v.HouseholdKW, 2), num(v.MinSolarKW, 2), v.SolarTaxSaving)
}

func renderState(in Input) string {
	s := in.State
	var b strings.Builder
	switch {
	case s.SoC == nil:
		b.WriteString("- SoC: unknown (read when the car is plugged in)\n")
	default:
		fmt.Fprintf(&b, "- SoC: %s%%", num(*s.SoC, 0))
		if s.SoCSource != "" && s.SoCAt != nil {
			fmt.Fprintf(&b, " (from %s, %s)", s.SoCSource, in.at(*s.SoCAt))
		}
		if s.Projected != nil && math.Abs(*s.Projected-*s.SoC) >= 0.5 {
			fmt.Fprintf(&b, "; about %s%% after the trips driven since", num(*s.Projected, 0))
		}
		b.WriteString("\n")
	}
	switch {
	case s.Charging:
		b.WriteString("- Plugged in and charging\n")
	case s.PluggedIn:
		b.WriteString("- Plugged in\n")
	default:
		b.WriteString("- Unplugged\n")
	}
	// Only home or away: a zone's name could be a private place.
	switch s.Location {
	case "":
	case "home":
		b.WriteString("- The car is home\n")
	default:
		b.WriteString("- The car is away\n")
	}
	if s.Mode != "" {
		fmt.Fprintf(&b, "- EVCC mode: %s\n", s.Mode)
	}
	if s.Plan != nil {
		fmt.Fprintf(&b, "- EVCC plan: %d%% by %s (set by %s)\n", s.Plan.SoC, in.at(s.Plan.Time), s.Plan.By)
	} else {
		b.WriteString("- EVCC plan: none\n")
	}
	if s.CheapLimit != nil {
		fmt.Fprintf(&b, "- EVCC charges from the grid by itself at or below €%s/kWh (cheap-grid limit)\n", eur(*s.CheapLimit))
	}
	if s.ExtraPlan != nil {
		fmt.Fprintf(&b, "- Active extra plan: %d%% by %s\n", s.ExtraPlan.SoC, in.at(s.ExtraPlan.Time))
	}
	if s.PostponedUntil != nil {
		fmt.Fprintf(&b, "- Cheap-grid charging paused until %s\n", in.at(*s.PostponedUntil))
	}
	if s.AutoExtra {
		b.WriteString("- Your charge extra (and postponing) become plans by themselves\n")
	} else {
		b.WriteString("- Your charge extra (and postponing) become plans only when the driver confirms them\n")
	}
	if in.Next != nil {
		n := in.Next
		fmt.Fprintf(&b, "- Next trip: %s leaves %s and needs at least %d%%", in.tripLabel(n.TripID), in.at(n.DepartAt), n.MinTargetSoC)
		if n.StartSoC != nil {
			fmt.Fprintf(&b, "; about %s%% when charging can start", num(*n.StartSoC, 0))
		}
		if n.LatestChargeStart != nil {
			fmt.Fprintf(&b, "; charging must start by %s", in.at(*n.LatestChargeStart))
		}
	} else {
		b.WriteString("- No upcoming trip")
	}
	return b.String()
}

// tripLabel is a trip's ID with its name: T1 "BSO pick-up".
func (in Input) tripLabel(id string) string {
	for _, t := range in.Trips {
		if t.ID == id && t.Name != "" {
			return fmt.Sprintf("%s %q", id, t.Name)
		}
	}
	return id
}

func renderTrips(in Input) string {
	if len(in.Trips) == 0 {
		return "No trips in the next 7 days."
	}
	var b strings.Builder
	for _, t := range in.Trips {
		fmt.Fprintf(&b, "%s: leaves %s, arrives by %s, back home %s; %s km, %s kWh, %s%% of the battery; needs %s%% at departure",
			in.tripLabel(t.ID), in.at(t.DepartAt), in.clock(t.ArriveBy), in.at(t.ReturnAt), num(t.DistanceKm, 1), num(t.EnergyKWh, 1),
			num(t.EnergyPct, 1), num(math.Ceil(t.RequiredSoC-1e-6), 0))
		var notes []string
		if t.Count > 1 {
			notes = append(notes, fmt.Sprintf("%d trips together", t.Count))
		}
		switch t.Status {
		case "ready":
			notes = append(notes, "covered by the current charge")
		case "shortfall":
			notes = append(notes, "not enough time to charge")
		case "exceeds":
			notes = append(notes, "needs more than a full battery")
		}
		if t.Unconfirmed {
			notes = append(notes, "calendar event that may not be a car trip")
		}
		if len(notes) > 0 {
			b.WriteString(" (" + strings.Join(notes, "; ") + ")")
		}
		b.WriteString("\n")
	}
	return b.String()
}

func renderDailyNeeds(in Input) string {
	var b strings.Builder
	for _, d := range in.Days {
		fmt.Fprintf(&b, "%s: ", in.day(d.Date))
		if d.Trips == 0 {
			b.WriteString("no trips")
		} else {
			fmt.Fprintf(&b, "%d trip%s, %s km, %s kWh", d.Trips, plural(d.Trips), num(d.DistanceKm, 1), num(d.EnergyKWh, 1))
			if d.ChargePct > 0 {
				fmt.Fprintf(&b, "; its departures need +%s%% charge", num(math.Ceil(d.ChargePct-1e-6), 0))
			}
			if d.TargetSoC != nil {
				fmt.Fprintf(&b, "; the first leaves with at least %d%%", *d.TargetSoC)
			}
		}
		fmt.Fprintf(&b, "; home %s h\n", num(d.HomeHours, 1))
	}
	return b.String()
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func renderPrices(in Input) string {
	if len(in.Prices) == 0 {
		return "No published prices."
	}
	var b strings.Builder
	last := in.Prices[len(in.Prices)-1].End
	fmt.Fprintf(&b, "EUR/kWh incl. VAT, published until %s. Each row: the hour, then its slots in order; \"away\": the car is away for part of that hour.\n", in.at(last))
	for _, d := range in.Days {
		if d.Published && d.PriceAvg != nil {
			fmt.Fprintf(&b, "%s: lowest %s, average %s, highest %s\n", in.day(d.Date), eurPtr(d.PriceMin), eurPtr(d.PriceAvg), eurPtr(d.PriceMax))
		}
	}
	date, hour := "", ""
	var row []string
	away := false
	flush := func() {
		if len(row) == 0 {
			return
		}
		fmt.Fprintf(&b, "  %s  %s", hour, strings.Join(row, " "))
		if away {
			b.WriteString("  away")
		}
		b.WriteString("\n")
		row, away = nil, false
	}
	for _, s := range in.Prices {
		t := s.Start.In(in.loc())
		if d := t.Format("2006-01-02"); d != date {
			flush()
			date = d
			fmt.Fprintf(&b, "%s:\n", t.Format(dayLayout))
		}
		if h := t.Format("15:00"); h != hour {
			flush()
			hour = h
		}
		row = append(row, eur(s.Price))
		away = away || s.Away
	}
	flush()
	return b.String()
}

func renderSolar(in Input) string {
	if len(in.Solar) == 0 {
		return "No local solar forecast."
	}
	v := in.Vehicle
	var b strings.Builder
	fmt.Fprintf(&b, "Forecast power per hour. The car can use the surplus: the forecast minus household use (%s kW), from %s kW (the charger's minimum) up to %s kW. A solar kWh costs the hour's price minus €%.2f (tax not paid on own solar).\n",
		num(v.HouseholdKW, 2), num(v.MinSolarKW, 2), num(v.ChargeKW, 2), v.SolarTaxSaving)
	date := ""
	var row []string
	flush := func() {
		switch {
		case len(row) > 0:
			b.WriteString("  " + strings.Join(row, " · ") + "\n")
		case date != "":
			b.WriteString("  no sun\n")
		}
		row = nil
	}
	for _, s := range in.Solar {
		t := s.Start.In(in.loc())
		if d := t.Format("2006-01-02"); d != date {
			flush()
			date = d
			fmt.Fprintf(&b, "%s", t.Format(dayLayout))
			for _, day := range in.Days {
				if day.Date == d && day.SolarKWh != nil {
					fmt.Fprintf(&b, " (%s kWh", num(*day.SolarKWh, 1))
					if day.SolarHomeKWh != nil {
						fmt.Fprintf(&b, ", %s kWh while the car is home", num(*day.SolarHomeKWh, 1))
					}
					b.WriteString(")")
				}
			}
			b.WriteString(":\n")
		}
		if s.KW >= 0.05 {
			row = append(row, in.solarHour(s))
		}
	}
	flush()
	b.WriteString("Later days: no local forecast; use the weather outlook's sunshine.")
	return b.String()
}

// solarHour is an hour of the solar forecast: "12:00 2.1 kW (1.7 kW usable
// at 0.052)", "09:00 0.6 kW (too little to charge)".
func (in Input) solarHour(s SolarSlot) string {
	v := in.Vehicle
	out := fmt.Sprintf("%s %s kW", s.Start.In(in.loc()).Format("15:04"), num(s.KW, 1))
	usable := math.Min(math.Max(0, s.KW-v.HouseholdKW), v.ChargeKW)
	if usable < v.MinSolarKW || usable <= 0 {
		return out + " (too little to charge)"
	}
	var sum, hours float64
	away := false
	for _, p := range in.Prices {
		start, end := maxTime(p.Start, s.Start), minTime(p.End, s.End)
		if end.After(start) {
			h := end.Sub(start).Hours()
			sum, hours = sum+p.Price*h, hours+h
			away = away || p.Away
		}
	}
	if hours == 0 {
		return out + fmt.Sprintf(" (%s kW usable)", num(usable, 1))
	}
	out += fmt.Sprintf(" (%s kW usable at %s", num(usable, 1), eur(sum/hours-v.SolarTaxSaving))
	if away {
		out += ", car away part of it"
	}
	return out + ")"
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func renderOutlook(in Input) string {
	o := in.Outlook
	if o == nil {
		return "No weekly outlook."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Report %s to %s, fetched %s", o.ValidFrom, o.ValidTo, in.at(o.FetchedAt))
	if o.Stale {
		b.WriteString(" (old)")
	}
	b.WriteString(".\n")
	if o.Summary != "" {
		fmt.Fprintf(&b, "Summary: %s\n", o.Summary)
	}
	if o.Trend != "" && o.Trend != "unknown" {
		fmt.Fprintf(&b, "Prices compared with last week: %s\n", o.Trend)
	}
	if o.WeeklyPrice != nil {
		fmt.Fprintf(&b, "Weekly average: %s EUR/kWh incl. VAT\n", eur(*o.WeeklyPrice))
	}
	for _, d := range o.Days {
		fmt.Fprintf(&b, "%s:", in.day(d.Date))
		if d.Price != nil {
			fmt.Fprintf(&b, " about %s average", eur(*d.Price))
		}
		for _, f := range [][2]string{{"level", d.Level}, {"wind", d.Wind}, {"solar", d.Solar}} {
			if f[1] != "" && f[1] != "unknown" {
				fmt.Fprintf(&b, " %s %s", f[0], strings.ReplaceAll(f[1], "_", " "))
			}
		}
		if d.Note != "" {
			fmt.Fprintf(&b, ". %s", d.Note)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func renderWeather(in Input) string {
	if len(in.Weather) == 0 {
		return "No weather outlook."
	}
	var b strings.Builder
	for _, w := range in.Weather {
		var parts []string
		if w.WindBft != nil {
			p := fmt.Sprintf("wind %d Bft", *w.WindBft)
			if w.WindDir != "" {
				p += " " + w.WindDir
			}
			parts = append(parts, p)
		}
		if w.SunshinePct != nil {
			parts = append(parts, fmt.Sprintf("sunshine %d%% of possible", *w.SunshinePct))
		}
		if w.TempMinC != nil && w.TempMaxC != nil {
			parts = append(parts, fmt.Sprintf("%s to %s °C", num(*w.TempMinC, 0), num(*w.TempMaxC, 0)))
		}
		if w.RainChancePct != nil {
			parts = append(parts, fmt.Sprintf("rain chance %d%%", *w.RainChancePct))
		}
		fmt.Fprintf(&b, "%s: %s\n", in.day(w.Date), strings.Join(parts, ", "))
	}
	return b.String()
}

func renderHistory(in Input) string {
	if len(in.History) == 0 {
		return "No price history yet."
	}
	var b strings.Builder
	type sum struct {
		v float64
		n int
	}
	var low, avg, high, km, kwh, wind, sun sum
	add := func(s *sum, v *float64) {
		if v != nil {
			s.v += *v
			s.n++
		}
	}
	addInt := func(s *sum, v *int) {
		if v != nil {
			s.v += float64(*v)
			s.n++
		}
	}
	for _, d := range in.History {
		fmt.Fprintf(&b, "%s: ", in.day(d.Date))
		if d.PriceAvg != nil {
			fmt.Fprintf(&b, "lowest %s, average %s, highest %s", eurPtr(d.PriceMin), eurPtr(d.PriceAvg), eurPtr(d.PriceMax))
		} else {
			b.WriteString("no prices")
		}
		if d.DistanceKm != nil {
			fmt.Fprintf(&b, "; %s km", num(*d.DistanceKm, 0))
		}
		if d.EnergyKWh != nil {
			fmt.Fprintf(&b, ", %s kWh", num(*d.EnergyKWh, 1))
		}
		if d.WindBft != nil {
			fmt.Fprintf(&b, "; wind %d Bft", *d.WindBft)
		}
		if d.SunshinePct != nil {
			fmt.Fprintf(&b, "; sun %d%%", *d.SunshinePct)
		}
		b.WriteString("\n")
		add(&low, d.PriceMin)
		add(&avg, d.PriceAvg)
		add(&high, d.PriceMax)
		add(&km, d.DistanceKm)
		add(&kwh, d.EnergyKWh)
		addInt(&wind, d.WindBft)
		addInt(&sun, d.SunshinePct)
	}
	mean := func(s sum, f func(float64) string) string {
		if s.n == 0 {
			return "?"
		}
		return f(s.v / float64(s.n))
	}
	n := func(places int) func(float64) string { return func(v float64) string { return num(v, places) } }
	parts := []string{}
	if avg.n > 0 {
		parts = append(parts, fmt.Sprintf("lowest %s, average %s, highest %s EUR/kWh", mean(low, eur), mean(avg, eur), mean(high, eur)))
	}
	if km.n > 0 {
		parts = append(parts, fmt.Sprintf("%s km and %s kWh driving a day", mean(km, n(0)), mean(kwh, n(1))))
	}
	if wind.n > 0 {
		parts = append(parts, fmt.Sprintf("wind %s Bft", mean(wind, n(1))))
	}
	if sun.n > 0 {
		parts = append(parts, fmt.Sprintf("sun %s%%", mean(sun, n(0))))
	}
	fmt.Fprintf(&b, "Averages over %d days: %s.", len(in.History), strings.Join(parts, "; "))
	return b.String()
}

func renderBaseline(in Input) string {
	bl := in.Baseline
	var b strings.Builder
	if in.Next != nil && bl.TargetSoC > 0 {
		fmt.Fprintf(&b, "Charge ahead: %d%% by %s", bl.TargetSoC, in.at(in.Next.DepartAt))
		if len(bl.AheadIDs) > 0 {
			fmt.Fprintf(&b, ", also for %s", strings.Join(bl.AheadIDs, ", "))
		}
		b.WriteString("\n")
	}
	days := append([]DayResult(nil), bl.Days...)
	sort.SliceStable(days, func(i, j int) bool { return days[i].Date < days[j].Date })
	for _, d := range days {
		fmt.Fprintf(&b, "%s: %s", in.day(d.Date), strings.ReplaceAll(d.Recommendation, "_", " "))
		if d.TargetSoC > 0 {
			fmt.Fprintf(&b, " to %d%%", d.TargetSoC)
		}
		if d.From != nil && d.Until != nil {
			fmt.Fprintf(&b, " %s to %s", in.at(*d.From), in.at(*d.Until))
		}
		if d.Summary != "" {
			fmt.Fprintf(&b, ". %s", d.Summary)
		}
		b.WriteString("\n")
	}
	if b.Len() == 0 {
		return "No suggestions from the automatic rules."
	}
	return b.String()
}
