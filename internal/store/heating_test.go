package store

import (
	"testing"
	"time"

	"github.com/diogoap/evcc-planner/internal/model"
)

func TestHeatingSettingsAndRuns(t *testing.T) {
	s := open(t)
	if v, err := s.HeatingSettings(model.DefaultHeatingSettings()); err != nil || v != model.DefaultHeatingSettings() {
		t.Fatalf("defaults = %+v, %v", v, err)
	}
	custom := model.DefaultHeatingSettings()
	custom.Enabled, custom.ClimateEntity, custom.BelowC = true, "climate.car_climate", -1.5
	if err := s.SaveHeatingSettings(custom); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.HeatingSettings(model.DefaultHeatingSettings()); v != custom {
		t.Errorf("saved = %+v", v)
	}

	if r, err := s.HeatingRunByKey("heat:manual:1"); err != nil || r != nil {
		t.Fatalf("no run: %+v, %v", r, err)
	}
	day := func(d, h int) time.Time { return time.Date(2026, 11, d, h, 0, 0, 0, time.UTC) }
	one := 1.5
	asked, err := s.SaveHeatingRun(model.HeatingRun{
		Key: "heat:manual:1", TripKey: "manual:1", Title: "Office", DepartAt: day(10, 7), DecidedAt: day(10, 6),
		Outcome: model.HeatAsked, OutsideC: &one, OutsideSource: "sensor", BelowC: 3, TargetC: 20,
	})
	if err != nil || asked.ID == 0 {
		t.Fatalf("save = %+v, %v", asked, err)
	}
	if _, err := s.SaveHeatingRun(model.HeatingRun{Key: "heat:manual:1", DepartAt: day(10, 7)}); err == nil {
		t.Error("a second decision for the same departure must fail")
	}
	s.SaveHeatingRun(model.HeatingRun{Key: "heat:manual:2", DepartAt: day(1, 7), Outcome: model.HeatWarm})
	if list, _ := s.AskedHeatingRuns(); len(list) != 1 || list[0].ID != asked.ID || *list[0].OutsideC != 1.5 {
		t.Errorf("asked = %+v", list)
	}
	asked.Outcome = model.HeatConfirmed
	if _, err := s.SaveHeatingRun(asked); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.AskedHeatingRuns(); len(list) != 0 {
		t.Errorf("asked after confirming = %+v", list)
	}
	recent, err := s.RecentHeatingRuns(5)
	if err != nil || len(recent) != 2 || recent[0].Key != "heat:manual:1" || recent[0].Outcome != model.HeatConfirmed {
		t.Errorf("recent = %+v, %v", recent, err)
	}

	// Clean-up deletes decisions for departures before the cutoff.
	deleted, err := s.Cleanup(day(5, 0), time.UTC)
	if err != nil || deleted["heating_runs"] != 1 {
		t.Errorf("cleanup = %v, %v", deleted, err)
	}
	if err := s.Reset(); err != nil {
		t.Fatal(err)
	}
	if recent, _ := s.RecentHeatingRuns(5); len(recent) != 0 {
		t.Errorf("after reset = %+v", recent)
	}
}
