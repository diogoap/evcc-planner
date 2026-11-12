package app

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/diogoap/evcc-planner/internal/audit"
	"github.com/diogoap/evcc-planner/internal/config"
	"github.com/diogoap/evcc-planner/internal/evcc"
	"github.com/diogoap/evcc-planner/internal/model"
	"github.com/diogoap/evcc-planner/internal/service"
)

type activityEvent struct {
	ID          int64          `json:"id"`
	At          time.Time      `json:"at"`
	Type        string         `json:"type"`
	Source      string         `json:"source"`
	Action      string         `json:"action"`
	SubjectType string         `json:"subjectType"`
	SubjectID   string         `json:"subjectId"`
	Related     string         `json:"related"`
	Summary     string         `json:"summary"`
	Before      map[string]any `json:"before"`
	After       map[string]any `json:"after"`
	Detail      map[string]any `json:"detail"`
	Outcome     string         `json:"outcome"`
	Error       string         `json:"error"`
	Service     string         `json:"service"`
	StatusCode  int            `json:"statusCode"`
	Bytes       int64          `json:"bytes"`
}

type activityPage struct {
	Events        []activityEvent `json:"events"`
	Next          *int64          `json:"next"`
	RetentionDays int             `json:"retentionDays"`
}

func (ta *testApp) activity(query string) activityPage {
	ta.t.Helper()
	var p activityPage
	ta.json("GET", "/api/activity"+query, nil, http.StatusOK, &p)
	return p
}

// eventLog returns the events recorded since its previous check, oldest
// first.
type eventLog struct {
	ta   *testApp
	last int64
}

func (l *eventLog) fresh() []activityEvent {
	l.ta.t.Helper()
	var out []activityEvent
	for _, e := range l.ta.activity("?limit=500").Events {
		if e.ID > l.last {
			out = append(out, e)
		}
	}
	slices.Reverse(out)
	if len(out) > 0 {
		l.last = out[len(out)-1].ID
	}
	return out
}

// one asserts that exactly one successful event was recorded since the
// last check, with the given action and source.
func (l *eventLog) one(action, source string) activityEvent {
	l.ta.t.Helper()
	got := l.fresh()
	if len(got) != 1 {
		l.ta.t.Fatalf("%s: got %d events, want exactly one: %+v", action, len(got), got)
	}
	e := got[0]
	if e.Action != action || e.Source != source || e.Outcome != model.OutcomeSuccess || e.Type != model.EventAction {
		l.ta.t.Fatalf("event = %+v, want a successful %s from %s", e, action, source)
	}
	return e
}

func (l *eventLog) none(what string) {
	l.ta.t.Helper()
	if got := l.fresh(); len(got) != 0 {
		l.ta.t.Fatalf("%s recorded %+v", what, got)
	}
}

// changed asserts an event's before and after values for one field.
func changed(t *testing.T, e activityEvent, field string, before, after any) {
	t.Helper()
	if b, a := e.Before[field], e.After[field]; b != before || a != after {
		t.Errorf("%s %s: %v → %v, want %v → %v (event %+v)", e.Action, field, b, a, before, after, e)
	}
}

func TestEveryChangeRecordsOneEvent(t *testing.T) {
	ta := newTestApp(t, "")
	log := &eventLog{ta: ta}
	log.none("starting the demo")
	ui := model.SourceUI

	// Manual trips.
	var created struct{ Key string }
	ta.json("POST", "/api/trips", map[string]any{
		"title": "Hardware store", "destination": "Bouwmarkt 5, Demo Town", "date": "2026-11-15", "start": "10:00",
	}, http.StatusCreated, &created)
	e := log.one("trip.create", ui)
	if e.SubjectType != "trip" || e.SubjectID != created.Key || e.Before != nil || e.After["arriveBy"] != "2026-11-15 10:00" ||
		e.After["until"] != "2026-11-15 10:15" || e.After["title"] != "Hardware store" || e.Summary != "Added Hardware store on Sun 15 Nov" {
		t.Errorf("create = %+v", e)
	}
	ta.json("PATCH", "/api/trips/"+created.Key, map[string]any{"start": "11:00"}, http.StatusOK, nil)
	e = log.one("trip.update", ui)
	changed(t, e, "arriveBy", "2026-11-15 10:00", "2026-11-15 11:00")
	changed(t, e, "until", "2026-11-15 10:15", "2026-11-15 11:15")
	if len(e.After) != 2 || e.Summary != "Edited Hardware store on Sun 15 Nov: arrive-by time, end time" {
		t.Errorf("only changed fields are recorded: %+v", e)
	}
	ta.json("PATCH", "/api/trips/"+created.Key, map[string]any{"enabled": false}, http.StatusOK, nil)
	changed(t, log.one("trip.disable", ui), "enabled", true, false)
	ta.json("DELETE", "/api/trips/"+created.Key, nil, http.StatusNoContent, nil)
	if e = log.one("trip.delete", ui); e.After != nil || e.Before["title"] != "Hardware store" || e.Before["enabled"] != false {
		t.Errorf("delete = %+v", e)
	}

	// Dates of a schedule: disable, re-enable, edit, reset.
	thu := "recurring:2:2026-11-12"
	ta.json("PATCH", "/api/trips/"+thu, map[string]any{"enabled": false}, http.StatusOK, nil)
	e = log.one("trip.disable", ui)
	changed(t, e, "enabled", true, false)
	if e.SubjectID != thu || e.Related != "schedule:2" || e.Summary != "Disabled School pickup on Thu 12 Nov" {
		t.Errorf("date disable = %+v", e)
	}
	ta.json("PATCH", "/api/trips/"+thu, map[string]any{"enabled": true}, http.StatusOK, nil)
	changed(t, log.one("trip.enable", ui), "enabled", false, true)
	fri := "recurring:1:2026-11-13"
	ta.json("PATCH", "/api/trips/"+fri, map[string]any{"start": "08:45", "end": "08:50"}, http.StatusOK, nil)
	changed(t, log.one("trip.update", ui), "arriveBy", "2026-11-13 08:15", "2026-11-13 08:45")
	ta.json("POST", "/api/trips/"+fri+"/reset", map[string]any{}, http.StatusNoContent, nil)
	e = log.one("trip.reset", ui)
	changed(t, e, "arriveBy", "2026-11-13 08:45", "2026-11-13 08:15")
	if e.Summary != "Reset School drop-off on Fri 13 Nov to its schedule" {
		t.Errorf("reset = %+v", e)
	}

	// Calendar trip confirmed.
	var review string
	for _, tr := range ta.plan().Trips {
		if tr.NeedsReview {
			review = tr.Key
		}
	}
	ta.json("PATCH", "/api/trips/"+review, map[string]any{"reviewed": true}, http.StatusOK, nil)
	e = log.one("trip.confirm", ui)
	changed(t, e, "needsReview", true, false)
	if !strings.HasSuffix(e.Summary, "as a car trip") || e.Related != "" {
		t.Errorf("confirm = %+v", e)
	}

	// Schedules: create, pause, resume, edit, delete.
	tpl := map[string]any{"title": "Swimming", "destination": "Pool", "weekdays": []int{6}, "start": "09:00", "end": "10:00", "enabled": true, "exceptions": []any{}}
	var saved struct{ ID int64 }
	ta.json("POST", "/api/recurring", tpl, http.StatusCreated, &saved)
	if e = log.one("schedule.create", ui); e.SubjectID != itoa(saved.ID) || e.After["weekdays"] != "Sat" || e.After["arriveBy"] != "09:00" {
		t.Errorf("schedule create = %+v", e)
	}
	path := "/api/recurring/" + itoa(saved.ID)
	tpl["enabled"] = false
	ta.json("PUT", path, tpl, http.StatusOK, nil)
	changed(t, log.one("schedule.pause", ui), "enabled", true, false)
	tpl["enabled"] = true
	ta.json("PUT", path, tpl, http.StatusOK, nil)
	log.one("schedule.resume", ui)
	tpl["weekdays"] = []int{6, 7}
	ta.json("PUT", path, tpl, http.StatusOK, nil)
	e = log.one("schedule.update", ui)
	changed(t, e, "weekdays", "Sat", "Sat, Sun")
	if e.Summary != "Edited schedule Swimming: weekdays" {
		t.Errorf("schedule edit = %+v", e)
	}
	ta.json("DELETE", path, nil, http.StatusNoContent, nil)
	if e = log.one("schedule.delete", ui); e.Before["title"] != "Swimming" || e.After != nil {
		t.Errorf("schedule delete = %+v", e)
	}

	// Places.
	var place struct{ ID int64 }
	ta.json("POST", "/api/places", map[string]any{"name": "Pool", "address": "Badweg 1"}, http.StatusCreated, &place)
	if e = log.one("place.create", ui); e.After["address"] != "Badweg 1" {
		t.Errorf("place create = %+v", e)
	}
	ta.json("PUT", "/api/places/"+itoa(place.ID), map[string]any{"name": "Pool", "address": "Badweg 2"}, http.StatusOK, nil)
	changed(t, log.one("place.update", ui), "address", "Badweg 1", "Badweg 2")
	ta.json("DELETE", "/api/places/"+itoa(place.ID), nil, http.StatusNoContent, nil)
	log.one("place.delete", ui)

	// Settings.
	var s map[string]any
	ta.json("GET", "/api/settings", nil, http.StatusOK, &s)
	s["consumptionKWhPer100Km"] = 20.0
	ta.json("PUT", "/api/settings", s, http.StatusOK, nil)
	e = log.one("settings.update", ui)
	changed(t, e, "consumptionKWhPer100Km", 18.0, 20.0)
	if e.SubjectID != "planning" || e.Summary != "Changed settings: consumption" || len(e.After) != 1 {
		t.Errorf("settings = %+v", e)
	}

	// Advice: dismiss and restore, then its settings.
	ta.json("POST", "/api/advice/2026-11-10/dismiss", map[string]any{}, http.StatusNoContent, nil)
	e = log.one("advice.dismiss", ui)
	changed(t, e, "dismissed", false, true)
	if e.Detail["recommendation"] != "charge_ahead" || !strings.Contains(e.Summary, "Tue 10 Nov") {
		t.Errorf("dismiss = %+v", e)
	}
	ta.json("POST", "/api/advice/2026-11-10/dismiss", map[string]any{"undo": true}, http.StatusNoContent, nil)
	log.one("advice.undismiss", ui)
	var as map[string]any
	ta.json("GET", "/api/settings/advice", nil, http.StatusOK, &as)
	as["autoMinConfidence"] = 2
	ta.json("PUT", "/api/settings/advice", as, http.StatusOK, nil)
	if e = log.one("settings.update", ui); e.SubjectID != "advice" || e.Summary != "Changed advice settings: minimum confidence" {
		t.Errorf("advice settings = %+v", e)
	}

	// EVCC plan submission, with what it replaced and EVCC's response.
	ta.json("POST", "/api/evcc/plan", map[string]any{"soc": 42, "time": "2026-11-10T06:48:00Z", "tripKey": "recurring:1:2026-11-10", "confirm": true}, http.StatusOK, nil)
	e = log.one("plan.send", ui)
	changed(t, e, "evccPlan", "none", "42% by 2026-11-10 07:48")
	if e.SubjectType != "plan" || e.Related != "trip:recurring:1:2026-11-10" || e.Detail["evccResponse"] == nil {
		t.Errorf("plan send = %+v", e)
	}

	// Rejected requests change nothing and record nothing.
	ta.json("POST", "/api/trips", map[string]any{"date": "2026-11-15", "start": "10:00"}, http.StatusBadRequest, nil)
	ta.json("PATCH", "/api/trips/manual:999", map[string]any{"enabled": false}, http.StatusNotFound, nil)
	ta.json("POST", "/api/places", map[string]any{"name": "School", "address": "Elsewhere"}, http.StatusBadRequest, nil)
	s["chargingEfficiencyPct"] = 120.0
	ta.json("PUT", "/api/settings", s, http.StatusBadRequest, nil)
	ta.json("POST", "/api/evcc/plan", map[string]any{"soc": 42, "time": "2026-11-10T06:48:00Z"}, http.StatusBadRequest, nil)
	log.none("rejected requests")

	// Reads record nothing either (the demo routes are stubbed).
	ta.plan()
	ta.do("GET", "/api/recurring", nil)
	log.none("reads")
}

func TestAdviceChangesAreRecordedWithTheirSource(t *testing.T) {
	ta := newTestApp(t, "")
	ta.tripPlans(model.TripPlansManual) // extra plans restore the earlier plan
	log := &eventLog{ta: ta}
	log.fresh()
	ta.json("POST", "/api/evcc/plan", map[string]any{"soc": 50, "time": "2026-11-11T06:00:00Z", "confirm": true}, http.StatusOK, nil)
	log.one("plan.send", model.SourceUI)

	var p struct{ ReadyBy, ExpiresAt string }
	ta.json("GET", "/api/advice/2026-11-10/proposal?kind=extra_plan", nil, http.StatusOK, &p)
	log.none("opening a proposal")
	var a adjustment
	ta.json("POST", "/api/adjustments", map[string]any{
		"kind": "extra_plan", "date": "2026-11-10", "soc": 70, "readyBy": p.ReadyBy, "expiresAt": p.ExpiresAt, "confirm": true,
	}, http.StatusCreated, &a)
	e := log.one("adjustment.create", model.SourceUI)
	changed(t, e, "evccPlan", "50% by 2026-11-11 07:00", "70% by 2026-11-10 07:48")
	if e.SubjectID != itoa(a.ID) || e.Related != "plan" || e.Detail["recommendation"] != "charge_ahead" {
		t.Errorf("extra plan = %+v", e)
	}

	// The scheduled job restores the previous plan at expiry.
	ta.at(time.Date(2026, 11, 10, 7, 0, 0, 0, ams))
	ta.poll()
	log.none("checking before expiry")
	ta.at(time.Date(2026, 11, 10, 7, 48, 0, 0, ams))
	ta.poll()
	e = log.one("adjustment.end", model.SourceJob)
	changed(t, e, "evccPlan", "70% by 2026-11-10 07:48", "50% by 2026-11-11 07:00")
	if !strings.HasPrefix(e.Summary, "Extra plan ended: Restored the previous plan") || e.Detail["status"] != "restored" {
		t.Errorf("restore = %+v", e)
	}

	// The plan's history links the submissions and the extra plan.
	var actions []string
	for _, e := range ta.activity("?subject=plan").Events {
		actions = append(actions, e.Action)
	}
	if strings.Join(actions, ",") != "adjustment.end,adjustment.create,plan.send" {
		t.Errorf("plan history = %v", actions)
	}

	// Automatic plans are recorded as automation; ending one in the UI as UI.
	var as map[string]any
	ta.json("GET", "/api/settings/advice", nil, http.StatusOK, &as)
	as["autoApply"], as["autoPostpone"], as["autoMinConfidence"] = true, true, 2
	as["targetLittle"], as["targetMedium"], as["targetHigh"], as["readyBy"], as["expiryHours"] = 60, 70, 80, "06:00", 1
	ta.json("PUT", "/api/settings/advice", as, http.StatusOK, nil)
	log.one("settings.update", model.SourceUI)
	ta.at(time.Date(2026, 11, 10, 9, 0, 0, 0, ams))
	if err := ta.app.Service.AutoApply(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, e := range log.fresh() {
		if e.Action != "adjustment.create" || e.Source != model.SourceAutomation {
			t.Errorf("auto-apply = %+v", e)
		}
	}
}

// failingEVCC refuses every write.
type failingEVCC struct{ evcc.Client }

func (failingEVCC) SetSocPlan(context.Context, string, int, time.Time) (*evcc.SocPlan, error) {
	return nil, errors.New("evcc refused the plan")
}

func TestFailedEVCCWriteIsRecorded(t *testing.T) {
	ta := newTestApp(t, "")
	log := &eventLog{ta: ta}
	ta.app.Service.UseIntegrations(func(i *service.Integrations) { i.EVCC = failingEVCC{i.EVCC} })
	code, _ := ta.do("POST", "/api/evcc/plan", map[string]any{"soc": 42, "time": "2026-11-10T06:48:00Z", "confirm": true})
	if code < 500 {
		t.Fatalf("status = %d", code)
	}
	got := log.fresh()
	if len(got) != 1 || got[0].Action != "plan.send" || got[0].Outcome != model.OutcomeFailure || got[0].Error != "evcc refused the plan" {
		t.Fatalf("events = %+v", got)
	}
	if w, _ := ta.app.Store.LastPlanWrite(); w != nil {
		t.Errorf("a failed write must not be recorded as sent: %+v", w)
	}
}

func TestActivityFiltersExportAndCleanup(t *testing.T) {
	ta := newTestApp(t, "")
	ta.json("POST", "/api/places", map[string]any{"name": "Pool", "address": "Badweg 1"}, http.StatusCreated, nil)
	ta.json("PATCH", "/api/trips/recurring:2:2026-11-12", map[string]any{"enabled": false}, http.StatusOK, nil)
	ta.json("PUT", "/api/recurring/2", map[string]any{"title": "Pickup", "destination": "School", "weekdays": []int{1, 2, 3, 4, 5},
		"start": "15:00", "end": "15:15", "enabled": true, "exceptions": []any{}}, http.StatusOK, nil)
	old := model.AuditEvent{At: time.Date(2026, 7, 1, 12, 0, 0, 0, ams), Type: model.EventAction, Source: model.SourceJob,
		Action: "place.update", SubjectType: "place", SubjectID: "9", Summary: "Old change", Outcome: model.OutcomeSuccess}
	if _, err := ta.app.Store.AddAuditEvent(old); err != nil {
		t.Fatal(err)
	}

	count := func(query string) int { t.Helper(); return len(ta.activity(query).Events) }
	for query, want := range map[string]int{
		"":                                     4,
		"?type=place":                          2,
		"?type=schedule":                       1,
		"?source=job":                          1,
		"?source=ui":                           3,
		"?from=2026-11-09":                     3,
		"?to=2026-07-01":                       1,
		"?subject=schedule:2":                  2, // the schedule and one of its dates
		"?subject=trip:recurring:2:2026-11-12": 1,
	} {
		if got := count(query); got != want {
			t.Errorf("%q: %d events, want %d", query, got, want)
		}
	}
	for _, bad := range []string{"?type=nope", "?source=cron", "?from=09-11-2026", "?subject=../x", "?before=x", "?limit=0"} {
		ta.json("GET", "/api/activity"+bad, nil, http.StatusBadRequest, nil)
	}
	first := ta.activity("?limit=3")
	if len(first.Events) != 3 || first.Next == nil || first.RetentionDays != 90 {
		t.Fatalf("first page = %+v", first)
	}
	rest := ta.activity("?limit=3&before=" + itoa(*first.Next))
	// Newest recorded first: the back-dated event was recorded last.
	if len(rest.Events) != 1 || rest.Next != nil || rest.Events[0].Action != "place.create" {
		t.Fatalf("second page = %+v", rest)
	}

	req := httptest.NewRequest("GET", "/api/activity/export?format=csv&source=ui", nil)
	rec := httptest.NewRecorder()
	ta.app.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/csv") ||
		rec.Header().Get("Content-Disposition") != `attachment; filename="evcc-planner-activity-2026-11-09.csv"` {
		t.Fatalf("csv export = %d %v", rec.Code, rec.Header())
	}
	rows, err := csv.NewReader(rec.Body).ReadAll()
	if err != nil || len(rows) != 4 || rows[0][1] != "time" || rows[1][4] != "schedule.update" || !strings.HasSuffix(rows[1][1], "+01:00") {
		t.Fatalf("csv rows = %v, %v", rows, err)
	}
	var exported []activityEvent
	ta.json("GET", "/api/activity/export?format=json&type=place", nil, http.StatusOK, &exported)
	if len(exported) != 2 {
		t.Errorf("json export = %+v", exported)
	}
	ta.json("GET", "/api/activity/export?format=xml", nil, http.StatusBadRequest, nil)

	// The daily cleanup removes history older than 90 days and records it.
	if err := ta.app.Service.Cleanup(audit.WithSource(context.Background(), model.SourceJob)); err != nil {
		t.Fatal(err)
	}
	page := ta.activity("")
	if len(page.Events) != 4 || page.Events[0].Action != "retention.cleanup" || page.Events[0].Source != model.SourceJob ||
		page.Events[0].Summary != "Deleted history older than 90 days: 1 activity entry" {
		t.Errorf("after cleanup = %+v", page.Events)
	}
	if err := ta.app.Service.Cleanup(context.Background()); err != nil || len(ta.activity("").Events) != 4 {
		t.Errorf("a cleanup with nothing to delete records nothing: %v", err)
	}
}

const testFeed = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nX-WR-CALNAME:Family\r\n" +
	"BEGIN:VEVENT\r\nUID:dentist@x\r\nDTSTART;TZID=Europe/Amsterdam:20261111T090000\r\n" +
	"DTEND;TZID=Europe/Amsterdam:20261111T100000\r\nSUMMARY:Dentist\r\nLOCATION:Tandartsweg 3\\, Demo Town\r\nEND:VEVENT\r\n" +
	"END:VCALENDAR\r\n"

func TestExternalCallsAreRecordedWithoutSecrets(t *testing.T) {
	var googleCalls atomic.Int32
	google := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		googleCalls.Add(1)
		w.Write([]byte(`{"routes":[{"distanceMeters":12000,"duration":"900s"}]}`))
	}))
	defer google.Close()
	feeds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "private-SECRETCAL1") {
			w.Write([]byte(testFeed))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer feeds.Close()
	openrouter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"message":"invalid key"}}`))
	}))
	defer openrouter.Close()
	outlook, err := os.ReadFile(filepath.Join("..", "forecast", "testdata", "knmi-outlook.xml"))
	if err != nil {
		t.Fatal(err)
	}
	var knmi *httptest.Server
	knmi = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/files"):
			w.Write([]byte(`{"files":[{"filename":"outlook.xml","created":"2026-11-09T10:00:00Z"}]}`))
		case strings.HasSuffix(r.URL.Path, "/url"):
			w.Write([]byte(`{"temporaryDownloadUrl":"` + knmi.URL + `/download/outlook.xml?X-Amz-Signature=SIGSECRET42"}`))
		default:
			w.Write(outlook)
		}
	}))
	defer knmi.Close()

	dir := t.TempDir()
	yaml := "fixed_time: " + fixedNow + "\ndata_dir: " + filepath.Join(dir, "data") +
		"\nevcc:\n  mode: stub\ntibber:\n  mode: stub\nroutes:\n  base_url: " + google.URL +
		"\nopenrouter:\n  base_url: " + openrouter.URL + "\nknmi:\n  base_url: " + knmi.URL + "\n"
	cfgPath := filepath.Join(dir, "config.yaml")
	os.WriteFile(cfgPath, []byte(yaml), 0o600)
	env := map[string]string{
		"GOOGLE_MAPS_API_KEY":      "AIzaTESTKEY123",
		"OPENROUTER_API_KEY":       "sk-or-TESTSECRET",
		"KNMI_API_KEY":             "knmi-TESTSECRET",
		"GOOGLE_CALENDAR_ICAL_URL": feeds.URL + "/calendar/ical/a/private-SECRETCAL1/basic.ics," + feeds.URL + "/calendar/ical/b/private-SECRETCAL2/basic.ics",
	}
	cfg, err := config.Load(config.Options{Path: cfgPath, Getenv: func(k string) string { return env[k] }})
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	ta := &testApp{t: t, app: a, dir: dir}
	log := &eventLog{ta: ta}

	// Calendar feeds, from the hourly sync: one works, one fails.
	a.Syncer.SyncAll(audit.WithSource(context.Background(), model.SourceCalendarSync))
	feedsLog := log.fresh()
	if len(feedsLog) != 2 {
		t.Fatalf("calendar calls = %+v", feedsLog)
	}
	for _, e := range feedsLog {
		if e.Type != model.EventAPICall || e.Service != "calendar" || e.Source != model.SourceCalendarSync || !strings.HasPrefix(e.Summary, "Calendar feed: Calendar ") {
			t.Errorf("calendar call = %+v", e)
		}
	}
	if feedsLog[0].Outcome != model.OutcomeSuccess || feedsLog[0].Bytes != int64(len(testFeed)) ||
		feedsLog[1].Outcome != model.OutcomeFailure || feedsLog[1].Error != "HTTP 500 Internal Server Error" {
		t.Errorf("calendar outcomes = %+v", feedsLog)
	}
	// The next sync names the calendar from its feed.
	a.Syncer.SyncAll(audit.WithSource(context.Background(), model.SourceCalendarSync))
	if e := log.fresh(); len(e) != 2 || e[0].Summary != "Calendar feed: Family" {
		t.Errorf("second sync = %+v", e)
	}

	// Route lookups for the plan: real calls only, not cache hits.
	ta.json("PUT", "/api/settings", map[string]any{
		"homeAddress": "Thuisweg 1, Demo Town", "usableCapacityKWh": 59, "chargePowerKW": 3.68, "consumptionKWhPer100Km": 18,
		"arrivalReservePct": 15, "chargingEfficiencyPct": 90, "departureBufferMin": 15, "chargeBufferMin": 30, "defaultStayMin": 15,
	}, http.StatusOK, nil)
	log.one("settings.update", model.SourceUI)
	ta.json("POST", "/api/trips", map[string]any{"destination": "Office Park 38", "date": "2026-11-10", "start": "09:30"}, http.StatusCreated, nil)
	log.one("trip.create", model.SourceUI)
	// The first plan guesses each departure's time slot; the second looks
	// up the slot that follows from the real travel time; then all are cached.
	ta.plan()
	ta.plan()
	routes := log.fresh()
	if len(routes) == 0 || len(routes) != int(googleCalls.Load()) {
		t.Fatalf("route events = %d, provider calls = %d", len(routes), googleCalls.Load())
	}
	for _, e := range routes {
		if e.Service != "google_routes" || e.Source != model.SourceUI || e.Outcome != model.OutcomeSuccess ||
			!strings.HasPrefix(e.Summary, "Route ") || e.Detail["slot"] == nil || e.Detail["from"] == nil {
			t.Errorf("route call = %+v", e)
		}
	}
	ta.plan()
	log.none("cached routes")

	// Forecasts from the daily job: a failing OpenRouter call and KNMI's three requests.
	a.Service.RefreshForecasts(context.Background(), true)
	var labels []string
	for _, e := range log.fresh() {
		if e.Source != model.SourceJob {
			t.Errorf("forecast call source = %+v", e)
		}
		labels = append(labels, e.Service+" "+e.Outcome)
	}
	if strings.Join(labels, ",") != "openrouter failure,knmi success,knmi success,knmi success" {
		t.Errorf("forecast calls = %v", labels)
	}

	var usage struct {
		Month    string `json:"month"`
		Services []struct {
			Service   string `json:"service"`
			Calls     int    `json:"calls"`
			Failures  int    `json:"failures"`
			FreeCalls int    `json:"freeCalls"`
			Live      bool   `json:"live"`
		} `json:"services"`
	}
	ta.json("GET", "/api/activity/usage", nil, http.StatusOK, &usage)
	got := map[string][4]int{}
	for _, s := range usage.Services {
		live := 0
		if s.Live {
			live = 1
		}
		got[s.Service] = [4]int{s.Calls, s.Failures, s.FreeCalls, live}
	}
	want := map[string][4]int{
		"google_routes": {len(routes), 0, 5000, 1}, "calendar": {4, 2, 0, 1}, "tibber": {0, 0, 0, 0},
		"openrouter": {1, 1, 0, 1}, "knmi": {3, 0, 0, 1},
	}
	if usage.Month != "2026-11" || len(got) != 5 {
		t.Errorf("usage = %+v", usage)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s usage = %v, want %v", k, got[k], v)
		}
	}
	var prev struct{ Services []struct{ Calls int } }
	ta.json("GET", "/api/activity/usage?month=2026-10", nil, http.StatusOK, &prev)
	for _, s := range prev.Services {
		if s.Calls != 0 {
			t.Errorf("October usage = %+v", prev)
		}
	}
	ta.json("GET", "/api/activity/usage?month=11-2026", nil, http.StatusBadRequest, nil)

	// No secret is stored in any event.
	for _, format := range []string{"json", "csv"} {
		_, body := ta.do("GET", "/api/activity/export?format="+format, nil)
		for _, secret := range []string{"AIzaTESTKEY123", "sk-or-TESTSECRET", "knmi-TESTSECRET", "SECRETCAL1", "SECRETCAL2", "SIGSECRET42", "basic.ics"} {
			if strings.Contains(string(body), secret) {
				t.Errorf("%s export contains %q", format, secret)
			}
		}
	}
	raw, _ := json.Marshal(ta.activity("?limit=500"))
	if strings.Contains(string(raw), "private-") {
		t.Errorf("activity shows a secret calendar path: %s", raw)
	}
}
