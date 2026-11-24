package trips

import (
	"strings"
	"testing"
	"time"

	"github.com/diogoap/evcc-planner/internal/model"
)

var ams, _ = time.LoadLocation("Europe/Amsterdam")

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, ams) }

func schoolRun() model.Template {
	return model.Template{
		ID: 1, Title: "School drop-off", Destination: "School", Weekdays: []int{1, 2, 3, 4, 5},
		Start: "08:15", End: "08:20", Enabled: true,
	}
}

func TestOccurrencesKeepLocalTimeAcrossDST(t *testing.T) {
	tpl := schoolRun()
	tpl.Weekdays = []int{5, 6, 7, 1} // Fri to Mon around the 25 October change
	occ := Occurrences(tpl, ams, day(2026, 10, 23), day(2026, 10, 27))
	if len(occ) != 4 {
		t.Fatalf("got %d occurrences, want 4", len(occ))
	}
	for _, o := range occ {
		if l := o.Start.In(ams); l.Hour() != 8 || l.Minute() != 15 {
			t.Errorf("%s starts %s local, want 08:15", o.Date, l.Format("15:04"))
		}
	}
	if _, off := occ[0].Start.Zone(); off != 2*3600 {
		t.Errorf("Friday offset %d, want CEST", off)
	}
	if _, off := occ[3].Start.Zone(); off != 3600 {
		t.Errorf("Monday offset %d, want CET", off)
	}
}

func TestOccurrencesExceptionsAndOvernightEnd(t *testing.T) {
	tpl := schoolRun()
	tpl.Exceptions = []model.DateRange{{From: "2026-11-11", To: "2026-11-12", Note: "Study days"}}
	occ := Occurrences(tpl, ams, day(2026, 11, 9), day(2026, 11, 16))
	if len(occ) != 5 {
		t.Fatalf("got %d weekday occurrences, want 5", len(occ))
	}
	if occ[2].Exception == nil || occ[3].Exception == nil || occ[1].Exception != nil {
		t.Errorf("exceptions not applied to Wed/Thu only: %+v", occ)
	}

	night := model.Template{ID: 2, Title: "Night shift", Destination: "Hospital", Weekdays: []int{1}, Start: "22:00", End: "06:00", Enabled: true}
	o := Occurrences(night, ams, day(2026, 11, 9), day(2026, 11, 10))[0]
	if o.End.Sub(o.Start) != 8*time.Hour {
		t.Errorf("overnight stay = %s, want 8h", o.End.Sub(o.Start))
	}
}

func TestBuildMergesSourcesAndAppliesOverrides(t *testing.T) {
	places := []model.Place{{ID: 1, Name: "School", Address: "Schoolstraat 1, Town"}}
	thu := RecurringKey(1, "2026-11-12")
	fri := RecurringKey(1, "2026-11-13")
	off := false
	later := time.Date(2026, 11, 13, 8, 45, 0, 0, ams)
	in := Input{
		Location: ams, From: day(2026, 11, 9), To: day(2026, 11, 16), Home: "Home 1, Town", Places: places,
		Templates: []model.Template{schoolRun()},
		Manual: []model.ManualTrip{{
			ID: 7, Title: "Hardware store", Destination: "Bouwmarkt 5, Town",
			Start: time.Date(2026, 11, 14, 10, 0, 0, 0, ams), End: time.Date(2026, 11, 14, 11, 0, 0, 0, ams), Enabled: true,
		}},
		Events: []model.CalendarEvent{
			{Key: "e1", Calendar: "Family", Title: "Dentist", Location: "Stationsweg 12, Town",
				Start: time.Date(2026, 11, 10, 14, 0, 0, 0, ams), End: time.Date(2026, 11, 10, 14, 45, 0, 0, ams)},
			{Key: "e2", Calendar: "Family", Title: "Team call", Location: "https://meet.google.com/x",
				Start: time.Date(2026, 11, 11, 10, 0, 0, 0, ams), End: time.Date(2026, 11, 11, 11, 0, 0, 0, ams)},
			{Key: "e3", Calendar: "Family", Title: "Party", Location: "Grandma",
				Start: time.Date(2026, 11, 15, 14, 0, 0, 0, ams), End: time.Date(2026, 11, 15, 17, 0, 0, 0, ams)},
		},
		Overrides: map[string]model.Override{
			thu:               {TripKey: thu, Enabled: &off},
			fri:               {TripKey: fri, Start: &later},
			CalendarKey("e3"): {TripKey: CalendarKey("e3"), Reviewed: ptr(true)},
			"recurring:99:x":  {TripKey: "recurring:99:x", Enabled: &off}, // orphan; ignored
		},
	}
	list, ignored := Build(in)
	if len(list) != 5+1+2 {
		t.Fatalf("got %d trips, want 8: %+v", len(list), list)
	}
	if len(ignored) != 1 || ignored[0].Reason != "Online meeting" {
		t.Errorf("ignored = %+v, want the online meeting", ignored)
	}
	byKey := map[string]Trip{}
	for _, tr := range list {
		byKey[tr.Key] = tr
	}
	if tr := byKey[thu]; tr.Enabled || len(tr.Overridden) != 1 {
		t.Errorf("Thursday should be disabled by override: %+v", tr)
	}
	if tr := byKey[fri]; !tr.Start.Equal(later) || tr.Title != "School drop-off" {
		t.Errorf("Friday should keep its moved time and the template title: %+v", tr)
	}
	if tr := byKey[RecurringKey(1, "2026-11-10")]; tr.Address != "Schoolstraat 1, Town" || tr.PlaceName != "School" {
		t.Errorf("saved place not resolved: %+v", tr)
	}
	if tr := byKey[CalendarKey("e3")]; tr.NeedsReview {
		t.Errorf("reviewed event should not need review: %+v", tr)
	}
	if !list[0].Start.Before(list[1].Start) {
		t.Errorf("trips not sorted by arrival")
	}

	// A template edit reaches dates with unrelated overrides.
	in.Templates[0].Title = "School run"
	list, _ = Build(in)
	for _, tr := range list {
		if tr.Key == thu && tr.Title != "School run" {
			t.Errorf("template title change did not reach overridden date: %+v", tr)
		}
	}
}

func TestPausedTemplateHasNoTrips(t *testing.T) {
	tpl := schoolRun()
	tpl.Enabled = false
	list, _ := Build(Input{Location: ams, From: day(2026, 11, 9), To: day(2026, 11, 16), Templates: []model.Template{tpl}})
	if len(list) != 0 {
		t.Fatalf("paused schedule produced %d trips", len(list))
	}
}

func TestUnclearCalendarLocationNeedsReview(t *testing.T) {
	list, _ := Build(Input{
		Location: ams, From: day(2026, 11, 9), To: day(2026, 11, 16),
		Events: []model.CalendarEvent{{Key: "e", Title: "Party", Location: "Grandma",
			Start: time.Date(2026, 11, 15, 14, 0, 0, 0, ams), End: time.Date(2026, 11, 15, 17, 0, 0, 0, ams)}},
	})
	if len(list) != 1 || !list[0].NeedsReview || !list[0].Enabled {
		t.Fatalf("want one enabled trip needing review, got %+v", list)
	}
}

func TestParseKey(t *testing.T) {
	cases := map[string]Key{
		"manual:3":                {Source: SourceManual, ID: 3},
		"recurring:2:2026-11-12":  {Source: SourceRecurring, ID: 2, Date: "2026-11-12"},
		"calendar:80cebe0497495c": {Source: SourceCalendar, Event: "80cebe0497495c"},
	}
	for in, want := range cases {
		got, err := ParseKey(in)
		if err != nil || got != want {
			t.Errorf("ParseKey(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "manual:x", "recurring:1:2026-13-01", "calendar:", "other:1"} {
		if _, err := ParseKey(bad); err == nil {
			t.Errorf("ParseKey(%q) succeeded, want error", bad)
		}
	}
}

func ptr[T any](v T) *T { return &v }

func TestRoutes(t *testing.T) {
	places := []model.Place{{ID: 1, Name: "School", Address: "Schoolstraat 1, Town"}}
	home := "Home 1, Town"
	start := time.Date(2026, 11, 10, 10, 0, 0, 0, ams)
	in := Input{
		Location: ams, From: day(2026, 11, 9), To: day(2026, 11, 16), Home: home, Places: places,
		Manual: []model.ManualTrip{
			{ID: 1, Title: "Default", Destination: "School", Start: start, End: start.Add(time.Hour), Enabled: true},
			{ID: 2, Title: "Errands", Destination: "School", Start: start, End: start.Add(time.Hour), Enabled: true,
				Points: []string{"home", "School", "Shop 3, Town", " Home 1,  town "}},
			{ID: 3, Title: "Airport", Destination: "Airport 1", Start: start, End: start, Enabled: true,
				Points: []string{"Home", "Airport 1"}},
			{ID: 4, Title: "Back", Destination: "Home", Start: start, End: start, Enabled: true,
				Points: []string{"Airport 1", "Home"}},
		},
		Events: []model.CalendarEvent{{Key: "e1", Calendar: "Family", Title: "Dentist", Location: "Stationsweg 12, Town",
			Start: start, End: start.Add(time.Hour)}},
		Overrides: map[string]model.Override{
			CalendarKey("e1"): {TripKey: CalendarKey("e1"), Points: []string{"Home", "Stationsweg 12, Town", "School", "Home"}},
		},
	}
	list, _ := Build(in)
	by := map[string]Trip{}
	for _, tr := range list {
		by[tr.Key] = tr
	}
	names := func(tr Trip) string {
		var n []string
		for _, p := range tr.Points {
			n = append(n, p.Name+"="+p.Address)
		}
		return strings.Join(n, " | ")
	}

	def := by[ManualKey(1)]
	if names(def) != "Home=Home 1, Town | School=Schoolstraat 1, Town | Home=Home 1, Town" || def.Route != nil {
		t.Errorf("default route = %s", names(def))
	}
	if def.OneWay() || def.StartsAway() || def.EndsAway() || def.Address != "Schoolstraat 1, Town" || def.PlaceName != "School" {
		t.Errorf("default trip = %+v", def)
	}
	// "home" is Home; the home address typed out is home too.
	errands := by[ManualKey(2)]
	if len(errands.Points) != 4 || !errands.Points[0].Home || errands.Points[0].Name != "Home" ||
		!errands.Points[3].Home || errands.Points[3].PlaceName != "Home" || errands.Points[2].Home {
		t.Errorf("errands = %+v", errands.Points)
	}
	if a := by[ManualKey(3)]; !a.OneWay() || a.StartsAway() || !a.EndsAway() {
		t.Errorf("one way out = %+v", a.Points)
	}
	if b := by[ManualKey(4)]; !b.OneWay() || !b.StartsAway() || b.EndsAway() || b.PlaceName != "Home" {
		t.Errorf("one way back = %+v", b)
	}
	cal := by[CalendarKey("e1")]
	if len(cal.Points) != 4 || cal.Points[2].PlaceName != "School" || strings.Join(cal.Overridden, ",") != "route" {
		t.Errorf("calendar route = %s %v", names(cal), cal.Overridden)
	}

	if !IsDefaultRoute([]string{"home", "School", "Home"}) || IsDefaultRoute([]string{"Home", "Home", "Home"}) ||
		IsDefaultRoute([]string{"Home", "School"}) || IsDefaultRoute([]string{"Office", "School", "Home"}) {
		t.Error("IsDefaultRoute")
	}
	// Without a home address, Home has no address (no route).
	if p := ResolvePoint("Home", "", places); !p.Home || p.Address != "" {
		t.Errorf("home without address = %+v", p)
	}
}
