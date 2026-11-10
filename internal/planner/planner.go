// Package planner turns trips into departure SoC targets, charging
// durations, and feasibility. It is pure: no I/O and no clock access.
//
// Model: the car is plugged in whenever it is home, so every home period is a
// charging opportunity at the achievable battery power (wall power times
// charging efficiency), minus a time buffer. Trips that overlap are merged
// into one away block with no charging in between.
package planner

import (
	"math"
	"sort"
	"time"
)

// Params are the vehicle and planning settings.
type Params struct {
	CapacityKWh            float64 // usable
	ConsumptionKWhPer100Km float64
	ReservePct             float64 // SoC to keep on arrival home
	EfficiencyPct          float64 // wall to battery
	ChargePowerKW          float64 // at the wall
	ChargeBuffer           time.Duration
}

// BatteryPowerKW is the power reaching the battery.
func (p Params) BatteryPowerKW() float64 { return p.ChargePowerKW * p.EfficiencyPct / 100 }

// Trip is one round trip from home.
type Trip struct {
	Key        string
	DepartAt   time.Time
	ReturnAt   time.Time // back home
	DistanceKm float64   // whole round trip
}

// SoC is the latest known state of charge.
type SoC struct {
	Percent    float64
	Known      bool
	ObservedAt time.Time
}

// Status of an away block.
type Status string

const (
	StatusDone       Status = "done"
	StatusInProgress Status = "in_progress"
	StatusReady      Status = "ready"     // projected SoC already covers it
	StatusCharge     Status = "charge"    // needs charging; enough time
	StatusShortfall  Status = "shortfall" // not enough time to charge
	StatusExceeds    Status = "exceeds"   // needs more than a full battery
	StatusUnknown    Status = "unknown"   // no SoC reading yet
)

// Block is a period away from home: one trip, or overlapping trips merged.
type Block struct {
	TripKeys       []string
	DepartAt       time.Time
	ReturnAt       time.Time
	DistanceKm     float64
	EnergyKWh      float64
	EnergyPct      float64
	OwnRequiredSoC float64 // this block alone, plus reserve
	RequiredSoC    float64 // also covering later blocks, capped at 100
	Status         Status
	Overlap        bool

	// Projection; nil or zero when the SoC is unknown or the block is past.
	ChargeFrom        *time.Time
	StartSoC          *float64
	DepartSoC         *float64
	ArrivalSoC        *float64
	ChargePct         float64 // SoC points added before departure
	ChargeKWh         float64 // into the battery
	GridKWh           float64 // drawn from the grid, including losses
	ChargeDuration    time.Duration
	LatestChargeStart *time.Time
	Shortfall         float64 // percentage points missing at departure
}

// Point is a step in the projected SoC timeline.
type Point struct {
	At   time.Time `json:"at"`
	SoC  float64   `json:"soc"`
	Kind string    `json:"kind"` // now, charge_start, charge_end, depart, return
}

// Result is the computed plan.
type Result struct {
	Blocks       []Block
	Timeline     []Point
	Next         int     // index of the next future block, or -1
	StartSoC     float64 // SoC used as the starting point
	SoCEstimated bool    // SoC reduced by trips driven since the reading
}

const eps = 1e-9

// Plan computes targets and a minimum-charge projection: each departure is
// reached with just its required SoC, charging as soon as the car is home.
func Plan(now time.Time, p Params, soc SoC, input []Trip) Result {
	trips := append([]Trip(nil), input...)
	sort.SliceStable(trips, func(i, j int) bool { return trips[i].DepartAt.Before(trips[j].DepartAt) })

	var blocks []Block
	for _, t := range trips {
		if n := len(blocks); n > 0 && t.DepartAt.Before(blocks[n-1].ReturnAt) {
			b := &blocks[n-1]
			b.TripKeys = append(b.TripKeys, t.Key)
			b.DistanceKm += t.DistanceKm
			if t.ReturnAt.After(b.ReturnAt) {
				b.ReturnAt = t.ReturnAt
			}
			b.Overlap = true
			continue
		}
		blocks = append(blocks, Block{TripKeys: []string{t.Key}, DepartAt: t.DepartAt, ReturnAt: t.ReturnAt, DistanceKm: t.DistanceKm})
	}

	res := Result{Next: -1}
	for i := range blocks {
		b := &blocks[i]
		b.EnergyKWh = b.DistanceKm * p.ConsumptionKWhPer100Km / 100
		b.EnergyPct = pct(b.EnergyKWh, p)
		b.OwnRequiredSoC = b.EnergyPct + p.ReservePct
		switch {
		case !b.ReturnAt.After(now):
			b.Status = StatusDone
		case !b.DepartAt.After(now):
			b.Status = StatusInProgress
		default:
			if res.Next < 0 {
				res.Next = i
			}
		}
	}
	res.Blocks = blocks
	if res.Next < 0 {
		if soc.Known {
			res.StartSoC = soc.Percent
			res.Timeline = []Point{{At: now, SoC: soc.Percent, Kind: "now"}}
		}
		return res
	}

	// Backward pass: each departure must also leave enough for later blocks,
	// counting what the following home period can recharge.
	need := make([]float64, len(blocks))
	for i := len(blocks) - 1; i >= res.Next; i-- {
		need[i] = blocks[i].OwnRequiredSoC
		if i+1 < len(blocks) {
			gap := blocks[i+1].DepartAt.Sub(blocks[i].ReturnAt) - p.ChargeBuffer
			later := need[i+1] - chargeablePct(gap, p) + blocks[i].EnergyPct
			need[i] = math.Max(need[i], later)
		}
		blocks[i].RequiredSoC = math.Min(need[i], 100)
	}

	if !soc.Known {
		for i := res.Next; i < len(blocks); i++ {
			blocks[i].Status = StatusUnknown
		}
		return res
	}

	// Trips driven since the reading (car not plugged in afterwards) lower the
	// starting SoC.
	cur := soc.Percent
	from := now
	for i := 0; i < res.Next; i++ {
		b := blocks[i]
		if !b.DepartAt.Before(soc.ObservedAt) {
			cur -= b.EnergyPct
			res.SoCEstimated = true
		}
		if b.ReturnAt.After(from) {
			from = b.ReturnAt
		}
	}
	res.StartSoC = cur
	res.Timeline = append(res.Timeline, Point{At: now, SoC: cur, Kind: "now"})

	for i := res.Next; i < len(blocks); i++ {
		b := &blocks[i]
		start := from
		window := b.DepartAt.Sub(start) - p.ChargeBuffer
		needed := math.Max(0, math.Min(need[i], 100)-cur)
		add := math.Min(math.Min(needed, chargeablePct(window, p)), math.Max(0, 100-cur))

		b.ChargeFrom = ptr(start)
		b.StartSoC = ptr(cur)
		b.ChargePct = add
		b.ChargeKWh = add / 100 * p.CapacityKWh
		b.GridKWh = b.ChargeKWh / (p.EfficiencyPct / 100)
		b.ChargeDuration = hours(b.ChargeKWh / p.BatteryPowerKW())
		if needed > eps {
			full := hours(needed / 100 * p.CapacityKWh / p.BatteryPowerKW())
			b.LatestChargeStart = ptr(b.DepartAt.Add(-p.ChargeBuffer - full))
		}

		switch {
		case need[i] > 100+eps:
			b.Status = StatusExceeds
			b.Shortfall = need[i] - (cur + add)
		case needed <= eps:
			b.Status = StatusReady
		case add >= needed-1e-6:
			b.Status = StatusCharge
		default:
			b.Status = StatusShortfall
			b.Shortfall = needed - add
		}

		depart := cur + add
		b.DepartSoC = ptr(depart)
		if add > eps {
			res.Timeline = append(res.Timeline,
				Point{At: start, SoC: cur, Kind: "charge_start"},
				Point{At: start.Add(b.ChargeDuration), SoC: depart, Kind: "charge_end"})
		}
		cur = depart - b.EnergyPct
		b.ArrivalSoC = ptr(cur)
		res.Timeline = append(res.Timeline,
			Point{At: b.DepartAt, SoC: depart, Kind: "depart"},
			Point{At: b.ReturnAt, SoC: cur, Kind: "return"})
		from = b.ReturnAt
	}
	return res
}

func pct(kwh float64, p Params) float64 {
	if p.CapacityKWh <= 0 {
		return 0
	}
	return kwh / p.CapacityKWh * 100
}

// chargeablePct is how many SoC points can be added in d.
func chargeablePct(d time.Duration, p Params) float64 {
	if d <= 0 {
		return 0
	}
	return pct(p.BatteryPowerKW()*d.Hours(), p)
}

func hours(h float64) time.Duration { return time.Duration(h * float64(time.Hour)) }

func ptr[T any](v T) *T { return &v }
