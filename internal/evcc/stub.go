package evcc

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/diogoap/evcc-planner/internal/clock"
)

// StubVehicle is the vehicle name the stub reports.
const StubVehicle = "demo-leaf"

// Stub is an in-memory EVCC used by demo mode and tests. Prices follow a
// plausible Dutch daily curve and are published for tomorrow from 14:00.
type Stub struct {
	clock    clock.Clock
	loc      *time.Location
	powerKW  float64
	mu       sync.Mutex
	conn     bool
	soc      float64
	plan     *SocPlan
	capacity float64
	smart    *float64
}

// stubSmartCostLimit is the demo cheap-grid charging limit.
const stubSmartCostLimit = 0.2

// stubKmPerPct is the demo range per SoC point (a Leaf: about 3.2 km).
const stubKmPerPct = 3.2

// NewStub returns an unplugged demo car.
func NewStub(c clock.Clock, loc *time.Location) *Stub {
	s := &Stub{clock: c, loc: loc, powerKW: 3.68, capacity: 62}
	s.Reset()
	return s
}

// Reset restores the unplugged demo car without a plan and with the demo
// cheap-grid charging limit.
func (s *Stub) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	limit := stubSmartCostLimit
	s.conn, s.soc, s.plan, s.smart = false, 38, nil, &limit
}

// SetVehicle changes the simulated connection and SoC (demo controls).
func (s *Stub) SetVehicle(connected bool, soc float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conn = connected
	if soc > 0 {
		s.soc = soc
	}
}

// State reports the simulated EVCC state. Like EVCC, the SoC reads 0 while
// the car is unplugged.
func (s *Stub) State(ctx context.Context) (*State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock.Now()
	slots := s.priceSlots(now)
	var price *float64
	for _, r := range slots {
		if !now.Before(r.Start) && now.Before(r.End) {
			v := r.Value
			price = &v
			break
		}
	}
	soc, rangeKm := 0.0, 0.0
	if s.conn {
		soc, rangeKm = s.soc, math.Round(s.soc*stubKmPerPct)
	}
	lp := Loadpoint{
		Title: "Demo charger", Mode: "smart", Connected: s.conn, VehicleName: StubVehicle,
		VehicleTitle: "Nissan Leaf", VehicleSoc: soc, VehicleRange: rangeKm, EffectiveLimitSoc: 80,
	}
	if s.smart != nil {
		v := *s.smart
		lp.SmartCostLimit = &v
	}
	if s.plan != nil {
		lp.EffectivePlanSoc = s.plan.Soc
		t := s.plan.Time
		lp.EffectivePlanTime = &t
	}
	return &State{
		Version: "stub", Currency: "EUR", GridPrice: price, PVPowerW: s.pvPower(now),
		HomePowerW: 350,
		Loadpoints: []Loadpoint{lp},
		Vehicles: map[string]Vehicle{StubVehicle: {
			Name: StubVehicle, Title: "Nissan Leaf", CapacityKWh: s.capacity, MinSoc: 10, LimitSoc: 80, Plan: s.plan,
		}},
		PriceSlots:  slots,
		Solar:       s.solarForecast(now),
		RetrievedAt: now,
	}, nil
}

// PreviewSocPlan picks the cheapest slots before the deadline, as EVCC's
// cheap-charging planner would.
func (s *Stub) PreviewSocPlan(ctx context.Context, loadpoint int, soc int, at time.Time) (*Preview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if loadpoint != 1 {
		return nil, &APIError{Status: 400, Message: "loadpoint not found"}
	}
	now := s.clock.Now()
	current := 0.0
	if s.conn {
		current = s.soc
	}
	energy := math.Max(0, float64(soc)-current) / 100 * s.capacity
	need := time.Duration(energy / s.powerKW * float64(time.Hour))
	planTime := at

	var candidates []Rate
	for _, r := range s.priceSlots(now) {
		if r.End.After(now) && r.Start.Before(at) {
			if r.Start.Before(now) {
				r.Start = now
			}
			if r.End.After(at) {
				r.End = at
			}
			candidates = append(candidates, r)
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].Value < candidates[j].Value })
	var chosen []Rate
	remaining := need
	for _, r := range candidates {
		if remaining <= 0 {
			break
		}
		if d := r.End.Sub(r.Start); d > remaining {
			r.End = r.Start.Add(remaining)
		}
		remaining -= r.End.Sub(r.Start)
		chosen = append(chosen, r)
	}
	sort.Slice(chosen, func(i, j int) bool { return chosen[i].Start.Before(chosen[j].Start) })
	return &Preview{PlanTime: &planTime, Duration: int64(need.Seconds()), PowerW: s.powerKW * 1000, Slots: chosen}, nil
}

// SetSocPlan stores the plan.
func (s *Stub) SetSocPlan(ctx context.Context, vehicle string, soc int, at time.Time) (*SocPlan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if vehicle != StubVehicle {
		return nil, &APIError{Status: 400, Message: fmt.Sprintf("vehicle %q not found", vehicle)}
	}
	s.plan = &SocPlan{Soc: float64(soc), Time: at}
	return s.plan, nil
}

// DeleteSocPlan removes the plan.
func (s *Stub) DeleteSocPlan(ctx context.Context, vehicle string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if vehicle != StubVehicle {
		return &APIError{Status: 400, Message: fmt.Sprintf("vehicle %q not found", vehicle)}
	}
	s.plan = nil
	return nil
}

// SetSmartCostLimit stores or removes the cheap-grid charging limit.
func (s *Stub) SetSmartCostLimit(ctx context.Context, loadpoint int, limit *float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if loadpoint != 1 {
		return &APIError{Status: 400, Message: "loadpoint not found"}
	}
	if limit == nil {
		s.smart = nil
		return nil
	}
	v := *limit
	s.smart = &v
	return nil
}

func (s *Stub) priceSlots(now time.Time) []Rate {
	local := now.In(s.loc)
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, s.loc)
	days := 1
	if local.Hour() >= 14 {
		days = 2
	}
	end := time.Date(local.Year(), local.Month(), local.Day()+days, 0, 0, 0, 0, s.loc)
	var slots []Rate
	for t := day; t.Before(end); t = t.Add(15 * time.Minute) {
		slots = append(slots, Rate{Start: t, End: t.Add(15 * time.Minute), Value: stubPrice(t.In(s.loc))})
	}
	return slots
}

// stubDayLevel shifts each weekday's prices, so the demo week has cheaper and
// dearer days (windy Tuesday, calm Thursday) that match the demo outlook.
var stubDayLevel = map[time.Weekday]float64{
	time.Monday: 0.02, time.Tuesday: -0.035, time.Wednesday: 0, time.Thursday: 0.045,
	time.Friday: -0.005, time.Saturday: -0.02, time.Sunday: -0.025,
}

// stubPrice returns a retail price incl. VAT in EUR/kWh: cheap at night and
// midday, peaks in the morning and evening.
func stubPrice(t time.Time) float64 {
	h := float64(t.Hour()) + float64(t.Minute())/60
	base := 0.24
	switch {
	case h < 6:
		base = 0.21
	case h < 9:
		base = 0.30
	case h >= 11 && h < 15:
		base = 0.19
	case h >= 17 && h < 21:
		base = 0.37
	}
	wobble := 0.015 * math.Sin(float64(t.YearDay())+h/3)
	return math.Round((base+stubDayLevel[t.Weekday()]+wobble)*10000) / 10000
}

// stubSolarPeakW varies the demo PV peak by weekday (a cloudy Monday, a
// sunny Wednesday).
var stubSolarPeakW = map[time.Weekday]float64{
	time.Monday: 900, time.Tuesday: 1500, time.Wednesday: 2600, time.Thursday: 2800,
	time.Friday: 2000, time.Saturday: 1400, time.Sunday: 1000,
}

func (s *Stub) pvPower(now time.Time) float64 {
	local := now.In(s.loc)
	h := float64(local.Hour()) + float64(local.Minute())/60
	if h < 8.5 || h > 16.5 {
		return 0
	}
	return math.Round(stubSolarPeakW[local.Weekday()] * math.Sin((h-8.5)/8*math.Pi))
}

// solarForecast covers today and tomorrow, like the real EVCC setup.
func (s *Stub) solarForecast(now time.Time) *SolarForecast {
	local := now.In(s.loc)
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, s.loc)
	end := time.Date(local.Year(), local.Month(), local.Day()+2, 0, 0, 0, 0, s.loc)
	f := &SolarForecast{}
	for t := day; !t.After(end); t = t.Add(15 * time.Minute) {
		f.Points = append(f.Points, SolarPoint{Time: t, PowerW: s.pvPower(t)})
	}
	return f
}
