package app

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/diogoap/evcc-planner/internal/model"
)

func TestTooOldDataStopsAutomaticChangesAndShows(t *testing.T) {
	ta := newTestApp(t, "")
	ta.json("POST", "/api/demo/reset", map[string]any{}, http.StatusNoContent, nil)
	ta.tripPlans(model.TripPlansAutomatic)
	ctx := context.Background()
	mon := func(h, m int) time.Time { return time.Date(2026, 11, 9, h, m, 0, 0, ams) }

	// The car is plugged in, but EVCC's last answer was at 19:00, more
	// than three checks (a minute each) ago: no automatic changes.
	ta.app.Stub.SetVehicle(true, 38)
	ta.at(mon(19, 4))
	if err := ta.app.Service.SyncPlan(ctx); err != nil {
		t.Fatal(err)
	}
	if p := ta.evccPlan(); p != nil {
		t.Fatalf("a plan from old data: %+v", p)
	}
	var st struct {
		Reachable bool
		Error     string
	}
	ta.json("GET", "/api/status", nil, http.StatusOK, &st)
	if st.Reachable || st.Error != "no answer since 19:00" {
		t.Errorf("status = %+v", st)
	}
	if c := connection(ta, "EVCC"); c.Status != "error" || c.Detail != "No answer since 19:00" ||
		c.Fix != "The planner makes no automatic changes until EVCC answers; Check now tries at once." {
		t.Errorf("EVCC = %+v", c)
	}
	// EVCC answers: the plan follows at once.
	ta.poll()
	if err := ta.app.Service.SyncPlan(ctx); err != nil {
		t.Fatal(err)
	}
	if p := ta.evccPlan(); p == nil || p.Soc != 42 {
		t.Errorf("plan after an answer = %+v", p)
	}

	// Home Assistant: three checks (ten minutes each) without an answer.
	ta.at(mon(19, 31))
	if c := connection(ta, "Home Assistant"); c.Status != "error" || c.Detail != "No answer since 19:00" ||
		c.Fix != "The SoC comes from EVCC while plugged in; Check now tries at once." {
		t.Errorf("Home Assistant = %+v", c)
	}

	// The calendars: three hourly syncs.
	ta.at(mon(22, 1))
	if c := connection(ta, "Google Calendar"); c.Status != "error" || c.Detail != "Demo calendar: 6 events, last synced 19:00" ||
		c.Fix != "The planner tries again at its next sync, or now with Sync now." {
		t.Errorf("calendar = %+v", c)
	}

	// The daily forecasts: two days without a fetch.
	ta.at(time.Date(2026, 11, 11, 21, 1, 0, 0, ams))
	for _, name := range []string{"Tibber outlook", "KNMI"} {
		if c := connection(ta, name); c.Status != "error" || !strings.Contains(c.Detail, " · not fetched since Mon 9 Nov") ||
			c.Fix != "The advice keeps the last forecast; Refresh tries at once." {
			t.Errorf("%s = %+v", name, c)
		}
	}
	got := ta.health()
	if len(got) != 5 {
		t.Errorf("health = %q", got)
	}
}

// Ages count real time across a daylight-saving change, not the clock
// face: when the clocks go back, 02:00 CET comes an hour after 01:58 CEST;
// when they go forward, 03:00 CEST comes two minutes after 01:58 CET.
func TestOutagesAcrossADaylightSavingChange(t *testing.T) {
	type view struct {
		Reachable bool
		Error     string
	}
	status := func(ta *testApp) view {
		t.Helper()
		var st view
		ta.json("GET", "/api/status", nil, http.StatusOK, &st)
		return st
	}

	// Autumn: EVCC answered at 01:58 CEST; at 02:00 CET it is an hour
	// later, too old though the clock moved two minutes.
	ta := newTestAppAt(t, "", "2026-10-25T01:58:00+02:00")
	ta.app.Service.JobStarted("the EVCC check", time.Minute)
	ta.at(time.Date(2026, 10, 25, 1, 0, 0, 0, time.UTC)) // 02:00 CET, the second 02:00
	if st := status(ta); st.Reachable || st.Error != "no answer since 01:58" {
		t.Errorf("after the clocks went back = %+v", st)
	}
	if c := connection(ta, "EVCC"); c.Detail != "No answer since 01:58" {
		t.Errorf("EVCC = %+v", c)
	}
	var ready map[string]string
	ta.json("GET", "/readyz", nil, http.StatusServiceUnavailable, &ready)
	if ready["error"] != "background checks stopped: The EVCC check last ran at 01:58" {
		t.Errorf("readyz = %v", ready)
	}

	// Spring: EVCC answered at 01:58 CET; at 03:00 CEST it is two minutes
	// later, still fresh though the clock moved an hour; at 03:02 CEST it
	// is four minutes, too old.
	ta = newTestAppAt(t, "", "2027-03-28T01:58:00+01:00")
	ta.app.Service.JobStarted("the EVCC check", time.Minute)
	ta.at(time.Date(2027, 3, 28, 3, 0, 0, 0, ams))
	if st := status(ta); !st.Reachable || st.Error != "" {
		t.Errorf("two minutes after 01:58 CET = %+v", st)
	}
	ta.json("GET", "/readyz", nil, http.StatusOK, nil)
	ta.at(time.Date(2027, 3, 28, 3, 2, 0, 0, ams))
	if st := status(ta); st.Reachable || st.Error != "no answer since 01:58" {
		t.Errorf("four minutes after 01:58 CET = %+v", st)
	}
}
