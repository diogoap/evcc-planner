package service

import (
	"testing"
	"time"

	"github.com/diogoap/evcc-planner/internal/config"
	"github.com/diogoap/evcc-planner/internal/model"
)

// The evening reminder's night runs from the last evening time (19:30)
// until quiet hours end (07:00), also across daylight saving changes.
func TestTonight(t *testing.T) {
	ams, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		t.Fatal(err)
	}
	at := func(mo time.Month, d, h, m int) time.Time { return time.Date(2026, mo, d, h, m, 0, 0, ams) }
	nov := func(d, h, m int) time.Time { return at(time.November, d, h, m) }
	cases := []struct {
		name             string
		now, from, until time.Time
	}{
		{"at the evening time", nov(9, 19, 30), nov(9, 19, 30), nov(10, 7, 0)},
		{"later in the evening", nov(9, 22, 0), nov(9, 19, 30), nov(10, 7, 0)},
		{"after midnight", nov(10, 1, 0), nov(9, 19, 30), nov(10, 7, 0)},
		{"before the evening time: the night before", nov(9, 19, 29), nov(8, 19, 30), nov(9, 7, 0)},
		{"night the clocks go back", at(time.October, 24, 21, 0), at(time.October, 24, 19, 30), at(time.October, 25, 7, 0)},
		{"night the clocks go forward", at(time.March, 28, 21, 0), at(time.March, 28, 19, 30), at(time.March, 29, 7, 0)},
	}
	for _, c := range cases {
		r := &reminders{s: &Service{Cfg: &config.Config{Location: ams}}, settings: model.DefaultNotificationSettings(), now: c.now}
		if from, until := r.tonight(); !from.Equal(c.from) || !until.Equal(c.until) {
			t.Errorf("%s: %s–%s, want %s–%s", c.name, from, until, c.from, c.until)
		}
	}
}
