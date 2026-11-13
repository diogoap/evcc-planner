package evcc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/diogoap/evcc-planner/internal/clock"
)

func TestParseLiveStateFixture(t *testing.T) {
	body, err := os.ReadFile("testdata/state-unplugged.json")
	if err != nil {
		t.Fatal(err)
	}
	st, err := ParseState(body)
	if err != nil {
		t.Fatal(err)
	}
	if st.Version != "0.319.0" || st.GridPrice == nil || *st.GridPrice != 0.799 {
		t.Errorf("top-level fields: %+v", st)
	}
	lp, ok := st.Loadpoint(1)
	if !ok || lp.Title != "Alfen Eve Pro" || lp.Connected || lp.VehicleSoc != 0 || lp.VehicleName != "db:3" {
		t.Errorf("loadpoint = %+v", lp)
	}
	if lp.EffectivePlanTime != nil {
		t.Errorf("null plan time should stay nil")
	}
	v, ok := st.ResolveVehicle("", 1)
	if !ok || v.Name != "db:3" || v.CapacityKWh != 62 || v.LimitSoc != 80 || v.MinSoc != 10 || v.Plan != nil {
		t.Errorf("vehicle = %+v", v)
	}
	// Compact [start, end, value] price slots.
	if len(st.PriceSlots) != 8 || st.PriceSlots[0].Value != 0.4334 ||
		st.PriceSlots[0].End.Sub(st.PriceSlots[0].Start) != 15*time.Minute {
		t.Errorf("price slots = %+v", st.PriceSlots[:1])
	}
}

func TestParseStateVariants(t *testing.T) {
	legacy := `{"result":{"version":"0.200","loadpoints":[{"connected":true,"vehicleSoc":55,"vehicleRange":180,"vehicleName":"car"}],
		"vehicles":{"car":{"title":"Car","capacity":40,"plan":{"soc":80,"time":"2026-11-10T07:00:00Z"}}},
		"forecast":{"grid":[{"start":"2026-11-10T01:00:00+01:00","end":"2026-11-10T02:00:00+01:00","value":0.21}]}}}`
	st, err := ParseState([]byte(legacy))
	if err != nil {
		t.Fatal(err)
	}
	if lp, _ := st.Loadpoint(1); !lp.Connected || lp.VehicleSoc != 55 || lp.VehicleRange != 180 {
		t.Errorf("wrapped loadpoint = %+v", lp)
	}
	if v := st.Vehicles["car"]; v.Plan == nil || v.Plan.Soc != 80 {
		t.Errorf("vehicle plan = %+v", v.Plan)
	}
	if len(st.PriceSlots) != 1 || st.PriceSlots[0].Value != 0.21 {
		t.Errorf("object slots = %+v", st.PriceSlots)
	}
	if _, ok := st.Loadpoint(2); ok {
		t.Error("loadpoint 2 should not exist")
	}
	if _, err := ParseState([]byte("{")); err == nil {
		t.Error("want error for truncated JSON")
	}
}

func TestHTTPClientPreviewAndPlanWrite(t *testing.T) {
	var gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.EscapedPath(), r.Method
		switch {
		case strings.Contains(r.URL.Path, "/preview/"):
			w.Write([]byte(`{"planTime":"2026-11-10T06:50:00Z","duration":2460,"power":3680,
				"plan":[{"start":"2026-11-10T02:00:00+01:00","end":"2026-11-10T02:15:00+01:00","value":0.2}]}`))
		case strings.Contains(r.URL.Path, "/plan/soc/"):
			w.Write([]byte(`{"soc":46,"time":"2026-11-10T06:50:00Z"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"error":"not found"}`))
		}
	}))
	defer srv.Close()
	c := NewHTTPClient(srv.URL+"/", time.Second)
	at := time.Date(2026, 11, 10, 7, 50, 0, 0, time.FixedZone("CET", 3600))

	p, err := c.PreviewSocPlan(context.Background(), 1, 46, at)
	if err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodGet || gotPath != "/api/loadpoints/1/plan/static/preview/soc/46/2026-11-10T06:50:00Z" {
		t.Errorf("preview request %s %s", gotMethod, gotPath)
	}
	if p.Duration != 2460 || len(p.Slots) != 1 || p.Slots[0].Value != 0.2 {
		t.Errorf("preview = %+v", p)
	}

	plan, err := c.SetSocPlan(context.Background(), "db:3", 46, at)
	if err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/vehicles/db:3/plan/soc/46/2026-11-10T06:50:00Z" {
		t.Errorf("plan request %s %s", gotMethod, gotPath)
	}
	if plan.Soc != 46 {
		t.Errorf("plan = %+v", plan)
	}

	_, err = c.State(context.Background())
	if apiErr, ok := err.(*APIError); !ok || apiErr.Status != 404 || apiErr.Message != "not found" {
		t.Errorf("want APIError 404, got %v", err)
	}
}

func TestStubBehavesLikeEVCC(t *testing.T) {
	ams, _ := time.LoadLocation("Europe/Amsterdam")
	now := time.Date(2026, 11, 9, 19, 0, 0, 0, ams)
	s := NewStub(clock.Fixed{T: now}, ams)
	st, _ := s.State(context.Background())
	lp, _ := st.Loadpoint(1)
	if lp.Connected || lp.VehicleSoc != 0 {
		t.Errorf("unplugged stub must report SoC 0 like EVCC: %+v", lp)
	}
	// After 14:00 tomorrow's prices are published: 2 days of 15-minute slots.
	if len(st.PriceSlots) != 2*96 || st.GridPrice == nil {
		t.Errorf("slots = %d, price %v", len(st.PriceSlots), st.GridPrice)
	}

	s.SetVehicle(true, 38)
	deadline := time.Date(2026, 11, 10, 7, 48, 0, 0, ams)
	p, err := s.PreviewSocPlan(context.Background(), 1, 46, deadline)
	if err != nil {
		t.Fatal(err)
	}
	var charged time.Duration
	for _, r := range p.Slots {
		if r.Start.Before(now) || r.End.After(deadline) {
			t.Errorf("slot outside window: %+v", r)
		}
		charged += r.End.Sub(r.Start)
	}
	if want := time.Duration(p.Duration) * time.Second; charged < want-time.Second || charged > want+time.Second {
		t.Errorf("slots cover %s, want %s", charged, want)
	}
	if _, err := s.SetSocPlan(context.Background(), "other", 46, deadline); err == nil {
		t.Error("unknown vehicle should fail")
	}
	s.SetSocPlan(context.Background(), StubVehicle, 46, deadline)
	st, _ = s.State(context.Background())
	if st.Vehicles[StubVehicle].Plan == nil || st.Vehicles[StubVehicle].Plan.Soc != 46 {
		t.Errorf("plan not stored: %+v", st.Vehicles[StubVehicle])
	}
}

func TestHTTPClientPlanDeleteAndSmartCostLimit(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.EscapedPath())
		w.Write([]byte(`{"result":null}`))
	}))
	defer srv.Close()
	c := NewHTTPClient(srv.URL, time.Second)
	ctx := context.Background()
	if err := c.DeleteSocPlan(ctx, "db:3"); err != nil {
		t.Fatal(err)
	}
	limit := 0.2
	if err := c.SetSmartCostLimit(ctx, 1, &limit); err != nil {
		t.Fatal(err)
	}
	if err := c.SetSmartCostLimit(ctx, 1, nil); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"DELETE /api/vehicles/db:3/plan/soc",
		"POST /api/loadpoints/1/smartcostlimit/0.2",
		"DELETE /api/loadpoints/1/smartcostlimit",
	}
	if strings.Join(calls, "\n") != strings.Join(want, "\n") {
		t.Errorf("calls = %q, want %q", calls, want)
	}
}

func TestParseSmartCostLimit(t *testing.T) {
	st, err := ParseState([]byte(`{"loadpoints":[{"smartCostLimit":0.2},{"smartCostLimit":null}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if lp, _ := st.Loadpoint(1); lp.SmartCostLimit == nil || *lp.SmartCostLimit != 0.2 {
		t.Errorf("limit = %v", lp.SmartCostLimit)
	}
	if lp, _ := st.Loadpoint(2); lp.SmartCostLimit != nil {
		t.Errorf("null limit = %v", *lp.SmartCostLimit)
	}
}

func TestStubPlanDeleteAndSmartCostLimit(t *testing.T) {
	ams, _ := time.LoadLocation("Europe/Amsterdam")
	now := time.Date(2026, 11, 9, 19, 0, 0, 0, ams)
	s := NewStub(clock.Fixed{T: now}, ams)
	ctx := context.Background()
	st, _ := s.State(ctx)
	if lp, _ := st.Loadpoint(1); lp.SmartCostLimit == nil || *lp.SmartCostLimit != 0.25 {
		t.Fatalf("demo limit = %v", lp.SmartCostLimit)
	}
	s.SetSmartCostLimit(ctx, 1, nil)
	s.SetSocPlan(ctx, StubVehicle, 60, now.Add(time.Hour))
	s.DeleteSocPlan(ctx, StubVehicle)
	st, _ = s.State(ctx)
	if lp, _ := st.Loadpoint(1); lp.SmartCostLimit != nil || st.Vehicles[StubVehicle].Plan != nil {
		t.Errorf("limit %v and plan %v should be removed", lp.SmartCostLimit, st.Vehicles[StubVehicle].Plan)
	}
	// Weekday levels: the demo Tuesday is cheaper than Thursday.
	tue := stubPrice(time.Date(2026, 11, 10, 12, 0, 0, 0, ams))
	thu := stubPrice(time.Date(2026, 11, 12, 12, 0, 0, 0, ams))
	if tue >= thu {
		t.Errorf("Tuesday %.4f should be cheaper than Thursday %.4f", tue, thu)
	}
}

func TestParseSolarForecast(t *testing.T) {
	body := `{"forecast":{"solar":{"scale":1,"today":{"energy":4000,"complete":true},
		"timeseries":[[1794222000,0],[1794225600,2000],[1794229200,2000],[1794232800,0]]}}}`
	st, err := ParseState([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	f := st.Solar
	if f == nil || len(f.Points) != 4 || f.Points[1].PowerW != 2000 {
		t.Fatalf("solar = %+v", f)
	}
	start := f.Points[0].Time
	// Ramp up 1 h (1 kWh), flat 1 h (2 kWh), ramp down 1 h (1 kWh).
	if e := f.EnergyKWh(start, start.Add(3*time.Hour)); e < 3.999 || e > 4.001 {
		t.Errorf("energy = %.3f kWh, want 4", e)
	}
	if e := f.EnergyKWh(start.Add(30*time.Minute), start.Add(90*time.Minute)); e < 1.749 || e > 1.751 {
		t.Errorf("partial energy = %.3f kWh, want 1.75", e)
	}
	if e := f.EnergyKWh(start.Add(4*time.Hour), start.Add(8*time.Hour)); e != 0 {
		t.Errorf("beyond the forecast = %.3f", e)
	}
	if !f.Covers(start.Add(3*time.Hour)) || f.Covers(start.Add(4*time.Hour)) {
		t.Error("coverage wrong")
	}
	obj, _ := ParseState([]byte(`{"forecast":{"solar":{"timeseries":[{"ts":"2026-11-10T10:00:00Z","val":100},{"ts":"2026-11-10T11:00:00Z","val":100}]}}}`))
	if obj.Solar == nil || obj.Solar.EnergyKWh(obj.Solar.Points[0].Time, obj.Solar.Points[1].Time) != 0.1 {
		t.Errorf("object form = %+v", obj.Solar)
	}
	if none, _ := ParseState([]byte(`{"forecast":{"grid":[]}}`)); none.Solar != nil {
		t.Error("no solar forecast should stay nil")
	}
	// EVCC's correction applies only when "solar adjusted" is on there.
	adjusted, _ := ParseState([]byte(`{"solarAdjusted":true,` + body[1:]))
	if e := adjusted.Solar.EnergyKWh(start, start.Add(3*time.Hour)); e < 3.999 || e > 4.001 {
		t.Errorf("scale 1 = %.3f", e)
	}
	scaled, _ := ParseState([]byte(`{"solarAdjusted":true,"forecast":{"solar":{"scale":0.5,"timeseries":[[1794222000,1000],[1794225600,1000]]}}}`))
	if e := scaled.Solar.EnergyKWh(scaled.Solar.Points[0].Time, scaled.Solar.Points[1].Time); e != 0.5 {
		t.Errorf("adjusted energy = %.3f, want 0.5", e)
	}
}

func TestStubSolarForecast(t *testing.T) {
	ams, _ := time.LoadLocation("Europe/Amsterdam")
	now := time.Date(2026, 11, 9, 19, 0, 0, 0, ams)
	st, _ := NewStub(clock.Fixed{T: now}, ams).State(context.Background())
	tue := time.Date(2026, 11, 10, 0, 0, 0, 0, ams)
	wed := tue.AddDate(0, 0, 1)
	if !st.Solar.Covers(wed) || st.Solar.Covers(wed.Add(time.Minute)) {
		t.Errorf("the demo forecast covers today and tomorrow")
	}
	if e := st.Solar.EnergyKWh(tue, wed); e < 5 || e > 10 {
		t.Errorf("Tuesday %.1f kWh", e)
	}
}
