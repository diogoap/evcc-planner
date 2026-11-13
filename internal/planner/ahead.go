package planner

import (
	"math"
	"sort"
	"time"
)

// Slot is a published price interval with the forecast solar power in it.
type Slot struct {
	Start   time.Time
	End     time.Time
	Price   float64 // EUR/kWh including VAT
	SolarKW float64 // average forecast PV power; 0 when unknown
}

// AheadParams configure charging ahead for later trips (AUTOMATION.md).
type AheadParams struct {
	Enabled        bool
	MinSaving      float64 // EUR/kWh ahead must be cheaper to start
	MaxSoC         float64 // charging ahead never raises the target above it
	HouseholdKW    float64 // subtracted from the solar forecast
	SolarTaxSaving float64 // EUR/kWh not paid on own solar
	// Keep holds the first trip key of trips charged ahead before; they
	// stay included while ahead is no dearer.
	Keep map[string]bool
}

// AheadTrip is a later away block whose energy the target charges before
// the next departure.
type AheadTrip struct {
	Block      int
	EnergyPct  float64 // SoC points it adds to the charge
	GridKWh    float64 // from the wall, including losses
	PriceAhead float64 // EUR/kWh of that charge before the next departure
	// PriceLater is the cheapest EUR/kWh at home before the trip; Later
	// is false when that time cannot fit the charge.
	PriceLater float64
	Later      bool
	SavingEUR  float64
}

// Ahead is the target for the next departure with later trips charged
// ahead, and the planner's estimate of when the charging happens.
type Ahead struct {
	TargetSoC float64 // never below the next departure's RequiredSoC
	Trips     []AheadTrip
	SavingEUR float64
	// Charging lists the slots (or parts of them) expected to charge
	// before the next departure: the cheapest first, solar counted at
	// its price minus the tax saving. Empty with an unknown SoC.
	Charging   []Slot
	HorizonEnd time.Time
}

// unit is charging capacity in part of a slot from one source.
type unit struct {
	slot int     // index into the window's slots
	pct  float64 // SoC points it can still add
	cost float64 // EUR per kWh from the wall
}

// window is the charging capacity in [from, to), cheapest first.
type window struct {
	slots []Slot
	units []unit
	cap   []float64 // each slot's total SoC points
	used  []float64 // SoC points used in each slot
}

// minChargeShare is a charger's minimum current as a share of its usual
// maximum (6 of 16 A): solar below it cannot charge the car.
const minChargeShare = 6.0 / 16

// MinChargeKW is the least power the charger charges with: the solar surplus
// (after household use) it needs before the sun can charge the car.
func MinChargeKW(p Params) float64 { return p.ChargePowerKW * minChargeShare }

func newWindow(slots []Slot, from, to time.Time, p Params, ap AheadParams) *window {
	w := &window{}
	minKW := MinChargeKW(p)
	for _, s := range slots {
		start, end := s.Start, s.End
		if start.Before(from) {
			start = from
		}
		if end.After(to) {
			end = to
		}
		if !end.After(start) {
			continue
		}
		h := end.Sub(start).Hours()
		solar := math.Max(0, s.SolarKW-ap.HouseholdKW)
		if solar < minKW {
			solar = 0
		}
		solar = math.Min(solar, p.ChargePowerKW)
		i := len(w.slots)
		w.slots = append(w.slots, Slot{Start: start, End: end, Price: s.Price, SolarKW: s.SolarKW})
		w.cap = append(w.cap, pct(p.ChargePowerKW*p.EfficiencyPct/100*h, p))
		w.used = append(w.used, 0)
		if solar > 0 {
			w.units = append(w.units, unit{slot: i, pct: pct(solar*p.EfficiencyPct/100*h, p), cost: math.Max(0, s.Price-ap.SolarTaxSaving)})
		}
		if grid := p.ChargePowerKW - solar; grid > 0 {
			w.units = append(w.units, unit{slot: i, pct: pct(grid*p.EfficiencyPct/100*h, p), cost: s.Price})
		}
	}
	sort.SliceStable(w.units, func(i, j int) bool { return w.units[i].cost < w.units[j].cost })
	return w
}

func (w *window) clone() *window {
	c := *w
	c.units = append([]unit(nil), w.units...)
	c.used = append([]float64(nil), w.used...)
	return &c
}

// take uses the cheapest capacity for points SoC points and returns its
// cost in EUR, or ok false when the window is too short.
func (w *window) take(points float64, p Params) (eur float64, ok bool) {
	for i := range w.units {
		if points <= eps {
			break
		}
		u := &w.units[i]
		n := math.Min(u.pct, points)
		if n <= 0 {
			continue
		}
		u.pct -= n
		w.used[u.slot] += n
		points -= n
		eur += n / 100 * p.CapacityKWh / (p.EfficiencyPct / 100) * u.cost
	}
	return eur, points <= 1e-6
}

// charging lists the used part of each slot, in time order.
func (w *window) charging() []Slot {
	var out []Slot
	for i, s := range w.slots {
		if w.used[i] <= eps || w.cap[i] <= 0 {
			continue
		}
		share := math.Min(1, w.used[i]/w.cap[i])
		s.End = s.Start.Add(time.Duration(float64(s.End.Sub(s.Start)) * share))
		out = append(out, s)
	}
	return out
}

// ChargeAhead extends the next departure's target with later trips when
// charging for them before it is clearly cheaper (AUTOMATION.md). Later
// trips are checked in date order while they depart within the published
// prices; the first that is not cheaper ahead by the minimum saving, does
// not fit in the time before the next departure, or would pass the cap
// ends the check. For each, the price ahead is that of the extra cheapest
// capacity before the next departure, and the price later the cheapest
// capacity in the home periods between the next departure and that trip;
// solar surplus counts as its slot price minus the tax saving. With
// charging ahead disabled only the estimate of the minimum charge is made.
func ChargeAhead(res Result, p Params, slots []Slot, ap AheadParams) Ahead {
	if res.Next < 0 || res.Next >= len(res.Blocks) {
		return Ahead{}
	}
	next := res.Blocks[res.Next]
	out := Ahead{TargetSoC: math.Min(next.RequiredSoC, 100)}
	if len(slots) == 0 || next.StartSoC == nil || next.ChargeFrom == nil {
		return out
	}
	sorted := append([]Slot(nil), slots...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Start.Before(sorted[j].Start) })
	out.HorizonEnd = sorted[len(sorted)-1].End
	start := *next.StartSoC
	before := newWindow(sorted, *next.ChargeFrom, next.DepartAt.Add(-p.ChargeBuffer), p, ap)
	before.take(math.Max(0, out.TargetSoC-start), p)
	if ap.Enabled && !next.DepartAt.After(out.HorizonEnd) {
		before = out.addLater(res, p, sorted, ap, before, start)
	}
	out.Charging = before.charging()
	return out
}

// addLater adds later trips to the target while charging them ahead is
// cheaper, and returns the window before the next departure with their
// charge taken.
func (out *Ahead) addLater(res Result, p Params, sorted []Slot, ap AheadParams, before *window, start float64) *window {
	next := res.Blocks[res.Next]

	limit := math.Max(math.Min(ap.MaxSoC, 100), out.TargetSoC)
	energy := next.EnergyPct
	for j := res.Next + 1; j < len(res.Blocks); j++ {
		b := res.Blocks[j]
		if b.DepartAt.After(out.HorizonEnd) {
			break
		}
		energy += b.EnergyPct
		cover := math.Max(energy+p.ReservePct, out.TargetSoC)
		if cover > limit+eps {
			break
		}
		add := math.Max(0, cover-start) - math.Max(0, out.TargetSoC-start)
		if add <= eps {
			continue // the current charge already covers it
		}
		ahead := before.clone()
		costAhead, ok := ahead.take(add, p)
		if !ok {
			break
		}
		later := &window{}
		for k := res.Next; k < j; k++ {
			w := newWindow(sorted, res.Blocks[k].ReturnAt, res.Blocks[k+1].DepartAt.Add(-p.ChargeBuffer), p, ap)
			later.slots = append(later.slots, w.slots...)
			for _, u := range w.units {
				u.slot += len(later.cap)
				later.units = append(later.units, u)
			}
			later.cap = append(later.cap, w.cap...)
			later.used = append(later.used, w.used...)
		}
		sort.SliceStable(later.units, func(a, b int) bool { return later.units[a].cost < later.units[b].cost })
		costLater, fits := later.take(add, p)

		kwh := add / 100 * p.CapacityKWh / (p.EfficiencyPct / 100)
		t := AheadTrip{Block: j, EnergyPct: add, GridKWh: kwh, PriceAhead: costAhead / kwh, Later: fits}
		margin := ap.MinSaving
		if ap.Keep[b.TripKeys[0]] {
			margin = 0
		}
		if fits {
			t.PriceLater = costLater / kwh
			if t.PriceAhead+margin > t.PriceLater+1e-9 {
				break
			}
			t.SavingEUR = (t.PriceLater - t.PriceAhead) * kwh
		}
		before = ahead
		out.TargetSoC = cover
		out.Trips = append(out.Trips, t)
		out.SavingEUR += t.SavingEUR
	}
	return before
}

// SolarCharge is what the forecast solar surplus can add while the car is
// home: SoC points, the EUR/kWh it counts at (each slot's price minus the
// tax saving, weighted by energy), the slots' grid price itself (weighted
// the same), and the first and last slot it charges.
type SolarCharge struct {
	Pct       float64
	Price     float64
	GridPrice float64
	From      time.Time
	Until     time.Time
}

// Solar measures the usable solar surplus in the slots from `from`, outside
// the away periods (by each slot's middle), with the same rules as charging
// ahead: household use is subtracted, surplus below the charger's minimum
// counts as none, and no slot charges faster than the charger.
func Solar(slots []Slot, from time.Time, away [][2]time.Time, p Params, ap AheadParams) SolarCharge {
	var out SolarCharge
	minKW := MinChargeKW(p)
	wallKWh, cost, grid := 0.0, 0.0, 0.0
	for _, s := range slots {
		start := s.Start
		if start.Before(from) {
			start = from
		}
		if !s.End.After(start) {
			continue
		}
		mid := start.Add(s.End.Sub(start) / 2)
		gone := false
		for _, a := range away {
			if !mid.Before(a[0]) && mid.Before(a[1]) {
				gone = true
				break
			}
		}
		kw := math.Min(math.Max(0, s.SolarKW-ap.HouseholdKW), p.ChargePowerKW)
		if gone || kw < minKW {
			continue
		}
		kwh := kw * s.End.Sub(start).Hours()
		wallKWh += kwh
		cost += kwh * math.Max(0, s.Price-ap.SolarTaxSaving)
		grid += kwh * s.Price
		out.Pct += pct(kwh*p.EfficiencyPct/100, p)
		if out.From.IsZero() {
			out.From = start
		}
		out.Until = s.End
	}
	if wallKWh > 0 {
		out.Price, out.GridPrice = cost/wallKWh, grid/wallKWh
	}
	return out
}
