package homeassistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/diogoap/evcc-planner/internal/rejected"
)

func TestSetClimateCallsTheClimateServices(t *testing.T) {
	var calls []string
	fail := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("request %s %s, content type %q", r.Method, r.URL.Path, r.Header.Get("Content-Type"))
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		raw, _ := json.Marshal(body)
		calls = append(calls, r.URL.Path+" "+string(raw))
		if r.URL.Path == fail {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"message":"Service call failed"}`))
			return
		}
		w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	c := &HTTPClient{BaseURL: srv.URL + "/", Token: "token", HTTP: srv.Client()}
	ctx := context.Background()

	if err := c.SetClimate(ctx, "climate.car_climate", 20, "heat_cool"); err != nil {
		t.Fatal(err)
	}
	want := `/api/services/climate/set_temperature {"entity_id":"climate.car_climate","temperature":20},` +
		`/api/services/climate/set_hvac_mode {"entity_id":"climate.car_climate","hvac_mode":"heat_cool"}`
	if got := strings.Join(calls, ","); got != want {
		t.Errorf("calls =\n%s\nwant\n%s", got, want)
	}

	// The temperature fails: the climate is not turned on.
	calls, fail = nil, "/api/services/climate/set_temperature"
	err := c.SetClimate(ctx, "climate.car_climate", 20, "heat_cool")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 500 || !strings.HasPrefix(err.Error(), "set the temperature of climate.car_climate: ") ||
		len(calls) != 1 {
		t.Errorf("failed temperature: %v, calls %v", err, calls)
	}
	calls, fail = nil, "/api/services/climate/set_hvac_mode"
	if err := c.SetClimate(ctx, "climate.car_climate", 20, "heat_cool"); err == nil ||
		!strings.HasPrefix(err.Error(), "turn on climate.car_climate: ") || len(calls) != 2 {
		t.Errorf("failed mode: %v, calls %v", err, calls)
	}

	c.Token = "revoked"
	if err := c.SetClimate(ctx, "climate.car_climate", 20, "heat_cool"); !rejected.Is(err) {
		t.Errorf("revoked token: %v", err)
	}
}

func TestParseClimate(t *testing.T) {
	e := EntityState{EntityID: "climate.car_climate", State: "off", Attributes: map[string]any{
		"hvac_modes": []any{"heat_cool", "off"}, "min_temp": 16.0, "max_temp": 26.0, "target_temp_step": 1.0,
		"temperature": 16.0, "current_temperature": 10.0, "hvac_action": "off",
	}}
	c, err := ParseClimate(e)
	if err != nil || c.On() || c.Action != "off" || c.Min != 16 || c.Max != 26 || c.Step != 1 || *c.Target != 16 || *c.Current != 10 ||
		strings.Join(c.Modes, ",") != "heat_cool,off" {
		t.Fatalf("climate = %+v, %v", c, err)
	}
	if mode, ok := c.HeatMode(); !ok || mode != "heat_cool" {
		t.Errorf("heat mode = %q, %v", mode, ok)
	}
	for in, want := range map[float64]float64{20: 20, 30: 26, 15: 16, 20.4: 20, 20.6: 21} {
		if got := c.Clamp(in); got != want {
			t.Errorf("clamp %g = %g, want %g", in, got, want)
		}
	}
	// Without a range or step: steps of 0.5, no bounds.
	if got := (Climate{}).Clamp(20.3); got != 20.5 {
		t.Errorf("clamp without attributes = %g", got)
	}
	if mode, _ := (Climate{Modes: []string{"auto", "heat", "off"}}).HeatMode(); mode != "heat" {
		t.Errorf("heat preferred, got %q", mode)
	}
	if _, ok := (Climate{Modes: []string{"cool", "off"}}).HeatMode(); ok {
		t.Error("a climate that only cools has no heat mode")
	}
	e.State = "heat_cool"
	if c, _ := ParseClimate(e); !c.On() {
		t.Error("heat_cool is on")
	}
	e.State = "unavailable"
	if _, err := ParseClimate(e); err == nil || err.Error() != "climate.car_climate is unavailable" {
		t.Errorf("unavailable: %v", err)
	}
}

func TestParseTemperature(t *testing.T) {
	e := func(state, unit string) EntityState {
		return EntityState{EntityID: "sensor.outside_temperature", State: state, Attributes: map[string]any{"unit_of_measurement": unit}}
	}
	for _, c := range []struct {
		state, unit string
		want        float64
	}{{"1.5", "°C", 1.5}, {"-4", "°C", -4}, {"32", "°F", 0}, {"7", "", 7}} {
		if got, err := ParseTemperature(e(c.state, c.unit)); err != nil || got != c.want {
			t.Errorf("%s %s = %g, %v", c.state, c.unit, got, err)
		}
	}
	for _, c := range []struct{ state, unit, want string }{
		{"unavailable", "°C", "sensor.outside_temperature is unavailable"},
		{"", "°C", "sensor.outside_temperature is empty"},
		{"cold", "°C", `sensor.outside_temperature is "cold", not a number`},
		{"280", "K", "sensor.outside_temperature is in K, not °C or °F"},
		{"99", "°C", "sensor.outside_temperature reads 99 °C, treated as unknown"},
	} {
		if _, err := ParseTemperature(e(c.state, c.unit)); err == nil || err.Error() != c.want {
			t.Errorf("%q %s: %v, want %q", c.state, c.unit, err, c.want)
		}
	}
}

func TestClimateStub(t *testing.T) {
	now := time.Date(2026, 11, 10, 7, 33, 0, 0, time.UTC)
	s := NewClimateStub(func() time.Time { return now })
	ctx := context.Background()

	e, err := s.State(ctx, StubClimateEntity)
	if err != nil {
		t.Fatal(err)
	}
	c, err := ParseClimate(*e)
	if err != nil || c.On() || c.Min != 16 || c.Max != 26 || *c.Target != 18 {
		t.Fatalf("climate = %+v, %v", c, err)
	}
	o, _ := s.State(ctx, StubOutsideEntity)
	if v, err := ParseTemperature(*o); err != nil || v != 2 || !o.Reported().Equal(now.Add(-5*time.Minute)) {
		t.Fatalf("outside = %+v (%g, %v)", o, v, err)
	}
	if _, err := s.State(ctx, "sensor.other"); !errors.Is(err, ErrNotFound) {
		t.Errorf("other entity: %v", err)
	}

	if err := s.SetClimate(ctx, StubClimateEntity, 20, "heat"); err == nil {
		t.Error("the demo climate has no heat mode")
	}
	if err := s.SetClimate(ctx, StubClimateEntity, 20, "heat_cool"); err != nil {
		t.Fatal(err)
	}
	if calls := s.Take(); len(calls) != 1 || calls[0] != (ClimateCall{Entity: StubClimateEntity, Temp: 20, Mode: "heat_cool"}) {
		t.Errorf("calls = %+v", calls)
	}
	if e, _ := s.State(ctx, StubClimateEntity); e.State != "heat_cool" || e.Attributes["temperature"] != 20.0 ||
		e.Attributes["hvac_action"] != "heating" {
		t.Errorf("after turning on = %+v", e)
	}
	s.SetState("cool")
	if e, _ := s.State(ctx, StubClimateEntity); e.Attributes["hvac_action"] != "cooling" {
		t.Errorf("cooling = %+v", e)
	}

	s.SetOutside(nil, time.Time{})
	if o, _ := s.State(ctx, StubOutsideEntity); o.State != "unavailable" {
		t.Errorf("outside unset = %+v", o)
	}
	s.SetFail(errors.New("down"))
	if _, err := s.State(ctx, StubClimateEntity); err == nil {
		t.Error("a failing stub reads nothing")
	}
	s.SetFail(nil)
	s.Reset()
	if e, _ := s.State(ctx, StubClimateEntity); e.State != "off" || len(s.Take()) != 0 {
		t.Errorf("after reset = %+v", e)
	}
}
