package planner

import (
	"math"
	"testing"
	"time"
)

var (
	ams, _ = time.LoadLocation("Europe/Amsterdam")
	now    = time.Date(2026, 11, 9, 19, 0, 0, 0, ams)
	params = Params{
		CapacityKWh: 59, ConsumptionKWhPer100Km: 18, ReservePct: 15,
		EfficiencyPct: 90, ChargePowerKW: 3.68, ChargeBuffer: 30 * time.Minute,
	}
)

func at(day, hour, min int) time.Time { return time.Date(2026, 11, day, hour, min, 0, 0, ams) }

func near(t *testing.T, name string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Errorf("%s = %.3f, want %.3f ± %.3f", name, got, want, tol)
	}
}

func TestPlanExampleFromPlan(t *testing.T) {
	// 100 km at 18 kWh/100 km with 59 kWh usable and a 15% reserve needs
	// about 46% at departure.
	res := Plan(now, params, SoC{Percent: 38, Known: true, ObservedAt: now},
		[]Trip{{Key: "a", DepartAt: at(10, 8, 0), ReturnAt: at(10, 18, 0), DistanceKm: 100}})
	b := res.Blocks[0]
	near(t, "energy kWh", b.EnergyKWh, 18, 1e-9)
	near(t, "required SoC", b.RequiredSoC, 18.0/59*100+15, 1e-9)
	if math.Round(b.RequiredSoC) != 46 {
		t.Errorf("required SoC rounds to %v, want 46", math.Round(b.RequiredSoC))
	}
	if b.Status != StatusCharge {
		t.Errorf("status = %s, want charge", b.Status)
	}
	added := b.RequiredSoC - 38
	near(t, "battery kWh", b.ChargeKWh, added/100*59, 1e-6)
	near(t, "grid kWh incl. losses", b.GridKWh, added/100*59/0.9, 1e-6)
	near(t, "charge hours", b.ChargeDuration.Hours(), added/100*59/(3.68*0.9), 1e-6)
	wantLatest := at(10, 8, 0).Add(-30*time.Minute - b.ChargeDuration)
	if d := b.LatestChargeStart.Sub(wantLatest); d > time.Second || d < -time.Second {
		t.Errorf("latest start = %s, want %s", b.LatestChargeStart, wantLatest)
	}
	near(t, "arrival SoC", *b.ArrivalSoC, 15, 1e-6)
}

func TestLaterTripsRaiseEarlierTarget(t *testing.T) {
	// Two trips with only 20 minutes at home in between (less than the
	// charging buffer): the first departure must cover both.
	trips := []Trip{
		{Key: "school", DepartAt: at(10, 7, 48), ReturnAt: at(10, 8, 32), DistanceKm: 12},
		{Key: "office", DepartAt: at(10, 8, 52), ReturnAt: at(10, 12, 35), DistanceKm: 76},
	}
	res := Plan(now, params, SoC{Percent: 38, Known: true, ObservedAt: now}, trips)
	if len(res.Blocks) != 2 {
		t.Fatalf("got %d blocks, want 2", len(res.Blocks))
	}
	school, office := res.Blocks[0], res.Blocks[1]
	near(t, "office target", office.RequiredSoC, 76*0.18/59*100+15, 1e-9)
	near(t, "school target covers both", school.RequiredSoC, 88*0.18/59*100+15, 1e-9)
	near(t, "school own target", school.OwnRequiredSoC, 12*0.18/59*100+15, 1e-9)
	if res.Next != 0 {
		t.Errorf("next = %d, want 0", res.Next)
	}
}

func TestHomeChargingBetweenTripsLowersTarget(t *testing.T) {
	// Six hours at home in between can recharge ~20 kWh, so the first
	// departure only needs its own share.
	trips := []Trip{
		{Key: "a", DepartAt: at(10, 7, 0), ReturnAt: at(10, 9, 0), DistanceKm: 20},
		{Key: "b", DepartAt: at(10, 15, 0), ReturnAt: at(10, 18, 0), DistanceKm: 80},
	}
	res := Plan(now, params, SoC{Percent: 20, Known: true, ObservedAt: now}, trips)
	near(t, "first target", res.Blocks[0].RequiredSoC, 20*0.18/59*100+15, 1e-9)
	if res.Blocks[1].Status != StatusCharge {
		t.Errorf("second status = %s, want charge", res.Blocks[1].Status)
	}
}

func TestShortfallWhenTimeIsShort(t *testing.T) {
	trips := []Trip{{Key: "a", DepartAt: now.Add(45 * time.Minute), ReturnAt: now.Add(5 * time.Hour), DistanceKm: 150}}
	res := Plan(now, params, SoC{Percent: 20, Known: true, ObservedAt: now}, trips)
	b := res.Blocks[0]
	if b.Status != StatusShortfall {
		t.Fatalf("status = %s, want shortfall", b.Status)
	}
	// 15 usable minutes at 3.312 kW = 0.828 kWh = 1.4 points.
	near(t, "depart SoC", *b.DepartSoC, 20+0.25*3.312/59*100, 1e-6)
	near(t, "shortfall", b.Shortfall, b.RequiredSoC-*b.DepartSoC, 1e-6)
	if !b.LatestChargeStart.Before(now) {
		t.Errorf("latest start %s should already have passed", b.LatestChargeStart)
	}
}

func TestExceedsFullBattery(t *testing.T) {
	trips := []Trip{{Key: "far", DepartAt: at(12, 8, 0), ReturnAt: at(12, 20, 0), DistanceKm: 400}}
	res := Plan(now, params, SoC{Percent: 50, Known: true, ObservedAt: now}, trips)
	b := res.Blocks[0]
	if b.Status != StatusExceeds {
		t.Fatalf("status = %s, want exceeds", b.Status)
	}
	if b.RequiredSoC != 100 {
		t.Errorf("required SoC = %v, want capped at 100", b.RequiredSoC)
	}
	near(t, "depart SoC", *b.DepartSoC, 100, 1e-9)
}

func TestReadyWhenSoCCovers(t *testing.T) {
	trips := []Trip{{Key: "a", DepartAt: at(10, 8, 0), ReturnAt: at(10, 9, 0), DistanceKm: 10}}
	res := Plan(now, params, SoC{Percent: 70, Known: true, ObservedAt: now}, trips)
	if s := res.Blocks[0].Status; s != StatusReady {
		t.Fatalf("status = %s, want ready", s)
	}
	if res.Blocks[0].ChargeKWh != 0 || res.Blocks[0].LatestChargeStart != nil {
		t.Errorf("no charging expected: %+v", res.Blocks[0])
	}
}

func TestUnknownSoCStillComputesTargets(t *testing.T) {
	trips := []Trip{{Key: "a", DepartAt: at(10, 8, 0), ReturnAt: at(10, 9, 0), DistanceKm: 100}}
	res := Plan(now, params, SoC{}, trips)
	b := res.Blocks[0]
	if b.Status != StatusUnknown {
		t.Fatalf("status = %s, want unknown", b.Status)
	}
	if b.RequiredSoC < 45 || b.DepartSoC != nil {
		t.Errorf("want target without projection, got %+v", b)
	}
	if len(res.Timeline) != 0 {
		t.Errorf("timeline should be empty without a SoC, got %d points", len(res.Timeline))
	}
}

func TestOverlappingTripsMerge(t *testing.T) {
	trips := []Trip{
		{Key: "a", DepartAt: at(10, 9, 0), ReturnAt: at(10, 12, 0), DistanceKm: 30},
		{Key: "b", DepartAt: at(10, 11, 30), ReturnAt: at(10, 14, 0), DistanceKm: 20},
	}
	res := Plan(now, params, SoC{Percent: 60, Known: true, ObservedAt: now}, trips)
	if len(res.Blocks) != 1 {
		t.Fatalf("got %d blocks, want 1 merged block", len(res.Blocks))
	}
	b := res.Blocks[0]
	if !b.Overlap || b.DistanceKm != 50 || !b.ReturnAt.Equal(at(10, 14, 0)) {
		t.Errorf("merged block = %+v", b)
	}
}

func TestPastTripsAndEstimatedSoC(t *testing.T) {
	// The SoC was read at 07:00; a 40 km trip was driven afterwards and the
	// car was not plugged in again, so the planner subtracts it.
	readAt := at(9, 7, 0)
	trips := []Trip{
		{Key: "done", DepartAt: at(9, 8, 0), ReturnAt: at(9, 10, 0), DistanceKm: 40},
		{Key: "next", DepartAt: at(10, 8, 0), ReturnAt: at(10, 9, 0), DistanceKm: 10},
	}
	res := Plan(now, params, SoC{Percent: 60, Known: true, ObservedAt: readAt}, trips)
	if res.Blocks[0].Status != StatusDone {
		t.Errorf("first status = %s, want done", res.Blocks[0].Status)
	}
	if !res.SoCEstimated {
		t.Error("SoC should be marked estimated")
	}
	near(t, "start SoC", res.StartSoC, 60-40*0.18/59*100, 1e-9)
	if res.Next != 1 {
		t.Errorf("next = %d, want 1", res.Next)
	}
}

func TestInProgressBlockDelaysCharging(t *testing.T) {
	trips := []Trip{
		{Key: "now", DepartAt: now.Add(-time.Hour), ReturnAt: now.Add(time.Hour), DistanceKm: 20},
		{Key: "later", DepartAt: now.Add(4 * time.Hour), ReturnAt: now.Add(6 * time.Hour), DistanceKm: 60},
	}
	res := Plan(now, params, SoC{Percent: 50, Known: true, ObservedAt: now.Add(-2 * time.Hour)}, trips)
	if res.Blocks[0].Status != StatusInProgress {
		t.Fatalf("status = %s, want in_progress", res.Blocks[0].Status)
	}
	if got := *res.Blocks[1].ChargeFrom; !got.Equal(now.Add(time.Hour)) {
		t.Errorf("charging starts %s, want at return %s", got, now.Add(time.Hour))
	}
}
