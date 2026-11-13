package planner

import (
	"math"
	"testing"
	"time"
)

func TestSolarCountsUsableSurplusWhileHome(t *testing.T) {
	p := Params{CapacityKWh: 59, EfficiencyPct: 90, ChargePowerKW: 3.68}
	ap := AheadParams{HouseholdKW: 0.4, SolarTaxSaving: 0.11}
	at := func(h, m int) time.Time { return time.Date(2026, 11, 13, h, m, 0, 0, time.UTC) }
	slot := func(h, m int, price, kw float64) Slot {
		return Slot{Start: at(h, m), End: at(h, m).Add(15 * time.Minute), Price: price, SolarKW: kw}
	}
	slots := []Slot{
		slot(11, 0, 0.40, 1.5), // 1.1 kW surplus: below the charger's minimum (1.38 kW)
		slot(11, 15, 0.38, 2.4),
		slot(11, 30, 0.36, 2.4), // away
		slot(11, 45, 0.34, 5.0), // capped at the charger's 3.68 kW
	}
	away := [][2]time.Time{{at(11, 30), at(11, 45)}}

	got := Solar(slots, at(11, 0), away, p, ap)
	// 2.0 kW × 0.25 h at €0.27, then 3.68 kW × 0.25 h at €0.23.
	wall := 2.0*0.25 + 3.68*0.25
	if want := wall * 0.9 / 59 * 100; math.Abs(got.Pct-want) > 1e-9 {
		t.Errorf("Pct = %.3f, want %.3f", got.Pct, want)
	}
	if want := (2.0*0.25*0.27 + 3.68*0.25*0.23) / wall; math.Abs(got.Price-want) > 1e-9 {
		t.Errorf("Price = %.4f, want %.4f", got.Price, want)
	}
	// The grid price in the same slots, before the tax saving.
	if want := (2.0*0.25*0.38 + 3.68*0.25*0.34) / wall; math.Abs(got.GridPrice-want) > 1e-9 {
		t.Errorf("GridPrice = %.4f, want %.4f", got.GridPrice, want)
	}
	if math.Abs(MinChargeKW(p)-1.38) > 1e-9 {
		t.Errorf("MinChargeKW = %v", MinChargeKW(p))
	}
	if !got.From.Equal(at(11, 15)) || !got.Until.Equal(at(12, 0)) {
		t.Errorf("window %s–%s, want 11:15–12:00", got.From, got.Until)
	}
	if none := Solar(slots, at(12, 0), nil, p, ap); none.Pct != 0 || !none.From.IsZero() {
		t.Errorf("after the slots: %+v", none)
	}
}
