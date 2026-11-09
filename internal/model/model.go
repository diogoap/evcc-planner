// Package model holds the domain types shared by storage, planning, and the
// HTTP API.
package model

import (
	"fmt"
	"time"
)

// Settings are the personal planning choices edited in the UI. Config files
// provide bootstrap defaults; values saved through the UI take precedence.
type Settings struct {
	HomeAddress            string  `json:"homeAddress"`
	UsableCapacityKWh      float64 `json:"usableCapacityKWh"`
	ChargePowerKW          float64 `json:"chargePowerKW"`
	ConsumptionKWhPer100Km float64 `json:"consumptionKWhPer100Km"`
	ArrivalReservePct      float64 `json:"arrivalReservePct"`
	ChargingEfficiencyPct  float64 `json:"chargingEfficiencyPct"`
	DepartureBufferMin     int     `json:"departureBufferMin"`
	ChargeBufferMin        int     `json:"chargeBufferMin"`
	DefaultStayMin         int     `json:"defaultStayMin"`
}

// Validate reports the first out-of-range field.
func (s Settings) Validate() error {
	checks := []struct {
		name     string
		v, lo, h float64
	}{
		{"usable capacity", s.UsableCapacityKWh, 10, 200},
		{"charging power", s.ChargePowerKW, 1, 50},
		{"consumption", s.ConsumptionKWhPer100Km, 5, 60},
		{"arrival reserve", s.ArrivalReservePct, 0, 60},
		{"charging efficiency", s.ChargingEfficiencyPct, 50, 100},
		{"departure buffer", float64(s.DepartureBufferMin), 0, 240},
		{"charging time buffer", float64(s.ChargeBufferMin), 0, 480},
		{"default stay", float64(s.DefaultStayMin), 0, 24 * 60},
	}
	for _, c := range checks {
		if c.v < c.lo || c.v > c.h {
			return fmt.Errorf("%s must be between %g and %g", c.name, c.lo, c.h)
		}
	}
	return nil
}

// Place is a saved location alias such as "School".
type Place struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Address string `json:"address"`
}

// DateRange is an inclusive range of local dates (YYYY-MM-DD).
type DateRange struct {
	From string `json:"from"`
	To   string `json:"to"`
	Note string `json:"note,omitempty"`
}

// Contains reports whether the local date lies within the range.
func (r DateRange) Contains(date string) bool {
	to := r.To
	if to == "" {
		to = r.From
	}
	return date >= r.From && date <= to
}

// Template is a recurring trip schedule. Start and End are local HH:MM times;
// Start is the arrive-by time and the return leg leaves at End.
type Template struct {
	ID          int64       `json:"id"`
	Title       string      `json:"title"`
	Destination string      `json:"destination"`
	Weekdays    []int       `json:"weekdays"` // ISO: 1 = Monday ... 7 = Sunday
	Start       string      `json:"start"`
	End         string      `json:"end"`
	Enabled     bool        `json:"enabled"`
	Exceptions  []DateRange `json:"exceptions"`
}

// RunsOn reports whether the schedule applies to the ISO weekday.
func (t Template) RunsOn(isoWeekday int) bool {
	for _, d := range t.Weekdays {
		if d == isoWeekday {
			return true
		}
	}
	return false
}

// ExceptionFor returns the exception covering the date, if any.
func (t Template) ExceptionFor(date string) *DateRange {
	for i := range t.Exceptions {
		if t.Exceptions[i].Contains(date) {
			return &t.Exceptions[i]
		}
	}
	return nil
}

// ManualTrip is a one-off trip entered in the UI.
type ManualTrip struct {
	ID                 int64
	Title              string
	Destination        string
	Start              time.Time // arrive-by
	End                time.Time // return leaves
	Enabled            bool
	DistanceOverrideKm *float64
}

// CalendarEvent is a timed event synced from an iCal feed. Key is stable
// across syncs for the same event (or recurring instance).
type CalendarEvent struct {
	Key        string
	CalendarID string
	Calendar   string // display name
	UID        string
	Title      string
	Location   string
	Start      time.Time
	End        time.Time
}

// Override holds local edits to a recurring occurrence or calendar trip.
// Nil fields keep the source value, so source updates still apply to fields
// the user has not changed.
type Override struct {
	TripKey            string
	Title              *string
	Destination        *string
	Start              *time.Time
	End                *time.Time
	Enabled            *bool
	DistanceOverrideKm *float64
	Reviewed           *bool
}

// Fields lists which fields the override changes, for display.
func (o Override) Fields() []string {
	var f []string
	if o.Title != nil {
		f = append(f, "title")
	}
	if o.Destination != nil {
		f = append(f, "destination")
	}
	if o.Start != nil || o.End != nil {
		f = append(f, "time")
	}
	if o.Enabled != nil {
		f = append(f, "enabled")
	}
	if o.DistanceOverrideKm != nil {
		f = append(f, "distance")
	}
	return f
}

// SoCReading is the last state of charge read from EVCC.
type SoCReading struct {
	Percent    float64
	ObservedAt time.Time
	Source     string
}

// PlanWrite records a charging plan the app sent to EVCC.
type PlanWrite struct {
	ID         int64     `json:"id"`
	Vehicle    string    `json:"vehicle"`
	SoC        int       `json:"soc"`
	TargetTime time.Time `json:"targetTime"`
	TripKey    string    `json:"tripKey,omitempty"`
	WrittenAt  time.Time `json:"writtenAt"`
}

// CalendarSyncStatus is the outcome of the latest sync for one calendar.
type CalendarSyncStatus struct {
	CalendarID    string     `json:"calendarId"`
	Name          string     `json:"name"`
	LastAttemptAt *time.Time `json:"lastAttemptAt,omitempty"`
	LastSuccessAt *time.Time `json:"lastSuccessAt,omitempty"`
	LastError     string     `json:"lastError,omitempty"`
	EventCount    int        `json:"eventCount"`
}
