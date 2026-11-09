// Package localtime converts between local wall-clock values (dates and
// HH:MM times in the planner's timezone) and absolute instants, handling
// daylight-saving transitions.
package localtime

import (
	"fmt"
	"time"
)

const (
	DateLayout = "2006-01-02"
	TimeLayout = "15:04"
)

// ParseDate parses a YYYY-MM-DD date as local midnight in loc.
func ParseDate(loc *time.Location, date string) (time.Time, error) {
	d, err := time.ParseInLocation(DateLayout, date, loc)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid date %q, want YYYY-MM-DD", date)
	}
	return d, nil
}

// ParseClock parses an HH:MM time of day.
func ParseClock(hhmm string) (hour, minute int, err error) {
	t, err := time.Parse(TimeLayout, hhmm)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid time %q, want HH:MM", hhmm)
	}
	return t.Hour(), t.Minute(), nil
}

// At returns the instant for a local date and HH:MM time in loc.
//
// A wall time that does not exist (inside the spring-forward gap) is moved
// forward by the size of the gap, matching how a clock would be read that
// morning. An ambiguous wall time (autumn fall-back) resolves to the first
// occurrence.
func At(loc *time.Location, date string, hhmm string) (time.Time, error) {
	d, err := ParseDate(loc, date)
	if err != nil {
		return time.Time{}, err
	}
	h, m, err := ParseClock(hhmm)
	if err != nil {
		return time.Time{}, err
	}
	return OnDay(d, h, m), nil
}

// OnDay returns hour:minute local time on the calendar day of day (in day's
// location), with the DST rules described on At.
func OnDay(day time.Time, hour, minute int) time.Time {
	loc := day.Location()
	y, mo, d := day.Date()
	t := time.Date(y, mo, d, hour, minute, 0, 0, loc)
	if t.Hour() != hour || t.Minute() != minute {
		// Nonexistent wall time: read it with the offset in force before the
		// gap (European transitions happen after midnight), which moves it
		// forward by the gap size.
		_, offset := time.Date(y, mo, d, 0, 0, 0, 0, loc).Zone()
		wall := time.Date(y, mo, d, hour, minute, 0, 0, time.UTC)
		return wall.Add(-time.Duration(offset) * time.Second).In(loc)
	}
	// Ambiguous wall time: Go may pick either offset; prefer the earlier one.
	if earlier := t.Add(-time.Hour); earlier.Hour() == hour && earlier.Minute() == minute && earlier.Day() == d {
		return earlier
	}
	return t
}

// StartOfDay returns local midnight of t's calendar day in loc.
func StartOfDay(t time.Time, loc *time.Location) time.Time {
	y, m, d := t.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, loc)
}

// AddDays returns local midnight n calendar days after day's midnight. Days
// may be 23 or 25 hours long across DST changes.
func AddDays(day time.Time, n int) time.Time {
	y, m, d := day.Date()
	return time.Date(y, m, d+n, 0, 0, 0, 0, day.Location())
}

// Date formats t as a local YYYY-MM-DD date in loc.
func Date(t time.Time, loc *time.Location) string {
	return t.In(loc).Format(DateLayout)
}

// Clock formats t as a local HH:MM time in loc.
func Clock(t time.Time, loc *time.Location) string {
	return t.In(loc).Format(TimeLayout)
}

// ISOWeekday returns 1 (Monday) to 7 (Sunday).
func ISOWeekday(t time.Time) int {
	wd := int(t.Weekday())
	if wd == 0 {
		return 7
	}
	return wd
}
