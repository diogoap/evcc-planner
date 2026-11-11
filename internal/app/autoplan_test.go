package app

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/diogoap/evcc-planner/internal/evcc"
	"github.com/diogoap/evcc-planner/internal/model"
)

func TestPlanWhenThePlugGoesIn(t *testing.T) {
	ta := newTestApp(t, "")
	log := &eventLog{ta: ta}
	ctx := context.Background()
	plug := func(connected bool, soc float64) {
		t.Helper()
		ta.json("POST", "/api/demo/vehicle", map[string]any{"connected": connected, "soc": soc}, http.StatusOK, nil)
	}
	tuesday := time.Date(2026, 11, 10, 7, 48, 0, 0, ams)
	auto := model.SourceAutomation

	// The first check only learns the state: an app started with the car
	// already plugged in does not count as plugging in.
	if err := ta.app.Service.PlanOnPlugIn(ctx); err != nil {
		t.Fatal(err)
	}
	log.none("the first check")

	plug(true, 38)
	if plan := ta.evccPlan(); plan == nil || plan.Soc != 42 || !plan.Time.Equal(tuesday) {
		t.Fatalf("plan after plugging in = %+v, want the next departure's 42%% by 07:48", plan)
	}
	e := log.one("plan.send", auto)
	if e.Summary != "Car plugged in: sent plan 42% by Tue 10 Nov, 07:48 to EVCC" || e.Related != "trip:recurring:1:2026-11-10" {
		t.Errorf("event = %+v", e)
	}

	// Once per connection: a plan removed in EVCC while plugged in stays removed.
	ta.app.Stub.DeleteSocPlan(ctx, evcc.StubVehicle)
	ta.poll()
	ta.app.Service.PlanOnPlugIn(ctx)
	plug(true, 40)
	if plan := ta.evccPlan(); plan != nil {
		t.Fatalf("plan recreated while still plugged in: %+v", plan)
	}
	log.none("staying plugged in")

	// A plan with an equal or higher SoC is kept, even one ready only after
	// the next departure: someone chose it.
	plug(false, 0)
	log.none("unplugging")
	ta.app.Stub.SetSocPlan(ctx, evcc.StubVehicle, 60, time.Date(2026, 11, 11, 7, 0, 0, 0, ams))
	plug(true, 38)
	if plan := ta.evccPlan(); plan.Soc != 60 {
		t.Fatalf("higher plan replaced: %+v", plan)
	}
	if e := log.one("plan.keep", auto); !strings.Contains(e.Summary, "kept the EVCC plan (60% by 2026-11-11 07:00)") {
		t.Errorf("keep = %+v", e)
	}

	// A lower one is replaced.
	plug(false, 0)
	ta.app.Stub.SetSocPlan(ctx, evcc.StubVehicle, 30, tuesday)
	plug(true, 38)
	if plan := ta.evccPlan(); plan.Soc != 42 {
		t.Fatalf("lower plan kept: %+v", plan)
	}
	changed(t, log.one("plan.send", auto), "evccPlan", "30% by 2026-11-10 07:48", "42% by 2026-11-10 07:48")

	// A charge that already covers the departure needs no plan.
	plug(false, 0)
	ta.app.Stub.DeleteSocPlan(ctx, evcc.StubVehicle)
	plug(true, 70)
	if plan := ta.evccPlan(); plan != nil {
		t.Fatalf("plan sent although 70%% covers 42%%: %+v", plan)
	}
	if e := log.one("plan.keep", auto); e.Summary != "Car plugged in at 70%: enough for School drop-off (42%), so no plan was sent" {
		t.Errorf("covered = %+v", e)
	}

	// Turned off in Settings: nothing happens.
	var s map[string]any
	ta.json("GET", "/api/settings", nil, http.StatusOK, &s)
	if s["autoPlanOnPlugIn"] != true {
		t.Fatalf("on by default: %v", s)
	}
	s["autoPlanOnPlugIn"] = false
	ta.json("PUT", "/api/settings", s, http.StatusOK, nil)
	changed(t, log.one("settings.update", model.SourceUI), "autoPlanOnPlugIn", true, false)
	plug(false, 0)
	plug(true, 38)
	if plan := ta.evccPlan(); plan != nil {
		t.Fatalf("plan sent while turned off: %+v", plan)
	}
	log.none("plugging in while turned off")
}
