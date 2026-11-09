package calendar

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/diogoap/evcc-planner/internal/clock"
	"github.com/diogoap/evcc-planner/internal/config"
	"github.com/diogoap/evcc-planner/internal/localtime"
)

// StubCalendars is the demo calendar used when none is configured.
var StubCalendars = []config.CalendarSource{{ID: "demo", Label: "Calendar"}}

// StubSource generates a demo iCal feed for the current week, exercising the
// same parser as real feeds: a saved place, an address, an online meeting,
// an unclear location, no location, and a weekly recurring event.
type StubSource struct {
	Clock    clock.Clock
	Location *time.Location
}

// Fetch returns the demo feed.
func (s StubSource) Fetch(ctx context.Context, cal config.CalendarSource) ([]byte, error) {
	today := localtime.StartOfDay(s.Clock.Now(), s.Location)
	day := func(n int) string { return localtime.AddDays(today, n).Format("20060102") }
	type ev struct {
		uid, day, start, end, summary, location, rrule string
	}
	events := []ev{
		{"office-visit", day(1), "093000", "120000", "Office visit", "Office", ""},
		{"team-call", day(2), "100000", "110000", "Team call", "https://meet.google.com/demo-call", ""},
		{"dentist", day(3), "140000", "144500", "Dentist", "Stationsweg 12, Demo Town", ""},
		{"birthday", day(5), "140000", "170000", "Birthday party", "Grandma", ""},
		{"swimming", day(2), "163000", "171500", "Swimming lessons", "Sportlaan 3, Demo Town", "FREQ=WEEKLY;COUNT=10"},
		{"piano", day(4), "170000", "173000", "Piano lesson", "", ""},
	}
	var b strings.Builder
	b.WriteString("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//evcc-planner//demo//EN\r\nX-WR-CALNAME:Demo calendar\r\n")
	for _, e := range events {
		fmt.Fprintf(&b, "BEGIN:VEVENT\r\nUID:%s@demo\r\nDTSTAMP:20260101T000000Z\r\n", e.uid)
		fmt.Fprintf(&b, "DTSTART;TZID=%s:%sT%s\r\nDTEND;TZID=%s:%sT%s\r\n", s.Location, e.day, e.start, s.Location, e.day, e.end)
		fmt.Fprintf(&b, "SUMMARY:%s\r\n", e.summary)
		if e.location != "" {
			fmt.Fprintf(&b, "LOCATION:%s\r\n", strings.ReplaceAll(e.location, ",", "\\,"))
		}
		if e.rrule != "" {
			fmt.Fprintf(&b, "RRULE:%s\r\n", e.rrule)
		}
		b.WriteString("END:VEVENT\r\n")
	}
	b.WriteString("END:VCALENDAR\r\n")
	return []byte(b.String()), nil
}
