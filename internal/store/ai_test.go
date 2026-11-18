package store

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/diogoap/evcc-planner/internal/model"
)

func TestAIRunsAndSettings(t *testing.T) {
	s := open(t)
	if v, err := s.AISettings(model.DefaultAISettings()); err != nil || v != model.DefaultAISettings() {
		t.Fatalf("defaults = %+v, %v", v, err)
	}
	custom := model.AISettings{MaxRunsPerDay: 1, OnPrices: true, Model: "test/model", Prompt: "{{trips}}"}
	if err := s.SaveAISettings(custom); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.AISettings(model.DefaultAISettings()); v != custom {
		t.Errorf("saved = %+v", v)
	}

	if r, err := s.LatestAIRun(""); err != nil || r != nil {
		t.Fatalf("no runs: %+v, %v", r, err)
	}
	day := func(d, h int) time.Time { return time.Date(2026, 11, d, h, 0, 0, 0, time.UTC) }
	until := day(11, 0)
	runs := []model.AIRun{
		{StartedAt: day(1, 14), FinishedAt: day(1, 14), Trigger: model.AITriggerPrices, Status: model.AIRunOK},
		{StartedAt: day(1, 18), FinishedAt: day(1, 18), Trigger: model.AITriggerPlugIn, Status: model.AIRunFailed, Error: "timeout"},
		{StartedAt: day(10, 14), FinishedAt: day(10, 14), Trigger: model.AITriggerPrices, Status: model.AIRunOK, PricesUntil: &until,
			Trips: map[string]string{"T1": "recurring:1:2026-11-11"}, Prompt: "data", Response: `{"summary":"x"}`,
			Result: json.RawMessage(`{"summary":"x"}`)},
		{StartedAt: day(10, 20), FinishedAt: day(10, 20), Trigger: model.AITriggerManual, Status: model.AIRunFailed},
	}
	for i := range runs {
		saved, err := s.AddAIRun(runs[i])
		if err != nil || saved.ID != int64(i+1) {
			t.Fatalf("add = %+v, %v", saved, err)
		}
	}
	if r, _ := s.LatestAIRun(""); r.ID != 4 {
		t.Errorf("latest = %+v", r)
	}
	ok, err := s.LatestAIRun(model.AIRunOK)
	if err != nil || ok.ID != 3 || !ok.PricesUntil.Equal(until) || ok.Trips["T1"] != "recurring:1:2026-11-11" ||
		string(ok.Result) != `{"summary":"x"}` {
		t.Errorf("latest good = %+v, %v", ok, err)
	}
	if n, _ := s.AIRunsSince(day(10, 0)); n != 2 {
		t.Errorf("runs since = %d", n)
	}
	if _, err := s.AIRun(9); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown run = %v", err)
	}

	// Clean-up keeps the latest good run, however old.
	deleted, err := s.Cleanup(day(20, 0), time.UTC)
	if err != nil || deleted["ai_runs"] != 3 {
		t.Fatalf("cleanup = %v, %v", deleted, err)
	}
	if r, _ := s.LatestAIRun(""); r == nil || r.ID != 3 {
		t.Errorf("kept = %+v", r)
	}
}
