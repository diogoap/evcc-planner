//go:build live

package evcc

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/diogoap/evcc-planner/internal/config"
	"github.com/diogoap/evcc-planner/internal/model"
)

// TestLiveEVCC reads the real EVCC (EVCC_URL from the environment or .env;
// only this test reads it) and checks the contract the planner relies on. It never writes: the plan
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
	conn := model.DefaultConnectionSettings()
	conn.EVCCURL = liveAddress(t, "EVCC_URL", "http://192.0.2.10:7070")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := NewHTTPClient(conn.EVCCURL, conn.EVCCTimeout())

	st, err := c.State(ctx)
	if err != nil {
		t.Fatalf("read %s/api/state: %v", conn.EVCCURL, err)
	}
	lp, ok := st.Loadpoint(conn.EVCCLoadpoint)
	if !ok {
		t.Fatalf("loadpoint %d not found; EVCC has %d", conn.EVCCLoadpoint, len(st.Loadpoints))
	}
	veh, ok := st.ResolveVehicle(conn.EVCCVehicle, conn.EVCCLoadpoint)
	if !ok {
		t.Fatalf("no vehicle resolved; choose the vehicle under Settings → Connections (vehicles: %d)", len(st.Vehicles))
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

	if st.Solar == nil {
		t.Log("no solar forecast in EVCC state: advice uses KNMI sunshine only")
	} else {
		first, last := st.Solar.Points[0].Time, st.Solar.Points[len(st.Solar.Points)-1].Time
		t.Logf("solar forecast: %d points from %s to %s, %.1f kWh", len(st.Solar.Points),
			first.Format(time.RFC3339), last.Format(time.RFC3339), st.Solar.EnergyKWh(first, last))
	}
	t.Logf("smart cost limit (cheap-grid charging): %v", lp.SmartCostLimit)

	if !lp.Connected || lp.VehicleSoc <= 0 {
		t.Log("car unplugged: EVCC does not know the SoC, so the plan preview is skipped")
		return
	}
	target := int(lp.VehicleSoc) + 5
	if target > 100 {
		target = 100
	}
	at := time.Now().Add(12 * time.Hour).Truncate(time.Minute)
	p, err := c.PreviewSocPlan(ctx, conn.EVCCLoadpoint, target, at)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	t.Logf("preview to %d%% by %s: %d min in %d slots", target, at.Format(time.RFC3339), p.Duration/60, len(p.Slots))
}

// liveAddress reads an address to check from the environment or .env. The
// app reads addresses from Settings → Connections (its database), which live
// tests do not open.
func liveAddress(t *testing.T, key, example string) string {
	t.Helper()
	if v := os.Getenv(key); v != "" {
		return v
	}
	env, err := config.ReadEnvFile("../../.env")
	if err != nil {
		t.Fatal(err)
	}
	if env[key] == "" {
		t.Skipf("set %s to the address to check, e.g. %s=%s make test-live", key, key, example)
	}
	return env[key]
}
