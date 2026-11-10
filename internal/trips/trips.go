// Package trips combines manual trips, recurring schedules, and calendar
// events into one dated trip list, applying local edits.
//
// Recurring occurrences are materialized on demand for the rolling window
// rather than stored: a template plus per-date overrides fully determines
// each occurrence, so the window extends automatically each day, template
// edits reach every field a date has not overridden, and date-level edits
// survive template changes.
package trips

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/diogoap/evcc-planner/internal/calendar"
	"github.com/diogoap/evcc-planner/internal/localtime"
	"github.com/diogoap/evcc-planner/internal/model"
)

// Source of a trip.
type Source string

const (
	SourceManual    Source = "manual"
	SourceRecurring Source = "recurring"
	SourceCalendar  Source = "calendar"
)

// Trip is one dated journey: leave home, arrive by Start, stay until End,
// then return home.
type Trip struct {
	Key                string
	Source             Source
	SourceLabel        string
	SourceID           int64
	Title              string
	Destination        string // as entered or from the calendar
	Address            string // resolved for routing
	PlaceName          string // saved place, when the destination is one
	Start              time.Time
	End                time.Time
	Enabled            bool
	NeedsReview        bool
	DistanceOverrideKm *float64
	Overridden         []string
}

// Ignored is a calendar event that is not a car trip.
type Ignored struct {
	Key      string
	Calendar string
	Title    string
	Location string
	Reason   string
	Start    time.Time
	End      time.Time
}

// Input is everything Build needs.
type Input struct {
	Location  *time.Location
	From, To  time.Time
	Home      string
	Places    []model.Place
	Manual    []model.ManualTrip
	Templates []model.Template
	Events    []model.CalendarEvent
	Overrides map[string]model.Override
}

// Build returns trips overlapping [From, To) sorted by arrival, plus calendar
// events that were ignored and why.
func Build(in Input) ([]Trip, []Ignored) {
	var list []Trip
	for _, m := range in.Manual {
		t := Trip{
			Key: ManualKey(m.ID), Source: SourceManual, SourceLabel: "Manual", SourceID: m.ID,
			Title: m.Title, Destination: m.Destination, Start: m.Start, End: m.End,
			Enabled: m.Enabled, DistanceOverrideKm: m.DistanceOverrideKm,
		}
		list = append(list, t)
	}

	for _, tpl := range in.Templates {
		if !tpl.Enabled {
			continue
		}
		for _, occ := range Occurrences(tpl, in.Location, in.From, in.To) {
			if occ.Exception != nil {
				continue
			}
			t := Trip{
				Key: RecurringKey(tpl.ID, occ.Date), Source: SourceRecurring, SourceLabel: tpl.Title, SourceID: tpl.ID,
				Title: tpl.Title, Destination: tpl.Destination, Start: occ.Start, End: occ.End, Enabled: true,
			}
			list = append(list, applyOverride(t, in.Overrides))
		}
	}

	var ignored []Ignored
	for _, ev := range in.Events {
		c := calendar.Classify(ev.Location, in.Home, in.Places)
		key := CalendarKey(ev.Key)
		if !c.Trip {
			ignored = append(ignored, Ignored{
				Key: key, Calendar: ev.Calendar, Title: ev.Title, Location: ev.Location,
				Reason: c.Reason, Start: ev.Start, End: ev.End,
			})
			continue
		}
		title := ev.Title
		if title == "" {
			title = "Calendar event"
		}
		t := Trip{
			Key: key, Source: SourceCalendar, SourceLabel: ev.Calendar, Title: title,
			Destination: ev.Location, Start: ev.Start, End: ev.End, Enabled: true, NeedsReview: c.NeedsReview,
		}
		list = append(list, applyOverride(t, in.Overrides))
	}

	out := list[:0]
	for _, t := range list {
		if t.End.Before(t.Start) {
			t.End = t.Start
		}
		if t.Start.Before(in.To) && !t.End.Before(in.From) {
			t.Address, t.PlaceName = ResolveDestination(t.Destination, in.Places)
			out = append(out, t)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].Start.Equal(out[j].Start) {
			return out[i].Start.Before(out[j].Start)
		}
		return out[i].Key < out[j].Key
	})
	sort.SliceStable(ignored, func(i, j int) bool { return ignored[i].Start.Before(ignored[j].Start) })
	return out, ignored
}

func applyOverride(t Trip, overrides map[string]model.Override) Trip {
	o, ok := overrides[t.Key]
	if !ok {
		return t
	}
	return ApplyOverride(t, o)
}

// ApplyOverride returns the trip with a local edit applied.
func ApplyOverride(t Trip, o model.Override) Trip {
	if o.Title != nil {
		t.Title = *o.Title
	}
	if o.Destination != nil {
		t.Destination = *o.Destination
	}
	if o.Start != nil {
		t.Start = *o.Start
	}
	if o.End != nil {
		t.End = *o.End
	}
	if o.Enabled != nil {
		t.Enabled = *o.Enabled
	}
	if o.DistanceOverrideKm != nil {
		t.DistanceOverrideKm = o.DistanceOverrideKm
	}
	if o.Reviewed != nil && *o.Reviewed {
		t.NeedsReview = false
	}
	t.Overridden = o.Fields()
	return t
}

// Occurrence is one date of a recurring schedule.
type Occurrence struct {
	Date      string
	Start     time.Time
	End       time.Time
	Exception *model.DateRange
}

// Occurrences lists the schedule's dates whose local day starts in
// [from, to), including dates skipped by an exception.
func Occurrences(t model.Template, loc *time.Location, from, to time.Time) []Occurrence {
	sh, sm, err := localtime.ParseClock(t.Start)
	if err != nil {
		return nil
	}
	eh, em, err := localtime.ParseClock(t.End)
	if err != nil {
		return nil
	}
	var out []Occurrence
	for day := localtime.StartOfDay(from, loc); day.Before(to); day = localtime.AddDays(day, 1) {
		if !t.RunsOn(localtime.ISOWeekday(day)) {
			continue
		}
		start := localtime.OnDay(day, sh, sm)
		end := localtime.OnDay(day, eh, em)
		if end.Before(start) {
			end = localtime.OnDay(localtime.AddDays(day, 1), eh, em)
		}
		date := day.Format(localtime.DateLayout)
		out = append(out, Occurrence{Date: date, Start: start, End: end, Exception: t.ExceptionFor(date)})
	}
	return out
}

// ResolveDestination maps a saved place name to its address.
func ResolveDestination(dest string, places []model.Place) (address, placeName string) {
	d := strings.TrimSpace(dest)
	for _, p := range places {
		if strings.EqualFold(d, strings.TrimSpace(p.Name)) {
			return p.Address, p.Name
		}
	}
	return d, ""
}

// Trip keys identify trips across requests; overrides are stored by key.

func ManualKey(id int64) string { return fmt.Sprintf("manual:%d", id) }

func RecurringKey(templateID int64, date string) string {
	return fmt.Sprintf("recurring:%d:%s", templateID, date)
}

func CalendarKey(eventKey string) string { return "calendar:" + eventKey }

// Key is a parsed trip key.
type Key struct {
	Source Source
	ID     int64  // manual trip or template ID
	Date   string // recurring occurrence date
	Event  string // calendar event key
}

// ParseKey parses a trip key.
func ParseKey(s string) (Key, error) {
	parts := strings.Split(s, ":")
	bad := fmt.Errorf("invalid trip key %q", s)
	switch {
	case len(parts) == 2 && parts[0] == string(SourceManual):
		id, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return Key{}, bad
		}
		return Key{Source: SourceManual, ID: id}, nil
	case len(parts) == 3 && parts[0] == string(SourceRecurring):
		id, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return Key{}, bad
		}
		if _, err := time.Parse(localtime.DateLayout, parts[2]); err != nil {
			return Key{}, bad
		}
		return Key{Source: SourceRecurring, ID: id, Date: parts[2]}, nil
	case len(parts) == 2 && parts[0] == string(SourceCalendar) && parts[1] != "":
		return Key{Source: SourceCalendar, Event: parts[1]}, nil
	}
	return Key{}, bad
}
