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

func TestARestartKeepsAnActiveExtraPlanAndItsRestore(t *testing.T) {
	dir := t.TempDir()
	ta := newTestApp(t, dir)
	ta.pastWeek(0.40)
	ta.tripPlans(model.TripPlansManual) // extra plans restore the earlier plan
	// A plan the extra plan replaces, and should bring back when it ends.
	ta.json("POST", "/api/evcc/plan", map[string]any{"soc": 50, "time": "2026-11-11T06:00:00Z", "confirm": true}, http.StatusOK, nil)
	readyBy := "2026-11-10T05:45:00+01:00"
	var a adjustment
	ta.json("POST", "/api/adjustments", map[string]any{
		"kind": "extra_plan", "date": "2026-11-10", "soc": 70, "confirm": true, "readyBy": readyBy, "expiresAt": readyBy,
	}, http.StatusCreated, &a)
	if plan := ta.evccPlan(); plan == nil || plan.Soc != 70 {
		t.Fatalf("EVCC plan = %+v", plan)
	}

	// The planner restarts on the same database; EVCC keeps its plan.
	ta.app.Close()
	ta = openTestApp(t, dir, fixedNow)
	at, _ := time.Parse(time.RFC3339, readyBy)
	if _, err := ta.app.Stub.SetSocPlan(context.Background(), evcc.StubVehicle, 70, at); err != nil {
		t.Fatal(err)
	}
	ta.poll()
	got, err := ta.app.Store.Adjustment(a.ID)
	if err != nil || got.Status != model.AdjustActive {
		t.Fatalf("after the restart = %+v, %v", got, err)
	}
	if tue := ta.advice().Advice.Days[1]; tue.Adjustment == nil {
		t.Errorf("Tuesday should still show the extra plan: %+v", tue)
	}

	// It ends at 05:45 as planned, and the earlier plan comes back.
	ta.at(time.Date(2026, 11, 10, 5, 0, 0, 0, ams))
	ta.poll()
	if plan := ta.evccPlan(); plan == nil || plan.Soc != 70 {
		t.Fatalf("restored too early: %+v", plan)
	}
	ta.at(time.Date(2026, 11, 10, 5, 45, 0, 0, ams))
	ta.poll()
	if plan := ta.evccPlan(); plan == nil || plan.Soc != 50 {
		t.Fatalf("previous plan not restored after the restart: %+v", plan)
	}
	if got, _ := ta.app.Store.Adjustment(a.ID); got.Status != "restored" || !strings.Contains(got.Note, "50%") {
		t.Errorf("after expiry = %+v", got)
	}
}
