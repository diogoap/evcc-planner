package app

import (
	"net/http"
	"strings"
	"testing"

	"github.com/diogoap/evcc-planner/internal/demo"
	"github.com/diogoap/evcc-planner/internal/service"
)

// health is the Overview's health line: "name: detail" per connection with
// an error, and whether each says when it last worked.
func (ta *testApp) health() []string {
	ta.t.Helper()
	var st struct{ Health []service.HealthIssue }
	ta.json("GET", "/api/status", nil, http.StatusOK, &st)
	out := []string{}
	for _, h := range st.Health {
		line := h.Name + ": " + h.Detail
		if h.LastSuccessAt != nil {
			line += " · worked before"
		}
		out = append(out, line)
	}
	return out
}

func (ta *testApp) fault(service, fault string) []demo.Fault {
	ta.t.Helper()
	var list []demo.Fault
	ta.json("PUT", "/api/demo/faults", map[string]any{"service": service, "fault": fault}, http.StatusOK, &list)
	return list
}

func expectHealth(t *testing.T, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, " | ") != strings.Join(want, " | ") {
		t.Fatalf("health %q, want %q", got, want)
	}
}

func TestDemoOutagesShowOnTheHealthLine(t *testing.T) {
	ta := newTestApp(t, "")
	ta.json("POST", "/api/demo/reset", map[string]any{}, http.StatusNoContent, nil)
	expectHealth(t, ta.health())
	var list []demo.Fault
	ta.json("GET", "/api/demo/faults", nil, http.StatusOK, &list)
	if len(list) != 4 || list[0] != (demo.Fault{Service: "evcc", Name: "EVCC"}) || list[1].Fault != "" {
		t.Fatalf("faults = %+v", list)
	}

	// Home Assistant down: the SoC reading fails, and so does the phone.
	if list = ta.fault(demo.FaultHomeAssistant, demo.FaultDown); list[1].Fault != demo.FaultDown {
		t.Fatalf("after setting = %+v", list)
	}
	expectHealth(t, ta.health(), "Home Assistant: connection refused (demo outage) · worked before")
	ta.notifications(nil)
	ta.json("POST", "/api/notifications/test", map[string]any{}, http.StatusBadGateway, nil)
	expectHealth(t, ta.health(), "Home Assistant: connection refused (demo outage) · worked before",
		"Phone notifications: 1 phone; the last message failed at 19:00: mobile_app_demo_phone: connection refused (demo outage)")

	// EVCC and the calendar too.
	ta.fault(demo.FaultEVCC, demo.FaultDown)
	ta.fault(demo.FaultCalendar, demo.FaultDown)
	got := ta.health()
	if len(got) != 4 || got[0] != "EVCC: connection refused (demo outage) · worked before" ||
		!strings.HasPrefix(got[1], "Google Calendar: ") || !strings.Contains(got[1], "connection refused (demo outage)") {
		t.Fatalf("health = %q", got)
	}

	// Back up: the next checks clear them (the phone with its next send).
	ta.fault(demo.FaultEVCC, "")
	ta.fault(demo.FaultCalendar, "")
	ta.fault(demo.FaultHomeAssistant, "")
	ta.json("POST", "/api/notifications/test", map[string]any{}, http.StatusOK, nil)
	expectHealth(t, ta.health())

	// Route lookups report how the last one went: a new place can't be
	// looked up while they are down, and the next lookup clears it.
	ta.fault(demo.FaultRoutes, demo.FaultDown)
	ta.json("POST", "/api/trips", map[string]any{
		"title": "Hardware store", "destination": "Bouwmarkt 5, Demo Town", "date": "2026-11-12", "start": "10:00",
	}, http.StatusCreated, nil)
	ta.plan()
	expectHealth(t, ta.health(), "Google Routes: The last lookup failed at 19:00: connection refused (demo outage); "+
		"trips use a cached route where there is one · worked before")
	ta.fault(demo.FaultRoutes, "")
	ta.plan()
	expectHealth(t, ta.health())

	// A reset brings every service back.
	ta.fault(demo.FaultEVCC, demo.FaultDown)
	ta.json("POST", "/api/demo/reset", map[string]any{}, http.StatusNoContent, nil)
	expectHealth(t, ta.health())
	ta.json("PUT", "/api/demo/faults", map[string]any{"service": "evcc", "fault": "slow"}, http.StatusBadRequest, nil)
	ta.json("PUT", "/api/demo/faults", map[string]any{"service": "printer", "fault": "down"}, http.StatusBadRequest, nil)
}
