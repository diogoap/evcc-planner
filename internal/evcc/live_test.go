//go:build live

package evcc

import (
	"context"
	"testing"
	"time"

	"github.com/diogoap/evcc-planner/internal/config"
)

// TestLiveEVCC reads the real EVCC (EVCC_URL from the environment or .env)
// and checks the contract the planner relies on. It never writes: the plan
// preview is a simulation.
func TestLiveEVCC(t *testing.T) {
	cfg, err := config.Load(config.Options{
		Path: "../../config.yaml", LocalPath: "../../config.local.yaml", EnvFile: "../../.env",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EVCC.Mode != config.ModeLive {
		t.Skip("evcc.mode is not live")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := NewHTTPClient(cfg.EVCC.URL, cfg.EVCC.Timeout)

	st, err := c.State(ctx)
	if err != nil {
		t.Fatalf("read %s/api/state: %v (set EVCC_URL, e.g. in .env)", cfg.EVCC.URL, err)
	}
	lp, ok := st.Loadpoint(cfg.EVCC.Loadpoint)
	if !ok {
		t.Fatalf("loadpoint %d not found; EVCC has %d", cfg.EVCC.Loadpoint, len(st.Loadpoints))
	}
	veh, ok := st.ResolveVehicle(cfg.EVCC.Vehicle, cfg.EVCC.Loadpoint)
	if !ok {
		t.Fatalf("no vehicle resolved; set evcc.vehicle (vehicles: %d)", len(st.Vehicles))
	}
	t.Logf("EVCC %s · loadpoint %q mode=%s connected=%v soc=%.0f%% power=%.0f W",
		st.Version, lp.Title, lp.Mode, lp.Connected, lp.VehicleSoc, lp.ChargePowerW)
	t.Logf("vehicle %s (%s) capacity=%.0f kWh min=%d%% limit=%d%% plan=%v repeating=%d",
		veh.Name, veh.Title, veh.CapacityKWh, veh.MinSoc, veh.LimitSoc, veh.Plan, veh.RepeatingPlans)
	if st.GridPrice == nil || len(st.PriceSlots) == 0 {
		t.Errorf("no grid price or price forecast in EVCC state")
	} else {
		first, last := st.PriceSlots[0], st.PriceSlots[len(st.PriceSlots)-1]
		t.Logf("price now %.4f/kWh; %d slots from %s to %s", *st.GridPrice, len(st.PriceSlots),
			first.Start.Format(time.RFC3339), last.End.Format(time.RFC3339))
	}

	if !lp.Connected || lp.VehicleSoc <= 0 {
		t.Log("car unplugged: EVCC does not know the SoC, so the plan preview is skipped")
		return
	}
	target := int(lp.VehicleSoc) + 5
	if target > 100 {
		target = 100
	}
	at := time.Now().Add(12 * time.Hour).Truncate(time.Minute)
	p, err := c.PreviewSocPlan(ctx, cfg.EVCC.Loadpoint, target, at)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	t.Logf("preview to %d%% by %s: %d min in %d slots", target, at.Format(time.RFC3339), p.Duration/60, len(p.Slots))
}
