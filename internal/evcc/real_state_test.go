package evcc

import (
	"math"
	"os"
	"testing"
	"time"
)

// realState reads a real /api/state payload kept outside Git
// (testdata/private is ignored); tests using it skip when it is absent.
func realState(t *testing.T) *State {
	t.Helper()
	body, err := os.ReadFile("../../testdata/private/evcc-state.json")
	if err != nil {
		t.Skip("no private EVCC state in testdata/private (kept out of Git)")
	}
	st, err := ParseState(body)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestRealStateSolarAndSmartCost(t *testing.T) {
	st := realState(t)
	ams, _ := time.LoadLocation("Europe/Amsterdam")
	lp, ok := st.Loadpoint(1)
	if !ok || !lp.Connected || lp.VehicleSoc <= 0 || lp.VehicleRange <= 0 || lp.SmartCostLimit == nil {
		t.Fatalf("loadpoint = %+v", lp)
	}
	if len(st.PriceSlots) == 0 || st.Solar == nil {
		t.Fatalf("prices %d, solar %v", len(st.PriceSlots), st.Solar)
	}
	// EVCC's forecast covers today and tomorrow; its own daily total for
	// tomorrow must match our integration of the same curve.
	first := st.Solar.Points[0].Time.In(ams)
	tomorrow := time.Date(first.Year(), first.Month(), first.Day()+1, 0, 0, 0, 0, ams)
	after := tomorrow.AddDate(0, 0, 1)
	if !st.Solar.Covers(after) || st.Solar.Covers(after.AddDate(0, 0, 1)) {
		t.Errorf("solar forecast should cover exactly through tomorrow: %s to %s", first, st.Solar.Points[len(st.Solar.Points)-1].Time)
	}
	got := st.Solar.EnergyKWh(tomorrow, after)
	if want := 3.820; math.Abs(got-want) > want*0.01 {
		t.Errorf("tomorrow = %.3f kWh, EVCC says %.3f", got, want)
	}
}
