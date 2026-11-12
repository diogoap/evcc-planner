package planner

import (
	"testing"
	"time"
)

// km returns the distance that uses pct of the battery.
func km(pct float64) float64 {
	return pct / 100 * params.CapacityKWh / params.ConsumptionKWhPer100Km * 100
}

// prices are 15-minute slots from Monday 21:00 until end: €0.40 in the
// evening, night at the given price from 22:00 to 07:00, €0.31 by day.
func prices(night float64, end time.Time, solarKW func(time.Time) float64) []Slot {
	var out []Slot
	for t := at(9, 21, 0); t.Before(end); t = t.Add(15 * time.Minute) {
		h := t.Hour()
		p := 0.31
		switch {
		case h >= 22 || h < 7:
			p = night
		case h >= 17:
			p = 0.40
		}
		s := Slot{Start: t, End: t.Add(15 * time.Minute), Price: p}
		if solarKW != nil {
			s.SolarKW = solarKW(t)
		}
		out = append(out, s)
	}
	return out
}

// The example from AUTOMATION.md: plugged in Monday 21:30 at 30%; Tuesday
// a school run at 07:45 (12% of the battery, home by 08:45) and Utrecht at
// 17:00 (37%).
func exampleResult(t *testing.T, from time.Time) Result {
	t.Helper()
	res := Plan(from, params, SoC{Percent: 30, Known: true, ObservedAt: from}, []Trip{
		{Key: "school", DepartAt: at(10, 7, 45), ReturnAt: at(10, 8, 45), DistanceKm: km(12)},
		{Key: "utrecht", DepartAt: at(10, 17, 0), ReturnAt: at(10, 21, 0), DistanceKm: km(37)},
	})
	near(t, "minimum target", res.Blocks[res.Next].RequiredSoC, 27, 1e-6)
	return res
}

var aheadDefaults = AheadParams{Enabled: true, MinSaving: 0.05, MaxSoC: 90, HouseholdKW: 0.4, SolarTaxSaving: 0.11}

func TestChargeAheadWhenTheNightIsClearlyCheaper(t *testing.T) {
	res := exampleResult(t, at(9, 21, 30))
	a := ChargeAhead(res, params, prices(0.23, at(11, 0, 0), nil), aheadDefaults)
	near(t, "target", a.TargetSoC, 64, 1e-6) // 12 + 37 + 15 reserve
	if len(a.Trips) != 1 || a.Trips[0].Block != 1 || !a.Trips[0].Later {
		t.Fatalf("trips = %+v", a.Trips)
	}
	tr := a.Trips[0]
	near(t, "points added", tr.EnergyPct, 34, 1e-6)
	near(t, "price ahead", tr.PriceAhead, 0.23, 1e-9)
	near(t, "price later", tr.PriceLater, 0.31, 1e-9)
	kwh := 0.34 * 59 / 0.9
	near(t, "saving", a.SavingEUR, 0.08*kwh, 1e-6)
	if !a.HorizonEnd.Equal(at(11, 0, 0)) {
		t.Errorf("horizon = %s", a.HorizonEnd)
	}
	// The estimate charges the 34 points in the night slots before 07:15.
	var hours float64
	for _, s := range a.Charging {
		if s.Price != 0.23 || s.End.After(at(10, 7, 15)) {
			t.Errorf("charging slot %+v outside the cheap night", s)
		}
		hours += s.End.Sub(s.Start).Hours()
	}
	near(t, "charging hours", hours, 0.34*59/(3.68*0.9), 1e-6)
}

func TestChargeAheadKeepsTheMinimumOtherwise(t *testing.T) {
	sunny := func(t time.Time) float64 {
		if t.Day() == 10 && t.Hour() >= 10 && t.Hour() < 15 {
			return 3
		}
		return 0
	}
	for _, c := range []struct {
		name   string
		from   time.Time
		slots  []Slot
		params func(*AheadParams)
	}{
		// Night €0.04 cheaper: below the €0.05 minimum saving.
		{"small gap", at(9, 21, 30), prices(0.27, at(11, 0, 0), nil), nil},
		// Solar during €0.31 slots counts as €0.20/kWh, the rest €0.31:
		// about €0.25, not €0.05 dearer than the night's €0.23.
		{"solar first", at(9, 21, 30), prices(0.23, at(11, 0, 0), sunny), nil},
		{"cap", at(9, 21, 30), prices(0.23, at(11, 0, 0), nil), func(p *AheadParams) { p.MaxSoC = 60 }},
		{"beyond the published prices", at(9, 21, 30), prices(0.23, at(10, 12, 0), nil), nil},
		{"turned off", at(9, 21, 30), prices(0.23, at(11, 0, 0), nil), func(p *AheadParams) { p.Enabled = false }},
		// From 05:00 only 2¼ hours remain before the school run.
		{"no time", at(10, 5, 0), prices(0.23, at(11, 0, 0), nil), nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			ap := aheadDefaults
			if c.params != nil {
				c.params(&ap)
			}
			a := ChargeAhead(exampleResult(t, c.from), params, c.slots, ap)
			near(t, "target", a.TargetSoC, 27, 1e-6)
			if len(a.Trips) != 0 || len(a.Charging) != 0 {
				t.Errorf("ahead = %+v", a)
			}
		})
	}
}

func TestChargeAheadStaysOnceChosen(t *testing.T) {
	// A €0.04 gap does not start charging ahead, but keeps a trip that was
	// charged ahead before.
	ap := aheadDefaults
	ap.Keep = map[string]bool{"utrecht": true}
	a := ChargeAhead(exampleResult(t, at(9, 21, 30)), params, prices(0.27, at(11, 0, 0), nil), ap)
	near(t, "target", a.TargetSoC, 64, 1e-6)
}

func TestChargingEstimateForTheMinimumTarget(t *testing.T) {
	// At 20% the school run needs 7 more points: the cheapest slots, also
	// with charging ahead off.
	res := Plan(at(9, 21, 30), params, SoC{Percent: 20, Known: true, ObservedAt: at(9, 21, 30)}, []Trip{
		{Key: "school", DepartAt: at(10, 7, 45), ReturnAt: at(10, 8, 45), DistanceKm: km(12)},
	})
	a := ChargeAhead(res, params, prices(0.23, at(11, 0, 0), nil), AheadParams{})
	near(t, "target", a.TargetSoC, 27, 1e-6)
	var hours float64
	for _, s := range a.Charging {
		if s.Price != 0.23 {
			t.Errorf("slot %+v is not among the cheapest", s)
		}
		hours += s.End.Sub(s.Start).Hours()
	}
	near(t, "charging hours", hours, 0.07*59/(3.68*0.9), 1e-6)
	// Unknown SoC: no estimate.
	res = Plan(at(9, 21, 30), params, SoC{}, []Trip{
		{Key: "school", DepartAt: at(10, 7, 45), ReturnAt: at(10, 8, 45), DistanceKm: km(12)},
	})
	if a := ChargeAhead(res, params, prices(0.23, at(11, 0, 0), nil), aheadDefaults); len(a.Charging) != 0 {
		t.Errorf("estimate without a SoC = %+v", a.Charging)
	}
}

func TestOwnSolarBeatsACheaperNight(t *testing.T) {
	// Day slots at €0.20, night at €0.15. Without sun, the night is €0.05
	// cheaper and Utrecht is charged ahead. With sun, a solar kWh costs
	// €0.20 − €0.11 tax = €0.09, so Utrecht waits for the sun.
	slots := func(sunKW float64) []Slot {
		s := prices(0.15, at(11, 0, 0), func(t time.Time) float64 {
			if t.Day() == 10 && t.Hour() >= 9 && t.Hour() < 16 {
				return sunKW
			}
			return 0
		})
		for i := range s {
			if s[i].Price == 0.31 {
				s[i].Price = 0.20
			}
		}
		return s
	}
	res := exampleResult(t, at(9, 21, 30))
	near(t, "target without sun", ChargeAhead(res, params, slots(0), aheadDefaults).TargetSoC, 64, 1e-6)
	a := ChargeAhead(res, params, slots(4), aheadDefaults)
	near(t, "target with sun", a.TargetSoC, 27, 1e-6)
}
