package calendar

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/diogoap/evcc-planner/internal/clock"
	"github.com/diogoap/evcc-planner/internal/config"
	"github.com/diogoap/evcc-planner/internal/model"
)

var ams, _ = time.LoadLocation("Europe/Amsterdam")

const feed = "BEGIN:VCALENDAR\r\n" +
	"VERSION:2.0\r\n" +
	"X-WR-CALNAME:Kids\r\n" +
	// Weekly event across the DST change, one date excluded, one moved, one cancelled.
	"BEGIN:VEVENT\r\nUID:swim@x\r\nDTSTART;TZID=Europe/Amsterdam:20261021T163000\r\n" +
	"DTEND;TZID=Europe/Amsterdam:20261021T171500\r\nRRULE:FREQ=WEEKLY;COUNT=6\r\n" +
	"EXDATE;TZID=Europe/Amsterdam:20261104T163000\r\nSUMMARY:Swimming\r\nLOCATION:Sportlaan 3\\, Town\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nUID:swim@x\r\nRECURRENCE-ID;TZID=Europe/Amsterdam:20261111T163000\r\n" +
	"DTSTART;TZID=Europe/Amsterdam:20261112T170000\r\nDTEND;TZID=Europe/Amsterdam:20261112T174500\r\n" +
	"SUMMARY:Swimming (moved)\r\nLOCATION:Sportlaan 3\\, Town\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nUID:swim@x\r\nRECURRENCE-ID;TZID=Europe/Amsterdam:20261118T163000\r\n" +
	"DTSTART;TZID=Europe/Amsterdam:20261118T163000\r\nDTEND;TZID=Europe/Amsterdam:20261118T171500\r\n" +
	"STATUS:CANCELLED\r\nSUMMARY:Swimming\r\nEND:VEVENT\r\n" +
	// All-day event: skipped.
	"BEGIN:VEVENT\r\nUID:holiday@x\r\nDTSTART;VALUE=DATE:20261028\r\nDTEND;VALUE=DATE:20261029\r\nSUMMARY:School holiday\r\nEND:VEVENT\r\n" +
	// UTC event.
	"BEGIN:VEVENT\r\nUID:office@x\r\nDTSTART:20261110T083000Z\r\nDTEND:20261110T110000Z\r\nSUMMARY:Office visit\r\nLOCATION:Office\r\nEND:VEVENT\r\n" +
	// Cancelled single event: skipped.
	"BEGIN:VEVENT\r\nUID:gone@x\r\nDTSTART:20261110T130000Z\r\nDTEND:20261110T140000Z\r\nSTATUS:CANCELLED\r\nSUMMARY:Gone\r\nEND:VEVENT\r\n" +
	"END:VCALENDAR\r\n"

func TestParseExpandsRecurrenceAndSkipsNonTrips(t *testing.T) {
	from := time.Date(2026, 10, 19, 0, 0, 0, 0, ams)
	to := time.Date(2026, 11, 30, 0, 0, 0, 0, ams)
	events, name, err := Parse([]byte(feed), "cal1", ams, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if name != "Kids" {
		t.Errorf("calendar name = %q, want the feed's X-WR-CALNAME", name)
	}
	var got []string
	for _, e := range events {
		got = append(got, e.Start.In(ams).Format("2006-01-02 15:04")+" "+e.Title)
	}
	want := []string{
		"2026-10-21 16:30 Swimming",
		"2026-10-28 16:30 Swimming", // after DST change, same local time
		"2026-11-10 09:30 Office visit",
		"2026-11-12 17:00 Swimming (moved)",
		"2026-11-25 16:30 Swimming",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("events:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if events[0].Location != "Sportlaan 3, Town" {
		t.Errorf("location unescaped = %q", events[0].Location)
	}
	if events[0].End.Sub(events[0].Start) != 45*time.Minute {
		t.Errorf("duration = %s", events[0].End.Sub(events[0].Start))
	}

	// Keys are stable across syncs and unique per instance.
	again, _, _ := Parse([]byte(feed), "cal1", ams, from, to)
	seen := map[string]bool{}
	for i, e := range events {
		if again[i].Key != e.Key {
			t.Errorf("key changed between parses for %s", e.Title)
		}
		if seen[e.Key] {
			t.Errorf("duplicate key %s", e.Key)
		}
		seen[e.Key] = true
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	if _, _, err := Parse([]byte("not a calendar"), "x", ams, time.Now(), time.Now().Add(time.Hour)); err == nil {
		t.Fatal("want error for invalid feed")
	}
}

func TestClassify(t *testing.T) {
	home := "Damrak 1, 1012 LG Amsterdam"
	places := []model.Place{{Name: "School", Address: "Schoolstraat 1, Amsterdam"}}
	cases := []struct {
		location string
		trip     bool
		review   bool
		reason   string
	}{
		{"", false, false, ReasonNoLocation},
		{"https://meet.google.com/abc-def", false, false, ReasonOnline},
		{"Microsoft Teams Meeting", false, false, ReasonOnline},
		{"Damrak 1, Amsterdam, Netherlands", false, false, ReasonAtHome},
		{"Home", false, false, ReasonAtHome},
		{"school", true, false, ""},
		{"Schoolstraat 1, Amsterdam, NL", true, false, ""},
		{"Stationsweg 12, Utrecht", true, false, ""},
		{"Grandma", true, true, ""},
		{"Amsterdam", true, true, ""},
	}
	for _, c := range cases {
		got := Classify(c.location, home, places)
		if got.Trip != c.trip || got.NeedsReview != c.review || got.Reason != c.reason {
			t.Errorf("Classify(%q) = %+v, want trip=%v review=%v reason=%q", c.location, got, c.trip, c.review, c.reason)
		}
	}
}

type memStore struct {
	events   map[string][]model.CalendarEvent
	statuses map[string]model.CalendarSyncStatus
}

func (m *memStore) ReplaceCalendarEvents(id, name string, e []model.CalendarEvent) error {
	m.events[id] = e
	return nil
}
func (m *memStore) SaveSyncStatus(st model.CalendarSyncStatus) error {
	m.statuses[st.CalendarID] = st
	return nil
}
func (m *memStore) RemoveCalendarsExcept([]string) error { return nil }
func (m *memStore) SyncStatuses() (map[string]model.CalendarSyncStatus, error) {
	return m.statuses, nil
}

type failingSource struct{}

func (failingSource) Fetch(context.Context, config.CalendarSource) ([]byte, error) {
	return nil, io.ErrUnexpectedEOF
}

func TestSyncKeepsEventsWhenFeedFails(t *testing.T) {
	now := time.Date(2026, 11, 9, 19, 0, 0, 0, ams)
	st := &memStore{events: map[string][]model.CalendarEvent{}, statuses: map[string]model.CalendarSyncStatus{}}
	s := &Syncer{
		Store: st, Source: StubSource{Clock: clock.Fixed{T: now}, Location: ams},
		Calendars: []config.CalendarSource{{ID: "c1", Label: "Calendar"}}, Clock: clock.Fixed{T: now}, Location: ams,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if err := s.SyncAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	n := len(st.events["c1"])
	if n == 0 || st.statuses["c1"].LastSuccessAt == nil || st.statuses["c1"].Name != "Demo calendar" {
		t.Fatalf("stub sync stored %d events, status %+v", n, st.statuses["c1"])
	}
	if st.events["c1"][0].Calendar != "Demo calendar" {
		t.Errorf("events should carry the feed's name: %+v", st.events["c1"][0])
	}

	s.Source = failingSource{}
	if err := s.SyncAll(context.Background()); err == nil {
		t.Fatal("want error from failing feed")
	}
	stat := st.statuses["c1"]
	if len(st.events["c1"]) != n || stat.LastError == "" || stat.LastSuccessAt == nil || stat.EventCount != n ||
		stat.Name != "Demo calendar" {
		t.Fatalf("failed sync should keep events, name, and last success: %d events, %+v", len(st.events["c1"]), stat)
	}
}

var httpClientUnreachable = http.Client{Timeout: 2 * time.Second}

func TestHTTPSourceHidesURL(t *testing.T) {
	_, err := HTTPSource{HTTP: &httpClientUnreachable}.Fetch(context.Background(),
		config.CalendarSource{ID: "x", URL: "https://127.0.0.1:1/private-secret/basic.ics"})
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("error should not reveal the secret URL: %v", err)
	}
	if _, err = (HTTPSource{}).Fetch(context.Background(), config.CalendarSource{ID: "x"}); err == nil {
		t.Fatal("missing URL should fail")
	}
}
