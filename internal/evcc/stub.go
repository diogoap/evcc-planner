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
}

// NewStub returns an unplugged demo car.
func NewStub(c clock.Clock, loc *time.Location) *Stub {
	return &Stub{clock: c, loc: loc, powerKW: 3.68, capacity: 62, soc: 38}
}

// Reset restores the unplugged demo car without a plan.
func (s *Stub) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conn, s.soc, s.plan = false, 38, nil
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
	soc := 0.0
	if s.conn {
		soc = s.soc
	}
	lp := Loadpoint{
		Title: "Demo charger", Mode: "smart", Connected: s.conn, VehicleName: StubVehicle,
		VehicleTitle: "Nissan Leaf", VehicleSoc: soc, EffectiveLimitSoc: 80,
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
	return math.Round((base+wobble)*10000) / 10000
}

func (s *Stub) pvPower(now time.Time) float64 {
	local := now.In(s.loc)
	h := float64(local.Hour()) + float64(local.Minute())/60
	if h < 8.5 || h > 16.5 {
		return 0
	}
	return math.Round(1500 * math.Sin((h-8.5)/8*math.Pi))
}
