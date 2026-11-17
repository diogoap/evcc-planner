package app

import (
	"context"
	"math"
	"net/http"
	"strings"
	"testing"

	"github.com/diogoap/evcc-planner/internal/homeassistant"
	"github.com/diogoap/evcc-planner/internal/model"
	"github.com/diogoap/evcc-planner/internal/service"
)

// efficiencySensor is the demo Home Assistant with a consumption sensor
// whose state a test sets.
type efficiencySensor struct {
	homeassistant.Stub
	state *string
}

func (e efficiencySensor) State(ctx context.Context, entityID string) (*homeassistant.EntityState, error) {
	if entityID != "sensor.car_efficiency" {
		return e.Stub.State(ctx, entityID)
	}
	at := e.Now()
	return &homeassistant.EntityState{EntityID: entityID, State: *e.state, LastReported: at, LastChanged: at}, nil
}

type consumptionView struct {
	KWhPer100Km float64  `json:"kwhPer100Km"`
	Source      string   `json:"source"`
	Entity      string   `json:"entity"`
	Unit        string   `json:"unit"`
	SensorValue *float64 `json:"sensorValue"`
	ObservedAt  *string  `json:"observedAt"`
	Note        string   `json:"note"`
}

func (ta *testApp) consumption() (consumptionView, float64) {
	ta.t.Helper()
	var p struct {
		Consumption consumptionView `json:"consumption"`
		Days        []struct {
			EnergyKWh float64 `json:"energyKWh"`
		} `json:"days"`
	}
	ta.json("GET", "/api/plan", nil, http.StatusOK, &p)
	return p.Consumption, p.Days[1].EnergyKWh // Tuesday: 100 km
}

func TestConsumptionSensorReplacesTheSetValue(t *testing.T) {
	ta := newTestApp(t, "")
	state := "6.4"
	build := ta.app.Service.Build
	ta.app.Service.Build = func(cs model.ConnectionSettings) service.Integrations {
		i := build(cs)
		i.HA = efficiencySensor{Stub: i.HA.(homeassistant.Stub), state: &state}
		return i
	}
	if err := ta.app.Service.LoadSettings(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	poll := func(s string) (consumptionView, float64) {
		t.Helper()
		state = s
		if err := ta.app.Service.PollHomeAssistant(ctx); err != nil {
			t.Fatal(err)
		}
		return ta.consumption()
	}

	// By default the value set by hand: 18 kWh/100 km.
	if c, tue := ta.consumption(); c.Source != "manual" || c.KWhPer100Km != 18 || c.Entity != "" || tue != 18 {
		t.Fatalf("default = %+v, Tuesday %v kWh", c, tue)
	}

	// Choosing a sensor in km/kWh reads it at once and replaces the value.
	var s map[string]any
	ta.json("GET", "/api/settings", nil, http.StatusOK, &s)
	if s["consumptionUnit"] != "kwh_per_100km" || s["consumptionEntityUnit"] != "km_per_kwh" || s["consumptionEntity"] != "" {
		t.Fatalf("default consumption settings = %v", s)
	}
	s["consumptionEntity"] = " sensor.car_efficiency "
	ta.json("PUT", "/api/settings", s, http.StatusOK, &s)
	c, tue := ta.consumption()
	if c.Source != "homeassistant" || c.KWhPer100Km != 15.625 || c.Entity != "sensor.car_efficiency" || c.Unit != "km_per_kwh" ||
		c.SensorValue == nil || *c.SensorValue != 6.4 || c.ObservedAt == nil || c.Note != "" || math.Abs(tue-15.63) > 0.005 {
		t.Fatalf("with the sensor = %+v, Tuesday %v kWh", c, tue)
	}
	// The value set by hand stays in the settings, as the fallback.
	var saved map[string]any
	ta.json("GET", "/api/settings", nil, http.StatusOK, &saved)
	if saved["consumptionKWhPer100Km"] != 18.0 || saved["consumptionEntity"] != "sensor.car_efficiency" {
		t.Fatalf("saved settings = %v", saved)
	}
	if first := ta.activity("").Events[0]; !strings.Contains(first.Summary, "consumption sensor") {
		t.Fatalf("activity = %q", first.Summary)
	}

	// Zero, unavailable, and implausible readings keep the last valid value.
	for st, note := range map[string]string{
		"0":           "sensor.car_efficiency reads 0",
		"unavailable": "sensor.car_efficiency is unavailable",
		"0.5":         "sensor.car_efficiency reads 0.5 km/kWh, outside 1.7 to 20 km/kWh",
	} {
		if c, tue := poll(st); c.KWhPer100Km != 15.625 || c.Source != "homeassistant" || *c.SensorValue != 6.4 ||
			c.Note != note || math.Abs(tue-15.63) > 0.005 {
			t.Errorf("reading %q: %+v, Tuesday %v kWh", st, c, tue)
		}
	}
	if c, _ := poll("5"); c.KWhPer100Km != 20 || c.Note != "" {
		t.Fatalf("a new valid reading = %+v", c)
	}

	// The sensor in kWh/100 km: a saved value that doesn't fit the new unit
	// is not used; the value set by hand is.
	state = "2"
	if c, _ := poll("2"); c.KWhPer100Km != 50 {
		t.Fatalf("2 km/kWh = %+v", c)
	}
	s["consumptionEntityUnit"] = "kwh_per_100km"
	ta.json("PUT", "/api/settings", s, http.StatusOK, &s)
	if c, _ := ta.consumption(); c.Source != "manual" || c.KWhPer100Km != 18 || c.Note != "2 kWh/100 km is outside 5 to 60 kWh/100 km" {
		t.Fatalf("after the unit change = %+v", c)
	}
	if c, _ := poll("16.5"); c.Source != "homeassistant" || c.KWhPer100Km != 16.5 {
		t.Fatalf("16.5 kWh/100 km = %+v", c)
	}

	// Another sensor starts without a reading: the value set by hand, and why.
	s["consumptionEntity"] = "sensor.missing"
	ta.json("PUT", "/api/settings", s, http.StatusOK, &s)
	if c, _ := ta.consumption(); c.Source != "manual" || c.KWhPer100Km != 18 || c.SensorValue != nil ||
		c.Note != "sensor.missing: entity not found" {
		t.Fatalf("an unknown sensor = %+v", c)
	}
	// Clearing it: the value set by hand, without a note.
	s["consumptionEntity"] = ""
	ta.json("PUT", "/api/settings", s, http.StatusOK, &s)
	if c, _ := ta.consumption(); c.Source != "manual" || c.Entity != "" || c.Note != "" {
		t.Fatalf("without a sensor = %+v", c)
	}
}

func TestConsumptionCanBeSetInEitherUnit(t *testing.T) {
	ta := newTestApp(t, "")
	var s map[string]any
	ta.json("GET", "/api/settings", nil, http.StatusOK, &s)
	// Entered as 5 km/kWh, saved as 20 kWh/100 km.
	s["consumptionUnit"], s["consumptionKWhPer100Km"] = "km_per_kwh", 20.0
	ta.json("PUT", "/api/settings", s, http.StatusOK, &s)
	if c, tue := ta.consumption(); c.KWhPer100Km != 20 || tue != 20 {
		t.Fatalf("5 km/kWh = %+v, Tuesday %v kWh", c, tue)
	}
	for _, bad := range []struct {
		unit string
		kwh  float64
		want string
	}{
		{"km_per_kwh", 100.0 / 25, "consumption must be from 1.7 to 20 km/kWh"},
		{"kwh_per_100km", 4, "consumption must be from 5 to 60 kWh/100 km"},
		{"miles", 18, `consumption units must be "kwh_per_100km" or "km_per_kwh"`},
	} {
		s["consumptionUnit"], s["consumptionKWhPer100Km"] = bad.unit, bad.kwh
		var e struct{ Error string }
		ta.json("PUT", "/api/settings", s, http.StatusBadRequest, &e)
		if e.Error != bad.want {
			t.Errorf("%s %v = %q, want %q", bad.unit, bad.kwh, e.Error, bad.want)
		}
	}
	s["consumptionUnit"], s["consumptionKWhPer100Km"], s["consumptionEntity"] = "kwh_per_100km", 18.0, "car efficiency"
	if code, body := ta.do("PUT", "/api/settings", s); code != http.StatusBadRequest || !strings.Contains(string(body), "entity ID") {
		t.Errorf("a sensor name that is no entity ID = %d %s", code, body)
	}
}
