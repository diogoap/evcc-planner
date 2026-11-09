package service

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/diogoap/evcc-planner/internal/localtime"
	"github.com/diogoap/evcc-planner/internal/model"
	"github.com/diogoap/evcc-planner/internal/trips"
)

// NullableFloat distinguishes an absent JSON field from an explicit null
// (which clears a manual distance).
type NullableFloat struct {
	Set   bool
	Value *float64
}

// UnmarshalJSON records that the field was present.
func (n *NullableFloat) UnmarshalJSON(b []byte) error {
	n.Set = true
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		n.Value = nil
		return nil
	}
	var v float64
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	n.Value = &v
	return nil
}

// TripInput creates or edits a trip. Times are local: Start is the arrive-by
// time and the return leaves at End (the next day if End is before Start).
type TripInput struct {
	Title              *string       `json:"title"`
	Destination        *string       `json:"destination"`
	Date               *string       `json:"date"`
	Start              *string       `json:"start"`
	End                *string       `json:"end"`
	Enabled            *bool         `json:"enabled"`
	DistanceOverrideKm NullableFloat `json:"distanceOverrideKm"`
	Reviewed           *bool         `json:"reviewed"`
}

func trimmed(p *string) string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(*p)
}

func validDistance(n NullableFloat) error {
	if n.Value != nil && (*n.Value <= 0 || *n.Value > 2000) {
		return invalid("distance must be between 0 and 2000 km")
	}
	return nil
}

// times resolves date/start/end into instants, defaulting missing parts from
// the current values.
func (s *Service) times(in TripInput, curStart, curEnd time.Time, stay time.Duration) (time.Time, time.Time, error) {
	loc := s.loc()
	date := localtime.Date(curStart, loc)
	start := localtime.Clock(curStart, loc)
	if in.Date != nil {
		date = *in.Date
	}
	if in.Start != nil {
		start = *in.Start
	}
	startAt, err := localtime.At(loc, date, start)
	if err != nil {
		return time.Time{}, time.Time{}, ValidationError{Msg: err.Error()}
	}
	var endAt time.Time
	switch {
	case in.End != nil:
		endAt, err = localtime.At(loc, date, *in.End)
		if err != nil {
			return time.Time{}, time.Time{}, ValidationError{Msg: err.Error()}
		}
		if endAt.Before(startAt) {
			endAt, _ = localtime.At(loc, localtime.Date(localtime.AddDays(localtime.StartOfDay(startAt, loc), 1), loc), *in.End)
		}
	case curEnd.IsZero():
		endAt = startAt.Add(stay)
	default:
		// Keep the stay length when only the date or start time moves.
		endAt = startAt.Add(curEnd.Sub(curStart))
	}
	return startAt, endAt, nil
}

// CreateTrip adds a manual trip and returns its key.
func (s *Service) CreateTrip(in TripInput) (string, error) {
	settings, err := s.Settings()
	if err != nil {
		return "", err
	}
	dest := trimmed(in.Destination)
	if dest == "" {
		return "", invalid("destination is required")
	}
	if in.Date == nil || in.Start == nil {
		return "", invalid("date and arrive-by time are required")
	}
	if err := validDistance(in.DistanceOverrideKm); err != nil {
		return "", err
	}
	title := trimmed(in.Title)
	if title == "" {
		title = dest
	}
	stay := time.Duration(settings.DefaultStayMin) * time.Minute
	startAt, endAt, err := s.times(in, time.Time{}, time.Time{}, stay)
	if err != nil {
		return "", err
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	m, err := s.Store.CreateManualTrip(model.ManualTrip{
		Title: title, Destination: dest, Start: startAt, End: endAt, Enabled: enabled,
		DistanceOverrideKm: in.DistanceOverrideKm.Value,
	})
	if err != nil {
		return "", err
	}
	return trips.ManualKey(m.ID), nil
}

// UpdateTrip edits a trip. Manual trips change directly; recurring and
// calendar trips store a per-trip override holding only the fields that
// differ from the source.
func (s *Service) UpdateTrip(key string, in TripInput) error {
	k, err := trips.ParseKey(key)
	if err != nil {
		return ValidationError{Msg: err.Error()}
	}
	if err := validDistance(in.DistanceOverrideKm); err != nil {
		return err
	}
	if in.Destination != nil && trimmed(in.Destination) == "" {
		return invalid("destination is required")
	}
	if k.Source == trips.SourceManual {
		return s.updateManual(k.ID, in)
	}

	src, err := s.sourceTrip(key, k)
	if err != nil {
		return err
	}
	overrides, err := s.Store.Overrides()
	if err != nil {
		return err
	}
	ov, ok := overrides[key]
	if !ok {
		ov = model.Override{TripKey: key}
	}
	curStart, curEnd := src.Start, src.End
	if ov.Start != nil {
		curStart = *ov.Start
	}
	if ov.End != nil {
		curEnd = *ov.End
	}

	if in.Title != nil {
		// An empty title falls back to the schedule's or calendar's title.
		ov.Title = nil
		if t := trimmed(in.Title); t != "" {
			ov.Title = differs(t, src.Title)
		}
	}
	if in.Destination != nil {
		ov.Destination = differs(trimmed(in.Destination), src.Destination)
	}
	if in.Date != nil || in.Start != nil || in.End != nil {
		startAt, endAt, err := s.times(in, curStart, curEnd, 0)
		if err != nil {
			return err
		}
		ov.Start, ov.End = nil, nil
		if !startAt.Equal(src.Start) {
			ov.Start = &startAt
		}
		if !endAt.Equal(src.End) {
			ov.End = &endAt
		}
	}
	if in.Enabled != nil {
		ov.Enabled = differs(*in.Enabled, src.Enabled)
	}
	if in.DistanceOverrideKm.Set {
		ov.DistanceOverrideKm = in.DistanceOverrideKm.Value
	}
	if in.Reviewed != nil {
		ov.Reviewed = nil
		if *in.Reviewed {
			ov.Reviewed = ptr(true)
		}
	}
	return s.Store.SaveOverride(ov)
}

func differs[T comparable](v, source T) *T {
	if v == source {
		return nil
	}
	return &v
}

func (s *Service) updateManual(id int64, in TripInput) error {
	m, err := s.Store.ManualTrip(id)
	if err != nil {
		return err
	}
	if in.Title != nil {
		m.Title = trimmed(in.Title)
	}
	if in.Destination != nil {
		m.Destination = trimmed(in.Destination)
	}
	if m.Title == "" {
		m.Title = m.Destination
	}
	if in.Date != nil || in.Start != nil || in.End != nil {
		if m.Start, m.End, err = s.times(in, m.Start, m.End, 0); err != nil {
			return err
		}
	}
	if in.Enabled != nil {
		m.Enabled = *in.Enabled
	}
	if in.DistanceOverrideKm.Set {
		m.DistanceOverrideKm = in.DistanceOverrideKm.Value
	}
	return s.Store.UpdateManualTrip(m)
}

// sourceTrip returns a recurring or calendar trip as its source defines it,
// without local edits.
func (s *Service) sourceTrip(key string, k trips.Key) (trips.Trip, error) {
	settings, err := s.Settings()
	if err != nil {
		return trips.Trip{}, err
	}
	from, to := s.window()
	from = localtime.AddDays(from, -1)
	to = localtime.AddDays(to, 2)
	if k.Source == trips.SourceRecurring {
		day, err := localtime.ParseDate(s.loc(), k.Date)
		if err != nil {
			return trips.Trip{}, ValidationError{Msg: err.Error()}
		}
		from, to = day, localtime.AddDays(day, 1)
	}
	list, ignored, err := s.loadTrips(from, to, settings, key)
	if err != nil {
		return trips.Trip{}, err
	}
	for _, t := range list {
		if t.Key == key {
			return t, nil
		}
	}
	for _, ig := range ignored {
		if ig.Key == key {
			return trips.Trip{}, invalid("this calendar event is not a trip (%s)", strings.ToLower(ig.Reason))
		}
	}
	return trips.Trip{}, ErrNotFound
}

// ResetTrip removes local edits from a recurring or calendar trip.
func (s *Service) ResetTrip(key string) error {
	k, err := trips.ParseKey(key)
	if err != nil {
		return ValidationError{Msg: err.Error()}
	}
	if k.Source == trips.SourceManual {
		return invalid("manual trips have no source to reset to")
	}
	return s.Store.DeleteOverride(key)
}

// DeleteTrip removes a manual trip. Other trips can only be disabled.
func (s *Service) DeleteTrip(key string) error {
	k, err := trips.ParseKey(key)
	if err != nil {
		return ValidationError{Msg: err.Error()}
	}
	if k.Source != trips.SourceManual {
		return invalid("only manual trips can be deleted; disable this trip instead")
	}
	return s.Store.DeleteManualTrip(k.ID)
}

// ---- recurring schedules ----

// TemplateView is a schedule with its occurrences in the plan window.
type TemplateView struct {
	model.Template
	Occurrences []OccurrenceView `json:"occurrences"`
}

// OccurrenceView is one date of a schedule.
type OccurrenceView struct {
	Date       string   `json:"date"`
	Key        string   `json:"key"`
	Enabled    bool     `json:"enabled"`
	Exception  string   `json:"exception,omitempty"`
	Overridden []string `json:"overridden"`
}

// Templates lists schedules with their upcoming occurrences.
func (s *Service) Templates() ([]TemplateView, error) {
	list, err := s.Store.Templates()
	if err != nil {
		return nil, err
	}
	overrides, err := s.Store.Overrides()
	if err != nil {
		return nil, err
	}
	from, to := s.window()
	out := []TemplateView{}
	for _, t := range list {
		v := TemplateView{Template: t, Occurrences: []OccurrenceView{}}
		for _, occ := range trips.Occurrences(t, s.loc(), from, to) {
			key := trips.RecurringKey(t.ID, occ.Date)
			o := OccurrenceView{Date: occ.Date, Key: key, Enabled: t.Enabled, Overridden: []string{}}
			if occ.Exception != nil {
				o.Enabled = false
				o.Exception = occ.Exception.Note
				if o.Exception == "" {
					o.Exception = "Exception"
				}
			} else if ov, ok := overrides[key]; ok {
				if ov.Enabled != nil {
					o.Enabled = t.Enabled && *ov.Enabled
				}
				o.Overridden = ov.Fields()
			}
			v.Occurrences = append(v.Occurrences, o)
		}
		out = append(out, v)
	}
	return out, nil
}

func cleanTemplate(t model.Template) (model.Template, error) {
	t.Title = strings.TrimSpace(t.Title)
	t.Destination = strings.TrimSpace(t.Destination)
	if t.Destination == "" {
		return t, invalid("destination is required")
	}
	if t.Title == "" {
		t.Title = t.Destination
	}
	seen := map[int]bool{}
	var days []int
	for _, d := range t.Weekdays {
		if d < 1 || d > 7 {
			return t, invalid("weekdays must be 1 (Monday) to 7 (Sunday)")
		}
		if !seen[d] {
			seen[d] = true
			days = append(days, d)
		}
	}
	if len(days) == 0 {
		return t, invalid("choose at least one weekday")
	}
	sort.Ints(days)
	t.Weekdays = days
	for _, c := range []string{t.Start, t.End} {
		if _, _, err := localtime.ParseClock(c); err != nil {
			return t, ValidationError{Msg: err.Error()}
		}
	}
	for i, e := range t.Exceptions {
		if e.To == "" {
			e.To = e.From
		}
		_, errFrom := time.Parse(localtime.DateLayout, e.From)
		_, errTo := time.Parse(localtime.DateLayout, e.To)
		if errFrom != nil || errTo != nil || e.To < e.From {
			return t, invalid("exception dates must be YYYY-MM-DD with the end on or after the start")
		}
		e.Note = strings.TrimSpace(e.Note)
		t.Exceptions[i] = e
	}
	if t.Exceptions == nil {
		t.Exceptions = []model.DateRange{}
	}
	return t, nil
}

// CreateTemplate adds a recurring schedule.
func (s *Service) CreateTemplate(t model.Template) (model.Template, error) {
	t, err := cleanTemplate(t)
	if err != nil {
		return t, err
	}
	return s.Store.CreateTemplate(t)
}

// UpdateTemplate edits a schedule; dates with local edits keep them.
func (s *Service) UpdateTemplate(t model.Template) (model.Template, error) {
	t, err := cleanTemplate(t)
	if err != nil {
		return t, err
	}
	return t, s.Store.UpdateTemplate(t)
}

// DeleteTemplate removes a schedule.
func (s *Service) DeleteTemplate(id int64) error { return s.Store.DeleteTemplate(id) }
