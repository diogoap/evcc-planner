package service

import (
	"context"
	"time"

	"github.com/diogoap/evcc-planner/internal/advice"
	"github.com/diogoap/evcc-planner/internal/evcc"
	"github.com/diogoap/evcc-planner/internal/localtime"
	"github.com/diogoap/evcc-planner/internal/model"
	"github.com/diogoap/evcc-planner/internal/store"
)

// HistoryView is the saved days in a range, oldest first.
type HistoryView struct {
	Today string           `json:"today"`
	First string           `json:"first,omitempty"` // the earliest saved day
	Days  []model.DayStats `json:"days"`
}

// History returns the saved days from `from` through `to` (local dates,
// inclusive; empty means unbounded).
func (s *Service) History(from, to string) (*HistoryView, error) {
	for _, d := range []string{from, to} {
		if _, err := time.Parse(localtime.DateLayout, d); d != "" && err != nil {
			return nil, invalid("dates must be YYYY-MM-DD")
		}
	}
	days, err := s.Store.DayStats(from, to)
	if err != nil {
		return nil, err
	}
	first, err := s.Store.FirstDayStats()
	if err != nil {
		return nil, err
	}
	return &HistoryView{Today: localtime.Date(s.now(), s.loc()), First: first, Days: days}, nil
}

// The day's figures are saved again from this local time, with the trips
// changed during the day.
const historyEveningHour, historyEveningMinute = 23, 45

// SeedPastWeek saves the seven days before today with the given lowest
// price (and a typical day's spread above it), for demo mode and tests only:
// charge extra compares with past prices before tomorrow's are published.
// Real installs start their history empty.
func (s *Service) SeedPastWeek(low float64) error {
	today := localtime.StartOfDay(s.now(), s.loc())
	return s.Store.InTx(func(tx *store.Store) error {
		for i := 7; i >= 1; i-- {
			day := localtime.AddDays(today, -i)
			end := localtime.AddDays(day, 1)
			if err := tx.SaveDayStats(model.DayStats{
				Date: day.Format(localtime.DateLayout), PriceMin: ptr(low), PriceAvg: ptr(round(low+0.08, 4)),
				PriceMax: ptr(round(low+0.2, 4)), PriceHours: end.Sub(day).Hours(), UpdatedAt: end.Add(-15 * time.Minute),
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

// pastLows is the average lowest price of the up to seven complete days
// before date in the price history (nil without any).
func (s *Service) pastLows(date string) (*advice.History, error) {
	d, err := time.Parse(localtime.DateLayout, date)
	if err != nil {
		return nil, err
	}
	days, err := s.Store.DayStats(d.AddDate(0, 0, -7).Format(localtime.DateLayout), d.AddDate(0, 0, -1).Format(localtime.DateLayout))
	if err != nil {
		return nil, err
	}
	h := &advice.History{}
	for _, day := range days {
		if day.PriceMin != nil && day.PriceHours >= 23 {
			h.LowAverage += *day.PriceMin
			h.Days++
		}
	}
	if h.Days == 0 {
		return nil, nil
	}
	h.LowAverage = round(h.LowAverage/float64(h.Days), 4)
	return h, nil
}

// RecordHistory saves today's figures twice: when the day starts (the first
// check after midnight, or after a start later that day) and again from
// 23:45, with the trips changed during the day. It saves the lowest,
// average, and highest published price; the km of the day's trips (those
// already driven included) and their energy; KNMI's wind and sunshine for
// the day; and, at the first save only, EVCC's solar forecast for the whole
// day (by the evening the day's sun has passed). The evening save keeps the
// first save's prices if EVCC no longer lists the whole day. Past days never change. After a start it
// waits for the first EVCC check, so the prices are loaded. Recording is not
// an action, so it is not in the activity log (like SoC readings).
func (s *Service) RecordHistory(ctx context.Context) error {
	snap := s.snapshot()
	if snap.at.IsZero() {
		return nil
	}
	now := s.now()
	start := localtime.StartOfDay(now, s.loc())
	evening := localtime.OnDay(start, historyEveningHour, historyEveningMinute)
	date := localtime.Date(now, s.loc())
	saved, err := s.Store.DayStat(date)
	if err != nil {
		return err
	}
	if saved != nil && (now.Before(evening) || !saved.UpdatedAt.Before(evening)) {
		return nil // saved already
	}
	view, err := s.Plan(ctx)
	if err != nil {
		return err
	}
	weather, _, err := s.weather()
	if err != nil {
		return err
	}
	var slots []evcc.Rate
	if snap.state != nil {
		slots = snap.state.PriceSlots
	}
	day := model.DayStats{Date: date, UpdatedAt: now}
	day.PriceMin, day.PriceAvg, day.PriceMax, day.PriceHours = dayPrices(slots, start, localtime.AddDays(start, 1))
	dr := view.driving[date]
	day.DistanceKm, day.EnergyKWh = ptr(round(dr.km, 1)), ptr(round(dr.kwh, 2))
	if weather != nil {
		if wd := weather.Day(date); wd != nil {
			day.WindBft, day.SunshinePct = wd.WindBft, wd.SunshinePct
		}
	}
	// The solar forecast as the day starts; a first save after a start
	// later in the day saves none unless the forecast still has the whole
	// day's sun.
	if saved == nil && snap.state != nil {
		if kwh, ok := snap.state.Solar.DayKWh(start, localtime.AddDays(start, 1)); ok {
			day.SolarKWh = ptr(round(kwh, 1))
		}
	}
	return s.Store.SaveDayStats(mergeStats(saved, day))
}

// dayPrices summarizes the published slots in [from, to): the lowest and
// highest slot, the time-weighted average, and the hours they cover.
func dayPrices(slots []evcc.Rate, from, to time.Time) (low, avg, high *float64, hours float64) {
	var sum float64
	for _, r := range slots {
		start, end := maxTime(r.Start, from), minTime(r.End, to)
		if !end.After(start) {
			continue
		}
		h := end.Sub(start).Hours()
		sum += r.Value * h
		hours += h
		if low == nil || r.Value < *low {
			low = ptr(r.Value)
		}
		if high == nil || r.Value > *high {
			high = ptr(r.Value)
		}
	}
	if hours == 0 {
		return nil, nil, nil, 0
	}
	return low, ptr(round(sum/hours, 4)), high, round(hours, 2)
}

// driving is the distance and energy of a day's trips.
type driving struct{ km, kwh float64 }

// drivingByDate adds up the enabled trips of each local date.
func drivingByDate(list []TripView) map[string]driving {
	out := map[string]driving{}
	for _, t := range list {
		if !t.Enabled {
			continue
		}
		d := out[t.Date]
		if t.DistanceKm != nil {
			d.km += *t.DistanceKm
		}
		if t.EnergyKWh != nil {
			d.kwh += *t.EnergyKWh
		}
		out[t.Date] = d
	}
	return out
}

// mergeStats updates a saved day with new figures, keeping what is still
// known: an unknown new value leaves the saved one, and so do prices that
// cover less of the day (EVCC no longer lists the slots already past).
func mergeStats(saved *model.DayStats, d model.DayStats) model.DayStats {
	if saved == nil {
		return d
	}
	if d.PriceHours < saved.PriceHours {
		d.PriceMin, d.PriceAvg, d.PriceMax, d.PriceHours = saved.PriceMin, saved.PriceAvg, saved.PriceMax, saved.PriceHours
	}
	if d.DistanceKm == nil {
		d.DistanceKm, d.EnergyKWh = saved.DistanceKm, saved.EnergyKWh
	}
	if d.WindBft == nil {
		d.WindBft = saved.WindBft
	}
	if d.SunshinePct == nil {
		d.SunshinePct = saved.SunshinePct
	}
	if d.SolarKWh == nil {
		d.SolarKWh = saved.SolarKWh
	}
	return d
}
