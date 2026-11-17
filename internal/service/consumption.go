package service

import (
	"context"
	"fmt"
	"time"

	"github.com/diogoap/evcc-planner/internal/homeassistant"
	"github.com/diogoap/evcc-planner/internal/model"
)

// Where the consumption the plan uses comes from (ConsumptionView.Source).
const (
	ConsumptionManual        = "manual"
	ConsumptionHomeAssistant = "homeassistant"
)

// ConsumptionView is the consumption the plan uses, in kWh/100 km, and
// where it comes from: the value set in Settings, or the last valid reading
// of the Home Assistant sensor chosen there.
type ConsumptionView struct {
	KWhPer100Km float64 `json:"kwhPer100Km"`
	Source      string  `json:"source"` // manual or homeassistant
	// The sensor, if one is chosen, the unit it reports in, and its last
	// valid value (in that unit) with when Home Assistant received it.
	Entity      string     `json:"entity,omitempty"`
	Unit        string     `json:"unit,omitempty"`
	SensorValue *float64   `json:"sensorValue,omitempty"`
	ObservedAt  *time.Time `json:"observedAt,omitempty"`
	// Note says why the latest reading was not used, or why the sensor's
	// value is not used at all.
	Note string `json:"note,omitempty"`
}

// consumption returns the consumption the plan uses (kWh/100 km): the
// sensor's last valid value when a sensor is chosen and has one in the
// plausible range for its unit, otherwise the value set in Settings.
func (s *Service) consumption(settings model.Settings) (float64, ConsumptionView, error) {
	v := ConsumptionView{KWhPer100Km: settings.ConsumptionKWhPer100Km, Source: ConsumptionManual}
	if settings.ConsumptionEntity == "" {
		return v.KWhPer100Km, v, nil
	}
	v.Entity, v.Unit = settings.ConsumptionEntity, settings.ConsumptionEntityUnit
	s.mu.RLock()
	if s.consNote.entity == v.Entity {
		v.Note = s.consNote.note
	}
	s.mu.RUnlock()
	r, err := s.Store.SensorReading(v.Entity)
	if err != nil {
		return 0, v, err
	}
	if r == nil {
		if v.Note == "" {
			v.Note = "no reading yet"
		}
		return v.KWhPer100Km, v, nil
	}
	v.SensorValue, v.ObservedAt = &r.Value, &r.ObservedAt
	// A reading saved before the sensor's unit was changed may not fit it.
	kwh := model.KWhPer100Km(r.Value, v.Unit)
	if !model.ConsumptionInRange(kwh) {
		v.Note = fmt.Sprintf("%g %s is outside %s", r.Value, model.ConsumptionUnitText(v.Unit), model.ConsumptionRangeText(v.Unit))
		return v.KWhPer100Km, v, nil
	}
	v.KWhPer100Km, v.Source = round(kwh, 3), ConsumptionHomeAssistant
	return v.KWhPer100Km, v, nil
}

// consumptionNote is why the chosen sensor's latest reading was not used.
type consumptionNote struct{ entity, note string }

// pollConsumption reads the consumption sensor chosen in Settings, if any.
// A valid value is saved with when Home Assistant received it; anything
// else (zero, unavailable, out of range, Home Assistant unreachable) keeps
// the last valid value, and the note says why.
func (s *Service) pollConsumption(ctx context.Context, cs *connState) {
	settings, err := s.Settings()
	if err != nil || settings.ConsumptionEntity == "" {
		return
	}
	entity := settings.ConsumptionEntity
	note := func(n string) {
		s.mu.Lock()
		s.consNote = consumptionNote{entity: entity, note: n}
		s.mu.Unlock()
	}
	l := haLocator(cs)
	if l == nil {
		note("Home Assistant is not set up (Settings → Connections)")
		return
	}
	e, err := l.State(ctx, entity)
	if err != nil {
		note(err.Error())
		return
	}
	v, err := consumptionValue(*e, settings.ConsumptionEntityUnit)
	if err != nil {
		note(err.Error())
		return
	}
	at, now := e.Reported(), s.now()
	if at.IsZero() || at.After(now) {
		at = now
	}
	if err := s.Store.SaveSensorReading(model.SensorReading{Entity: entity, Value: v, ObservedAt: at}); err != nil {
		note(err.Error())
		return
	}
	note("")
}

// consumptionValue checks a consumption sensor's state: a positive number
// in the plausible range for the unit it reports in.
func consumptionValue(e homeassistant.EntityState, unit string) (float64, error) {
	v, err := homeassistant.ParseConsumption(e)
	if err != nil {
		return 0, err
	}
	if !model.ConsumptionInRange(model.KWhPer100Km(v, unit)) {
		return 0, fmt.Errorf("%s reads %g %s, outside %s", e.EntityID, v, model.ConsumptionUnitText(unit),
			model.ConsumptionRangeText(unit))
	}
	return v, nil
}

// haLocator reads single Home Assistant entities: through the SoC reader,
// or the sensor list's client while no SoC sensor is chosen.
func haLocator(cs *connState) homeassistant.Locator {
	if l, ok := cs.HA.(homeassistant.Locator); ok {
		return l
	}
	if l, ok := cs.HASensors.(homeassistant.Locator); ok {
		return l
	}
	return nil
}
