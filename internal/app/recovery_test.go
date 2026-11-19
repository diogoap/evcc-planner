package app

import (
	"net/http"
	"strings"
	"testing"

	"github.com/diogoap/evcc-planner/internal/demo"
	"github.com/diogoap/evcc-planner/internal/model"
	"github.com/diogoap/evcc-planner/internal/service"
)

// act does what a connection offers (Settings → Connections) and returns
// that connection afterwards.
func (ta *testApp) act(name, action string) service.ConnectionView {
	ta.t.Helper()
	var list []service.ConnectionView
	ta.json("POST", "/api/connections/action", map[string]any{"name": name, "action": action}, http.StatusOK, &list)
	for _, c := range list {
		if c.Name == name {
			return c
		}
	}
	ta.t.Fatalf("no connection %q in %+v", name, list)
	return service.ConnectionView{}
}

// checks lists the connection checks recorded since the last look: their
// summaries, failures marked with "!".
func checks(l *eventLog) []string {
	l.ta.t.Helper()
	var out []string
	for _, e := range l.fresh() {
		if e.Action != "connection.check" {
			continue
		}
		if e.Source != model.SourceUI {
			l.ta.t.Errorf("check from %s: %+v", e.Source, e)
		}
		mark := ""
		if e.Outcome == model.OutcomeFailure {
			mark = "!"
		}
		out = append(out, mark+e.Summary)
	}
	return out
}

func TestConnectionsOfferWhatCanBeDoneNow(t *testing.T) {
	ta := newTestApp(t, "")
	ta.json("POST", "/api/demo/reset", map[string]any{}, http.StatusNoContent, nil)
	log := &eventLog{ta: ta}
	log.fresh()

	var list []service.ConnectionView
	ta.json("GET", "/api/connections", nil, http.StatusOK, &list)
	actions := map[string]string{}
	for _, c := range list {
		actions[c.Name] = strings.Join(c.Actions, ",")
	}
	want := map[string]string{"EVCC": "check", "Google Calendar": "sync", "Google Routes": "check",
		"Tibber outlook": "refresh", "OpenRouter": "", "KNMI": "refresh", "Home Assistant": "check", "Phone notifications": ""}
	for name, w := range want {
		if actions[name] != w {
			t.Errorf("%s offers %q, want %q", name, actions[name], w)
		}
	}

	// Each check is recorded with its outcome, and an error says what to do.
	ta.fault(demo.FaultEVCC, demo.FaultDown)
	if c := ta.act("EVCC", service.ActionCheck); c.Status != "error" ||
		c.Fix != "Check that EVCC runs and that its address under Connection settings is right; the planner keeps trying." {
		t.Errorf("EVCC down = %+v", c)
	}
	ta.fault(demo.FaultEVCC, "")
	if c := ta.act("EVCC", service.ActionCheck); c.Status != "stub" || c.Fix != "" {
		t.Errorf("EVCC back = %+v", c)
	}
	ta.fault(demo.FaultCalendar, demo.FaultDown)
	if c := ta.act("Google Calendar", service.ActionSync); c.Status != "error" ||
		c.Fix != "The planner tries again at its next sync, or now with Sync now." {
		t.Errorf("calendar down = %+v", c)
	}
	ta.fault(demo.FaultCalendar, "")
	ta.act("Google Calendar", service.ActionSync)
	if c := ta.act("Google Routes", service.ActionCheck); c.Status != "stub" || c.LastSuccessAt == nil {
		t.Errorf("routes = %+v", c)
	}
	ta.fault(demo.FaultRoutes, demo.FaultDown)
	if c := ta.act("Google Routes", service.ActionCheck); c.Status != "error" ||
		c.Fix != "New places get a distance once a lookup works; Check now tries one." {
		t.Errorf("routes down = %+v", c)
	}
	ta.fault(demo.FaultRoutes, "")
	ta.act("KNMI", service.ActionRefresh)
	ta.fault(demo.FaultHomeAssistant, demo.FaultDown)
	if c := ta.act("Home Assistant", service.ActionCheck); c.Status != "error" || !strings.HasPrefix(c.Fix, "The SoC comes from EVCC") {
		t.Errorf("Home Assistant down = %+v", c)
	}
	ta.fault(demo.FaultHomeAssistant, "")
	got := checks(log)
	wantChecks := []string{
		"!Checked EVCC: connection refused (demo outage)", "Checked EVCC: works",
		"!Synced the calendars: Calendar: connection refused (demo outage)", "Synced the calendars: works",
		"Checked Google Routes: works", "!Checked Google Routes: connection refused (demo outage)",
		"Fetched the forecasts: works", "!Checked Home Assistant: connection refused (demo outage)",
	}
	if strings.Join(got, " | ") != strings.Join(wantChecks, " | ") {
		t.Errorf("checks %q, want %q", got, wantChecks)
	}

	// A test notification once there is a phone (it records itself).
	ta.notifications(nil)
	if c := ta.act("Phone notifications", service.ActionTest); c.Status != "stub" {
		t.Errorf("phone = %+v", c)
	}
	if got := log.fresh(); len(got) == 0 || got[len(got)-1].Action != "notification.send" {
		t.Errorf("test events = %+v", got)
	}

	// Only what a connection offers now.
	ta.json("POST", "/api/connections/action", map[string]any{"name": "EVCC", "action": "sync"}, http.StatusBadRequest, nil)
	ta.json("POST", "/api/connections/action", map[string]any{"name": "Printer", "action": "check"}, http.StatusBadRequest, nil)
	ta.json("POST", "/api/connections/action", map[string]any{"name": "EVCC", "action": "send"}, http.StatusBadRequest, nil)
}

func TestSendThePlanAgainAfterAFailedChange(t *testing.T) {
	ta := newTestApp(t, "")
	ta.json("POST", "/api/demo/reset", map[string]any{}, http.StatusNoContent, nil)
	ta.tripPlans(model.TripPlansAutomatic)

	// Plugging in should send the plan, but EVCC refuses it.
	ta.app.Service.UseIntegrations(func(i *service.Integrations) { i.EVCC = failingEVCC{i.EVCC} })
	ta.plug(true, 38)
	c := connection(ta, "EVCC")
	if c.Status != "error" || c.Detail != "Demo feed; changing its plan failed at 19:00: evcc refused the plan" ||
		c.Fix != "Send the plan again; the planner also tries at its next check." {
		t.Fatalf("after the failed change = %+v", c)
	}
	if got := ta.health(); len(got) != 1 || !strings.HasPrefix(got[0], "EVCC: Demo feed; changing its plan failed") {
		t.Errorf("health = %q", got)
	}

	// Unplugged, there is nothing to keep in line: the failure is gone,
	// until plugging in fails again.
	ta.plug(false, 38)
	if c := connection(ta, "EVCC"); c.Status != "stub" {
		t.Errorf("unplugged = %+v", c)
	}
	ta.plug(true, 38)
	if c := connection(ta, "EVCC"); c.Status != "error" {
		t.Fatalf("failed again = %+v", c)
	}

	// EVCC accepts it again: Send plan again writes it at once.
	ta.app.Service.UseIntegrations(func(i *service.Integrations) { i.EVCC = i.EVCC.(failingEVCC).Client })
	log := &eventLog{ta: ta}
	log.fresh()
	if c := ta.act("EVCC", service.ActionSend); c.Status != "stub" || strings.Join(c.Actions, ",") != "check" {
		t.Errorf("after sending again = %+v", c)
	}
	if p := ta.evccPlan(); p == nil || p.Soc != 42 {
		t.Errorf("plan = %+v", p)
	}
	if e := lastAction(log, "plan.send"); e == nil || e.Outcome != model.OutcomeSuccess {
		t.Errorf("send = %+v", e)
	}
}
