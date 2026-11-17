package model

import "testing"

func TestConsumptionUnits(t *testing.T) {
	for _, c := range []struct {
		v    float64
		unit string
		want float64
	}{
		{18, UnitKWhPer100Km, 18},
		{5, UnitKmPerKWh, 20},
		{6.25, UnitKmPerKWh, 16},
		{0, UnitKmPerKWh, 0}, // no value, not infinity
	} {
		if got := KWhPer100Km(c.v, c.unit); got != c.want {
			t.Errorf("%v %s = %v kWh/100 km, want %v", c.v, c.unit, got, c.want)
		}
	}
	if !ConsumptionInRange(5) || !ConsumptionInRange(60) || ConsumptionInRange(4.9) || ConsumptionInRange(KWhPer100Km(0, UnitKmPerKWh)) {
		t.Error("plausible range is 5 to 60 kWh/100 km")
	}
	if got := ConsumptionRangeText(UnitKmPerKWh); got != "1.7 to 20 km/kWh" {
		t.Errorf("km/kWh range = %q", got)
	}
	if got := ConsumptionRangeText(UnitKWhPer100Km); got != "5 to 60 kWh/100 km" {
		t.Errorf("kWh/100 km range = %q", got)
	}
}
