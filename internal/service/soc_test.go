package service

import (
	"testing"
	"time"

	"github.com/diogoap/evcc-planner/internal/model"
)

func TestChooseSoC(t *testing.T) {
	unplugged := time.Date(2026, 11, 10, 7, 0, 0, 0, time.UTC)
	ev := model.SoCReading{Source: model.SoCSourceEVCC, Percent: 80, ObservedAt: unplugged}
	ha := func(pct float64, reported, changed time.Duration) model.SoCReading {
		return model.SoCReading{
			Source: model.SoCSourceHomeAssistant, Percent: pct,
			ObservedAt: unplugged.Add(reported), ChangedAt: unplugged.Add(changed),
		}
	}
	cases := []struct {
		name string
		list []model.SoCReading
		want string // source, or "" for none
	}{
		{"nothing", nil, ""},
		{"only evcc", []model.SoCReading{ev}, model.SoCSourceEVCC},
		{"only home assistant", []model.SoCReading{ha(60, 0, 0)}, model.SoCSourceHomeAssistant},
		{"home assistant changed after unplugging (a drive)", []model.SoCReading{ha(65, time.Hour, 50*time.Minute), ev}, model.SoCSourceHomeAssistant},
		{"same value reported later", []model.SoCReading{ha(80, time.Hour, -time.Hour), ev}, model.SoCSourceHomeAssistant},
		{"lagging value reported again after unplugging", []model.SoCReading{ha(74, time.Hour, -20*time.Minute), ev}, model.SoCSourceEVCC},
		{"reported while still plugged in", []model.SoCReading{ha(74, -time.Minute, -time.Minute), ev}, model.SoCSourceEVCC},
		{"no change time: the report time counts", []model.SoCReading{{Source: model.SoCSourceHomeAssistant, Percent: 70, ObservedAt: unplugged.Add(time.Hour)}, ev}, model.SoCSourceHomeAssistant},
	}
	for _, c := range cases {
		got := chooseSoC(c.list)
		src := ""
		if got != nil {
			src = got.Source
		}
		if src != c.want {
			t.Errorf("%s: chose %q, want %q", c.name, src, c.want)
		}
	}
}
