package service

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/diogoap/evcc-planner/internal/audit"
	"github.com/diogoap/evcc-planner/internal/config"
	"github.com/diogoap/evcc-planner/internal/localtime"
	"github.com/diogoap/evcc-planner/internal/model"
	"github.com/diogoap/evcc-planner/internal/store"
	"github.com/diogoap/evcc-planner/internal/trips"
)

// ---- recording ----

// change runs fn and stores the event in the same transaction, so a change
// and its activity entry are saved together or not at all. fn completes
// the event (IDs, values, summary) and must use only tx. Input problems
// (validation, unknown records) are not recorded; other failures are
// recorded as failed actions.
func (s *Service) change(ctx context.Context, ev model.AuditEvent, fn func(tx *store.Store, e *model.AuditEvent) error) error {
	ev.Type = model.EventAction
	ev.At = s.now()
	err := s.Store.InTx(func(tx *store.Store) error {
		if err := fn(tx, &ev); err != nil {
			return err
		}
		ev.Outcome = model.OutcomeSuccess
		_, err := tx.AddAuditEvent(s.Audit.Prepare(ctx, ev))
		return err
	})
	var ve ValidationError
	if err != nil && !errors.As(err, &ve) && !IsNotFound(err) {
		ev.Outcome, ev.Error = model.OutcomeFailure, err.Error()
		s.record(ctx, ev)
	}
	return err
}

// record stores an action that changed nothing in the database, typically
// a failed EVCC write.
func (s *Service) record(ctx context.Context, ev model.AuditEvent) {
	if ev.Type == "" {
		ev.Type = model.EventAction
	}
	if ev.At.IsZero() {
		ev.At = s.now()
	}
	s.Audit.Record(ctx, ev)
}

// changes returns the fields that differ between two records, as JSON
// objects. With one side nil (a creation or deletion) the other is
// returned in full.
func changes(before, after any) (json.RawMessage, json.RawMessage) {
	b, a := toMap(before), toMap(after)
	if b == nil || a == nil {
		return audit.Marshal(before), audit.Marshal(after)
	}
	db, da := map[string]any{}, map[string]any{}
	for k := range b {
		if !reflect.DeepEqual(b[k], a[k]) {
			db[k], da[k] = b[k], a[k]
		}
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			db[k], da[k] = nil, a[k]
		}
	}
	return audit.Marshal(db), audit.Marshal(da)
}

func toMap(v any) map[string]any {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return m
}

// fieldList names the changed fields of a diff, e.g. "title, arrive-by time".
func fieldList(diff json.RawMessage, labels map[string]string) string {
	var m map[string]any
	if json.Unmarshal(diff, &m) != nil || len(m) == 0 {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	names := make([]string, len(keys))
	for i, k := range keys {
		names[i] = k
		if l, ok := labels[k]; ok {
			names[i] = l
		}
	}
	return strings.Join(names, ", ")
}

// localText is how instants appear in recorded values: local and sortable.
func (s *Service) localText(t time.Time) string { return t.In(s.loc()).Format("2006-01-02 15:04") }

func (s *Service) dayText(t time.Time) string { return t.In(s.loc()).Format("Mon 2 Jan") }

func (s *Service) dateText(date string) string {
	d, err := localtime.ParseDate(s.loc(), date)
	if err != nil {
		return date
	}
	return s.dayText(d)
}

// ---- recorded values ----

// tripRecord is a trip as the activity log shows it: what a user can change.
type tripRecord struct {
	Title            string   `json:"title"`
	Destination      string   `json:"destination"`
	ArriveBy         string   `json:"arriveBy"`
	Until            string   `json:"until"`
	Enabled          bool     `json:"enabled"`
	ManualDistanceKm *float64 `json:"manualDistanceKm"`
	NeedsReview      bool     `json:"needsReview"`
}

var tripLabels = map[string]string{
	"title": "title", "destination": "destination", "arriveBy": "arrive-by time", "until": "end time",
	"enabled": "on/off", "manualDistanceKm": "manual distance", "needsReview": "review",
}

func (s *Service) tripRecord(t trips.Trip) tripRecord {
	return tripRecord{
		Title: t.Title, Destination: t.Destination, ArriveBy: s.localText(t.Start), Until: s.localText(t.End),
		Enabled: t.Enabled, ManualDistanceKm: t.DistanceOverrideKm, NeedsReview: t.NeedsReview,
	}
}

func (s *Service) manualRecord(m model.ManualTrip) tripRecord {
	return tripRecord{
		Title: m.Title, Destination: m.Destination, ArriveBy: s.localText(m.Start), Until: s.localText(m.End),
		Enabled: m.Enabled, ManualDistanceKm: m.DistanceOverrideKm,
	}
}

// relatedSubject links a recurring date's events to its schedule's history.
func relatedSubject(k trips.Key) string {
	if k.Source == trips.SourceRecurring {
		return "schedule:" + strconv.FormatInt(k.ID, 10)
	}
	return ""
}

type scheduleRecord struct {
	Title       string   `json:"title"`
	Destination string   `json:"destination"`
	Weekdays    string   `json:"weekdays"`
	ArriveBy    string   `json:"arriveBy"`
	Until       string   `json:"until"`
	Enabled     bool     `json:"enabled"`
	Exceptions  []string `json:"exceptions"`
}

var weekdayNames = []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}

func scheduleOf(t model.Template) scheduleRecord {
	days := make([]string, 0, len(t.Weekdays))
	for _, d := range t.Weekdays {
		if d >= 1 && d <= 7 {
			days = append(days, weekdayNames[d-1])
		}
	}
	exc := []string{}
	for _, e := range t.Exceptions {
		v := e.From
		if e.To != "" && e.To != e.From {
			v += " to " + e.To
		}
		if e.Note != "" {
			v += " (" + e.Note + ")"
		}
		exc = append(exc, v)
	}
	return scheduleRecord{
		Title: t.Title, Destination: t.Destination, Weekdays: strings.Join(days, ", "), ArriveBy: t.Start, Until: t.End,
		Enabled: t.Enabled, Exceptions: exc,
	}
}

var scheduleLabels = map[string]string{
	"title": "title", "destination": "destination", "weekdays": "weekdays", "arriveBy": "arrive-by time",
	"until": "end time", "enabled": "on/off", "exceptions": "exceptions",
}

type placeRecord struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

var settingLabels = map[string]string{
	"homeAddress": "home address", "usableCapacityKWh": "usable capacity", "chargePowerKW": "charging power",
	"consumptionKWhPer100Km": "consumption", "arrivalReservePct": "arrival reserve",
	"chargingEfficiencyPct": "charging efficiency", "departureBufferMin": "departure buffer",
	"chargeBufferMin": "charging time buffer", "defaultStayMin": "default stay",
	"enabled": "daily advice", "autoApply": "automatic plans", "autoMinConfidence": "minimum confidence",
	"targetLittle": "target for little", "targetMedium": "target for medium", "targetHigh": "target for high",
	"readyBy": "ready-by time", "expiryHours": "expiry", "autoPostpone": "automatic postponement",
}

// planText describes an EVCC one-off plan, or "none".
func (s *Service) planText(soc float64, at time.Time, set bool) string {
	if !set {
		return "none"
	}
	return fmt.Sprintf("%d%% by %s", int(soc+0.5), s.localText(at))
}

func limitText(v *float64) string {
	if v == nil {
		return "none"
	}
	return fmt.Sprintf("€%.2f/kWh", *v)
}

// ---- activity log queries ----

// activityTypes groups subjects into the Activity page's type filter.
var activityTypes = map[string][]string{
	"trip": {"trip"}, "schedule": {"schedule"}, "place": {"place"}, "settings": {"settings"},
	"evcc": {"plan", "adjustment"}, "advice": {"advice"}, "api": {"api"}, "system": {"system"},
}

var activitySources = map[string]bool{
	model.SourceUI: true, model.SourceJob: true, model.SourceCalendarSync: true, model.SourceAutomation: true,
}

var subjectRe = regexp.MustCompile(`^[a-z]+(:[A-Za-z0-9:_.-]{1,120})?$`)

// ActivityQuery filters the activity log; every field is optional. From
// and To are inclusive local dates.
type ActivityQuery struct {
	Type    string
	Source  string
	From    string
	To      string
	Subject string // e.g. trip:manual:3, schedule:2, plan
	Before  int64  // paging cursor from ActivityView.Next
	Limit   int
}

// ActivityView is one page of the activity log, newest first.
type ActivityView struct {
	Events        []model.AuditEvent `json:"events"`
	Next          *int64             `json:"next"` // cursor for older events, if any
	RetentionDays int                `json:"retentionDays"`
}

func (s *Service) activityFilter(q ActivityQuery) (store.AuditFilter, error) {
	f := store.AuditFilter{Source: q.Source, Subject: q.Subject, BeforeID: q.Before}
	if q.Type != "" {
		types, ok := activityTypes[q.Type]
		if !ok {
			return f, invalid("unknown activity type %q", q.Type)
		}
		f.SubjectTypes = types
	}
	if q.Source != "" && !activitySources[q.Source] {
		return f, invalid("unknown source %q", q.Source)
	}
	if q.Subject != "" && !subjectRe.MatchString(q.Subject) {
		return f, invalid("invalid subject")
	}
	if q.From != "" {
		d, err := localtime.ParseDate(s.loc(), q.From)
		if err != nil {
			return f, invalid("from must be YYYY-MM-DD")
		}
		f.From = d
	}
	if q.To != "" {
		d, err := localtime.ParseDate(s.loc(), q.To)
		if err != nil {
			return f, invalid("to must be YYYY-MM-DD")
		}
		f.To = localtime.AddDays(d, 1)
	}
	return f, nil
}

// Activity returns a page of matching activity log events.
func (s *Service) Activity(q ActivityQuery) (*ActivityView, error) {
	f, err := s.activityFilter(q)
	if err != nil {
		return nil, err
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}
	f.Limit = min(limit, 500) + 1
	list, err := s.Store.AuditEvents(f)
	if err != nil {
		return nil, err
	}
	v := &ActivityView{Events: list, RetentionDays: s.Cfg.Retention.Days}
	if len(list) == f.Limit {
		v.Events = list[:len(list)-1]
		next := v.Events[len(v.Events)-1].ID
		v.Next = &next
	}
	return v, nil
}

// exportLimit bounds an export; a year of normal use is far below it.
const exportLimit = 200_000

// ActivityExport returns every matching event, newest first.
func (s *Service) ActivityExport(q ActivityQuery) ([]model.AuditEvent, error) {
	f, err := s.activityFilter(q)
	if err != nil {
		return nil, err
	}
	f.Limit = exportLimit
	return s.Store.AuditEvents(f)
}

// WriteActivityCSV writes events as CSV with local times; before, after,
// and detail stay JSON.
func (s *Service) WriteActivityCSV(w io.Writer, events []model.AuditEvent) error {
	cw := csv.NewWriter(w)
	header := []string{"id", "time", "type", "source", "action", "subject_type", "subject_id", "related", "summary",
		"outcome", "error", "service", "status_code", "duration_ms", "bytes", "before", "after", "detail"}
	if err := cw.Write(header); err != nil {
		return err
	}
	num := func(v int64) string {
		if v == 0 {
			return ""
		}
		return strconv.FormatInt(v, 10)
	}
	for _, e := range events {
		row := []string{
			strconv.FormatInt(e.ID, 10), e.At.In(s.loc()).Format(time.RFC3339), e.Type, e.Source, e.Action,
			e.SubjectType, e.SubjectID, e.Related, e.Summary, e.Outcome, e.Error, e.Service,
			num(int64(e.StatusCode)), num(e.DurationMs), num(e.Bytes),
			string(e.Before), string(e.After), string(e.Detail),
		}
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// ---- external API usage ----

// UsageView is the external API usage of one month.
type UsageView struct {
	Month    string         `json:"month"` // YYYY-MM, local
	Services []ServiceUsage `json:"services"`
}

// ServiceUsage counts one external service's calls in the month.
type ServiceUsage struct {
	Service   string `json:"service"`
	Name      string `json:"name"`
	Calls     int    `json:"calls"`
	Failures  int    `json:"failures"`
	Bytes     int64  `json:"bytes"`
	AvgMs     int64  `json:"avgMs"`
	FreeCalls int    `json:"freeCalls,omitempty"` // monthly free allowance
	Live      bool   `json:"live"`                // false: stubbed or not configured, so no real calls
	Note      string `json:"note,omitempty"`
}

// Service keys used by the auditing transports.
const (
	APIGoogleRoutes = "google_routes"
	APICalendar     = "calendar"
	APITibber       = "tibber"
	APIOpenRouter   = "openrouter"
	APIKNMI         = "knmi"
)

// APIUsage counts external calls per service in a local month (YYYY-MM;
// empty means the current month).
func (s *Service) APIUsage(month string) (*UsageView, error) {
	loc := s.loc()
	now := s.now().In(loc)
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)
	if month != "" {
		m, err := time.ParseInLocation("2006-01", month, loc)
		if err != nil {
			return nil, invalid("month must be YYYY-MM")
		}
		start = m
	}
	end := time.Date(start.Year(), start.Month()+1, 1, 0, 0, 0, 0, loc)
	counts, err := s.Store.APIUsage(start, end)
	if err != nil {
		return nil, err
	}
	f := s.Forecasts
	weatherLive := f.Weather != nil && f.Weather.Name() == "live"
	services := []ServiceUsage{
		{Service: APIGoogleRoutes, Name: "Google Routes", Live: s.Routes.Router.Name() != "stub", FreeCalls: s.Cfg.Routes.MonthlyFreeCalls},
		{Service: APICalendar, Name: "Google Calendar feeds", Live: s.Cfg.Calendar.Mode != config.ModeStub && len(s.Calendar.Calendars) > 0},
		{Service: APITibber, Name: "Tibber outlook page", Live: f.Article.Name() == "live"},
		{Service: APIOpenRouter, Name: "OpenRouter", Live: f.Extractor.Name() == "openrouter"},
		{Service: APIKNMI, Name: "KNMI Open Data", Live: weatherLive},
	}
	for i := range services {
		u := &services[i]
		c := counts[u.Service]
		u.Calls, u.Failures, u.Bytes = c.Calls, c.Failures, c.Bytes
		if c.Calls > 0 {
			u.AvgMs = c.DurationMs / int64(c.Calls)
		}
		if !u.Live {
			u.Note = "Stub or not configured: no real calls"
		}
	}
	return &UsageView{Month: start.Format("2006-01"), Services: services}, nil
}

// ---- retention ----

// cleanupLabels name what Cleanup deletes: singular and plural.
var cleanupLabels = map[string][2]string{
	"activity": {"activity entry", "activity entries"}, "manual_trips": {"past trip", "past trips"},
	"trip_edits":  {"per-date trip edit", "per-date trip edits"},
	"routes":      {"cached route", "cached routes"},
	"plan_writes": {"plan record", "plan records"}, "dismissals": {"advice dismissal", "advice dismissals"},
	"adjustments": {"ended EVCC change", "ended EVCC changes"},
}

// Cleanup deletes history older than the retention period (whole local
// days) and records what it removed. It runs daily.
func (s *Service) Cleanup(ctx context.Context) error {
	days := s.Cfg.Retention.Days
	cutoff := localtime.AddDays(localtime.StartOfDay(s.now(), s.loc()), -days)
	return s.Store.InTx(func(tx *store.Store) error {
		counts, err := tx.Cleanup(cutoff, s.loc())
		if err != nil || len(counts) == 0 {
			return err
		}
		keys := make([]string, 0, len(counts))
		for k := range counts {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			label := cleanupLabels[k][1]
			if counts[k] == 1 {
				label = cleanupLabels[k][0]
			}
			parts[i] = fmt.Sprintf("%d %s", counts[k], label)
		}
		s.Log.Info("old history deleted", "before", s.localText(cutoff), "deleted", strings.Join(parts, ", "))
		_, err = tx.AddAuditEvent(s.Audit.Prepare(ctx, model.AuditEvent{
			Type: model.EventAction, At: s.now(), Action: "retention.cleanup", SubjectType: "system",
			Summary: fmt.Sprintf("Deleted history older than %d days: %s", days, strings.Join(parts, ", ")),
			Detail:  audit.Marshal(map[string]any{"before": s.localText(cutoff), "deleted": counts}),
			Outcome: model.OutcomeSuccess,
		}))
		return err
	})
}
