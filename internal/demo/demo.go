// Package demo seeds editable sample data for local development.
package demo

import (
	"time"

	"github.com/diogoap/evcc-planner/internal/homeassistant"
	"github.com/diogoap/evcc-planner/internal/localtime"
	"github.com/diogoap/evcc-planner/internal/model"
	"github.com/diogoap/evcc-planner/internal/store"
	"github.com/diogoap/evcc-planner/internal/trips"
)

// Seed fills an empty demo database: home and places, school-run schedules,
// a disabled manual trip, a skipped school day, a SoC reading from earlier
// today (the demo car is unplugged), and the car location sensor.
func Seed(s *store.Store, now time.Time, loc *time.Location, defaults model.Settings) error {
	empty, err := s.IsEmpty()
	if err != nil || !empty {
		return err
	}
	settings := defaults
	settings.HomeAddress = "Demo Home 1, Demo Town"
	if err := s.SaveSettings(settings); err != nil {
		return err
	}
	// The demo car's location comes from the Home Assistant stub.
	notify := model.DefaultNotificationSettings()
	notify.LocationEntity = homeassistant.StubLocationEntity
	if err := s.SaveNotificationSettings(notify); err != nil {
		return err
	}
	for _, p := range []model.Place{
		{Name: "School", Address: "Schoolstraat 1, Demo Town"},
		{Name: "Office", Address: "Office Park 38, Demo City"},
	} {
		if _, err := s.CreatePlace(p); err != nil {
			return err
		}
	}
	weekdays := []int{1, 2, 3, 4, 5}
	dropoff, err := s.CreateTemplate(model.Template{
		Title: "School drop-off", Destination: "School", Weekdays: weekdays, Start: "08:15", End: "08:20", Enabled: true,
	})
	if err != nil {
		return err
	}
	if _, err := s.CreateTemplate(model.Template{
		Title: "School pickup", Destination: "School", Weekdays: weekdays, Start: "15:00", End: "15:15", Enabled: true,
	}); err != nil {
		return err
	}

	today := localtime.StartOfDay(now, loc)
	tomorrow := localtime.AddDays(today, 1)
	if _, err := s.CreateManualTrip(model.ManualTrip{
		Title: "Optional evening visit", Destination: "Parkweg 20, Demo Town",
		Start: localtime.OnDay(tomorrow, 17, 30), End: localtime.OnDay(tomorrow, 19, 0), Enabled: false,
	}); err != nil {
		return err
	}
	for i := 2; i < 7; i++ {
		day := localtime.AddDays(today, i)
		if wd := localtime.ISOWeekday(day); wd <= 5 {
			off := false
			if err := s.SaveOverride(model.Override{
				TripKey: trips.RecurringKey(dropoff.ID, day.Format(localtime.DateLayout)), Enabled: &off,
			}); err != nil {
				return err
			}
			break
		}
	}
	rangeKm := 122.0
	return s.SaveSoC(model.SoCReading{Percent: 38, RangeKm: &rangeKm, ObservedAt: now.Add(-135 * time.Minute), Source: model.SoCSourceEVCC})
}
