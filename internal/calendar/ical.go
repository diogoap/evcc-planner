// Package calendar syncs Google Calendar secret iCal feeds and decides which
// events are car trips.
package calendar

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/emersion/go-ical"

	"github.com/diogoap/evcc-planner/internal/model"
)

// Parse decodes an iCal feed and returns its name (X-WR-CALNAME, if any) and
// the timed, non-cancelled event instances overlapping [from, to), expanding
// recurring events (RRULE, RDATE, EXDATE) and applying moved or cancelled
// instances (RECURRENCE-ID). All-day events are skipped: they mark days, not
// journeys.
func Parse(data []byte, calendarID string, loc *time.Location, from, to time.Time) ([]model.CalendarEvent, string, error) {
	cal, err := ical.NewDecoder(bytes.NewReader(data)).Decode()
	if err != nil {
		return nil, "", fmt.Errorf("parse ical: %w", err)
	}
	name, _ := cal.Props.Text("X-WR-CALNAME")

	type override struct {
		ev  ical.Event
		rid time.Time
	}
	var masters []ical.Event
	overrides := map[string][]override{}
	replaced := map[string]map[int64]bool{}
	for _, ev := range cal.Events() {
		uid, _ := ev.Props.Text(ical.PropUID)
		if rp := ev.Props.Get(ical.PropRecurrenceID); rp != nil {
			rid, err := rp.DateTime(loc)
			if err != nil {
				continue
			}
			overrides[uid] = append(overrides[uid], override{ev: ev, rid: rid})
			if replaced[uid] == nil {
				replaced[uid] = map[int64]bool{}
			}
			replaced[uid][rid.Unix()] = true
			continue
		}
		masters = append(masters, ev)
	}

	var out []model.CalendarEvent
	add := func(ev ical.Event, uid, instance string, start, end time.Time) {
		if end.Before(start) {
			end = start
		}
		if !start.Before(to) || end.Before(from) {
			return
		}
		title, _ := ev.Props.Text(ical.PropSummary)
		location, _ := ev.Props.Text(ical.PropLocation)
		out = append(out, model.CalendarEvent{
			Key:        eventKey(calendarID, uid, instance),
			CalendarID: calendarID,
			UID:        uid,
			Title:      strings.TrimSpace(title),
			Location:   strings.TrimSpace(location),
			Start:      start,
			End:        end,
		})
	}

	for _, ev := range masters {
		if skip(ev) {
			continue
		}
		uid, _ := ev.Props.Text(ical.PropUID)
		start, err := ev.DateTimeStart(loc)
		if err != nil {
			continue
		}
		end, err := ev.DateTimeEnd(loc)
		if err != nil || end.IsZero() {
			end = start
		}
		dur := end.Sub(start)
		set, err := ev.RecurrenceSet(loc)
		if err != nil {
			continue
		}
		if set == nil {
			add(ev, uid, "", start, end)
			continue
		}
		for _, occ := range set.Between(from.Add(-dur), to, true) {
			if replaced[uid][occ.Unix()] {
				continue
			}
			add(ev, uid, occ.UTC().Format(time.RFC3339), occ, occ.Add(dur))
		}
	}

	for uid, list := range overrides {
		for _, o := range list {
			if skip(o.ev) {
				continue
			}
			start, err := o.ev.DateTimeStart(loc)
			if err != nil {
				continue
			}
			end, err := o.ev.DateTimeEnd(loc)
			if err != nil || end.IsZero() {
				end = start
			}
			add(o.ev, uid, o.rid.UTC().Format(time.RFC3339), start, end)
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out, strings.TrimSpace(name), nil
}

func skip(ev ical.Event) bool {
	if status, _ := ev.Props.Text(ical.PropStatus); strings.EqualFold(status, "CANCELLED") {
		return true
	}
	p := ev.Props.Get(ical.PropDateTimeStart)
	return p == nil || p.ValueType() == ical.ValueDate || len(p.Value) == len("20060102")
}

// eventKey is stable for the same event or recurring instance across syncs,
// so local edits survive calendar changes.
func eventKey(calendarID, uid, instance string) string {
	sum := sha1.Sum([]byte(calendarID + "\x00" + uid + "\x00" + instance))
	return hex.EncodeToString(sum[:8])
}
