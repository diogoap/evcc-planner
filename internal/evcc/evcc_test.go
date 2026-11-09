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
	legacy := `{"result":{"version":"0.200","loadpoints":[{"connected":true,"vehicleSoc":55,"vehicleName":"car"}],
		"vehicles":{"car":{"title":"Car","capacity":40,"plan":{"soc":80,"time":"2026-11-10T07:00:00Z"}}},
		"forecast":{"grid":[{"start":"2026-11-10T01:00:00+01:00","end":"2026-11-10T02:00:00+01:00","value":0.21}]}}}`
	st, err := ParseState([]byte(legacy))
	if err != nil {
		t.Fatal(err)
	}
	if lp, _ := st.Loadpoint(1); !lp.Connected || lp.VehicleSoc != 55 {
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
