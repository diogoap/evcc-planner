package app

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/diogoap/evcc-planner/internal/demo"
	"github.com/diogoap/evcc-planner/internal/forecast"
	"github.com/diogoap/evcc-planner/internal/model"
	"github.com/diogoap/evcc-planner/internal/rejected"
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
	if len(list) != 4 || list[0].Service != "evcc" || list[0].Name != "EVCC" || list[0].Fault != "" ||
		strings.Join(list[0].Faults, ",") != "down" || strings.Join(list[1].Faults, ",") != "down,key" {
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
	ta.json("PUT", "/api/demo/faults", map[string]any{"service": "evcc", "fault": "key"}, http.StatusBadRequest, nil)
	ta.json("PUT", "/api/demo/faults", map[string]any{"service": "printer", "fault": "down"}, http.StatusBadRequest, nil)
}

// fixes is the health line with each issue's fix.
func (ta *testApp) fixes() []string {
	ta.t.Helper()
	var st struct{ Health []service.HealthIssue }
	ta.json("GET", "/api/status", nil, http.StatusOK, &st)
	out := []string{}
	for _, h := range st.Health {
		out = append(out, h.Name+": "+h.Detail+" → "+h.Fix)
	}
	return out
}

func TestRejectedKeysSayWhatToRenew(t *testing.T) {
	ta := newTestApp(t, "")
	ta.json("POST", "/api/demo/reset", map[string]any{}, http.StatusNoContent, nil)

	// Home Assistant refuses the token: for the SoC, and for the phone.
	ta.fault(demo.FaultHomeAssistant, demo.FaultKey)
	ta.notifications(nil)
	ta.json("POST", "/api/notifications/test", map[string]any{}, http.StatusBadGateway, nil)
	renew := "Set a new HA_TOKEN in the planner's environment and restart it."
	expectHealth(t, ta.fixes(),
		"Home Assistant: access token rejected (HTTP 401): refused by the demo → "+renew,
		"Phone notifications: 1 phone; the last message failed at 19:00: mobile_app_demo_phone: access token rejected (HTTP 401): refused by the demo → "+renew)
	if c := connection(ta, "Home Assistant"); c.Status != "rejected" {
		t.Errorf("connection = %+v", c)
	}
	ta.fault(demo.FaultHomeAssistant, "")
	ta.json("POST", "/api/notifications/test", map[string]any{}, http.StatusOK, nil)

	// A calendar address that no longer works: recognised from the error
	// saved with the calendar's sync.
	ta.fault(demo.FaultCalendar, demo.FaultKey)
	expectHealth(t, ta.fixes(), "Google Calendar: Demo calendar: calendar address rejected (HTTP 404): refused by the demo → "+
		"Set GOOGLE_CALENDAR_ICAL_URL to the calendar's current secret address in the planner's environment and restart it.")
	ta.fault(demo.FaultCalendar, "")

	// Route lookups.
	ta.fault(demo.FaultRoutes, demo.FaultKey)
	ta.json("POST", "/api/trips", map[string]any{
		"title": "Hardware store", "destination": "Bouwmarkt 5, Demo Town", "date": "2026-11-12", "start": "10:00",
	}, http.StatusCreated, nil)
	ta.plan()
	expectHealth(t, ta.fixes(), "Google Routes: The last lookup failed at 19:00: API key rejected (HTTP 403): refused by the demo; "+
		"trips use a cached route where there is one → Set a new GOOGLE_MAPS_API_KEY in the planner's environment and restart it.")
	ta.fault(demo.FaultRoutes, "")
	ta.plan()
	expectHealth(t, ta.fixes())
}

func TestRejectedOpenRouterKeyInAIMode(t *testing.T) {
	ta := newTestApp(t, "")
	ta.json("POST", "/api/demo/reset", map[string]any{}, http.StatusNoContent, nil)
	ta.tripPlans(model.TripPlansAI)
	ta.useAI(&fakeAI{err: fmt.Errorf("openrouter: %w", &rejected.Error{What: "API key", Status: 401, Message: "No auth credentials found"})})
	ta.json("POST", "/api/ai/run", map[string]any{}, http.StatusOK, nil)
	c := connection(ta, "AI mode")
	if c.Status != "rejected" || !strings.HasSuffix(c.Detail, "the last run failed: openrouter: API key rejected (HTTP 401): No auth credentials found") {
		t.Errorf("connection = %+v", c)
	}
	var st struct{ Health []service.HealthIssue }
	ta.json("GET", "/api/status", nil, http.StatusOK, &st)
	if len(st.Health) != 1 || st.Health[0].Fix != "Set a new OPENROUTER_API_KEY in the planner's environment and restart it." {
		t.Errorf("health = %+v", st.Health)
	}
}

// Forecast sources that refuse their keys.
type refusingWeather struct{}

func (refusingWeather) Name() string { return "live" }
func (refusingWeather) Fetch(context.Context) (*forecast.Weather, error) {
	return nil, fmt.Errorf("knmi: %w", &rejected.Error{What: "API key", Status: 401, Message: "Authorization field missing"})
}

type refusingExtractor struct{}

func (refusingExtractor) Name() string  { return "openrouter" }
func (refusingExtractor) Label() string { return "OpenRouter test/model" }
func (refusingExtractor) Extract(context.Context, forecast.ExtractRequest) (forecast.Extraction, forecast.Usage, error) {
	return forecast.Extraction{}, forecast.Usage{}, fmt.Errorf("openrouter: %w",
		&rejected.Error{What: "API key", Status: 401, Message: "No auth credentials found"})
}

func TestRejectedForecastKeysAreRecognisedFromTheSavedError(t *testing.T) {
	ta := newTestApp(t, "")
	svc := ta.app.Service
	svc.UseIntegrations(func(i *service.Integrations) {
		i.Forecasts.Weather, i.Forecasts.Extractor = refusingWeather{}, refusingExtractor{}
	})
	if err := svc.RefreshForecasts(context.Background(), true); err == nil {
		t.Fatal("want an error")
	}
	for name, env := range map[string]string{"KNMI": "KNMI_API_KEY", "OpenRouter": "OPENROUTER_API_KEY"} {
		if c := connection(ta, name); c.Status != "rejected" ||
			c.Fix != "Set a new "+env+" in the planner's environment and restart it." {
			t.Errorf("%s = %+v", name, c)
		}
	}
	// The outlook itself failed, but its source has no key to renew.
	if tibber := connection(ta, "Tibber outlook"); tibber.Status != "error" ||
		!strings.Contains(tibber.Detail, "API key rejected (HTTP 401)") {
		t.Errorf("Tibber outlook = %+v", tibber)
	}
}
