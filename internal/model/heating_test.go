package model

import (
	"strings"
	"testing"
)

func TestHeatingSettingsValidate(t *testing.T) {
	d := DefaultHeatingSettings()
	if err := d.Validate(); err != nil || d.Enabled || d.BelowC != 3 || d.LeadMin != 15 || d.TargetC != 20 || d.AskPlugged || !d.AskUnplugged {
		t.Fatalf("defaults = %+v, %v", d, err)
	}
	on := d
	on.Enabled, on.ClimateEntity, on.OutsideEntity = true, "climate.car_climate", "sensor.outside_temperature"
	if err := on.Validate(); err != nil {
		t.Fatalf("valid settings: %v", err)
	}
	for name, c := range map[string]struct {
		edit func(*HeatingSettings)
		want string
	}{
		"too cold":       {func(h *HeatingSettings) { h.BelowC = -21 }, "heat below must be between -20 and 15"},
		"too warm":       {func(h *HeatingSettings) { h.BelowC = 16 }, "heat below must be between -20 and 15"},
		"lead":           {func(h *HeatingSettings) { h.LeadMin = 4 }, "minutes before departure must be between 5 and 60"},
		"target":         {func(h *HeatingSettings) { h.TargetC = 31 }, "target temperature must be between 10 and 30"},
		"climate syntax": {func(h *HeatingSettings) { h.ClimateEntity = "Car climate" }, "must be a climate entity"},
		"climate domain": {func(h *HeatingSettings) { h.ClimateEntity = "switch.car_climate" }, "must be a climate entity"},
		"outside domain": {func(h *HeatingSettings) { h.OutsideEntity = "weather.home" }, "outside temperature must be a sensor"},
		"on, no climate": {func(h *HeatingSettings) { h.ClimateEntity = "" }, "choose the car's climate entity"},
	} {
		h := on
		c.edit(&h)
		if err := h.Validate(); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
	}
	// No outside sensor is fine: the forecast stands in.
	on.OutsideEntity = ""
	if err := on.Validate(); err != nil {
		t.Errorf("without an outside sensor: %v", err)
	}
}

func TestHeatingRunFinal(t *testing.T) {
	for _, c := range []struct {
		run  HeatingRun
		want bool
	}{
		{HeatingRun{Outcome: HeatHeated}, true},
		{HeatingRun{Outcome: HeatAsked}, false},
		{HeatingRun{Outcome: HeatFailed, Retry: true}, false},
		{HeatingRun{Outcome: HeatFailed}, true},
		{HeatingRun{Outcome: HeatWarm}, true},
	} {
		if got := c.run.Final(); got != c.want {
			t.Errorf("%+v final = %v", c.run, got)
		}
	}
}
