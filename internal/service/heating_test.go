package service

import (
	"errors"
	"testing"

	"github.com/diogoap/evcc-planner/internal/homeassistant"
	"github.com/diogoap/evcc-planner/internal/model"
)

func TestDecideHeating(t *testing.T) {
	cold, mild := -1.0, 3.0
	yes, no := true, false
	off := &homeassistant.Climate{EntityID: "climate.car_climate", State: "off", Min: 16, Max: 26, Step: 1, Modes: []string{"heat_cool", "off"}}
	on := &homeassistant.Climate{EntityID: "climate.car_climate", State: "heat_cool", Modes: off.Modes}
	coolOnly := &homeassistant.Climate{EntityID: "climate.car_climate", State: "off", Modes: []string{"cool", "off"}}
	base := func() heatInput {
		return heatInput{settings: model.HeatingSettings{BelowC: 3, TargetC: 30, AskUnplugged: true}, outside: &cold,
			climate: off, plugged: &yes}
	}
	for _, c := range []struct {
		name string
		edit func(*heatInput)
		want heatDecision
	}{
		{"heats while plugged in", func(in *heatInput) {}, heatDecision{outcome: model.HeatHeated, mode: "heat_cool", target: 26}},
		{"asks while unplugged", func(in *heatInput) { in.plugged = &no },
			heatDecision{outcome: model.HeatAsked, mode: "heat_cool", target: 26}},
		{"asks when EVCC can't tell", func(in *heatInput) { in.plugged = nil },
			heatDecision{outcome: model.HeatAsked, mode: "heat_cool", target: 26}},
		{"asks while plugged in when set", func(in *heatInput) { in.settings.AskPlugged = true },
			heatDecision{outcome: model.HeatAsked, mode: "heat_cool", target: 26}},
		{"can't ask", func(in *heatInput) { in.plugged, in.noAsk = &no, "notifications are off" },
			heatDecision{outcome: model.HeatSkipped, reason: "it asks first while unplugged, but notifications are off"}},
		{"at the threshold it is warm enough", func(in *heatInput) { in.outside = &mild }, heatDecision{outcome: model.HeatWarm}},
		{"away", func(in *heatInput) { in.away = "the car isn't home" },
			heatDecision{outcome: model.HeatSkipped, reason: "the car isn't home"}},
		{"no temperature", func(in *heatInput) { in.outside, in.noTemp = nil, "no outside temperature" },
			heatDecision{outcome: model.HeatSkipped, reason: "no outside temperature"}},
		{"warm before reading the climate", func(in *heatInput) { in.outside, in.climate, in.climateErr = &mild, nil, errors.New("down") },
			heatDecision{outcome: model.HeatWarm}},
		{"climate unreadable", func(in *heatInput) { in.climate, in.climateErr = nil, errors.New("down") },
			heatDecision{outcome: model.HeatFailed, reason: "couldn't read the car's climate: down", retry: true}},
		{"already on", func(in *heatInput) { in.climate = on },
			heatDecision{outcome: model.HeatSkipped, reason: "the climate is already on (heat_cool)"}},
		{"no heating mode", func(in *heatInput) { in.climate = coolOnly },
			heatDecision{outcome: model.HeatFailed, reason: "climate.car_climate has no heating mode (it offers cool, off)"}},
	} {
		in := base()
		c.edit(&in)
		if got := decideHeating(in); got != c.want {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestTempText(t *testing.T) {
	for v, want := range map[float64]string{2: "2 °C", 2.64: "2.6 °C", -0.04: "0 °C", -4.5: "-4.5 °C"} {
		if got := tempText(v); got != want {
			t.Errorf("%g = %q, want %q", v, got, want)
		}
	}
}
