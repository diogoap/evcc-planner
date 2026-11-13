// Package model holds the domain types shared by storage, planning, and the
// HTTP API.
package model

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
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
	// TripPlans is how trip plans reach EVCC: manual, plug_in (once when
	// the car is plugged in), or automatic (kept in line with the trips).
	// See AUTOMATION.md.
	TripPlans string `json:"tripPlans"`
	// ChargeAhead adds later trips to the automatic target when charging
	// for them before the next departure is cheaper by at least
	// ChargeAheadMinSaving (EUR/kWh), up to ChargeAheadMaxSoC.
	ChargeAhead          bool    `json:"chargeAhead"`
	ChargeAheadMinSaving float64 `json:"chargeAheadMinSaving"`
	ChargeAheadMaxSoC    int     `json:"chargeAheadMaxSoc"`
	// HouseholdKW is subtracted from the solar forecast to estimate the
	// surplus the car can use.
	HouseholdKW float64 `json:"householdKW"`
	// SolarTaxSaving is the tax (EUR/kWh) not paid on solar energy used at
	// home: a solar kWh counts as the grid price at that time minus it.
	SolarTaxSaving float64 `json:"solarTaxSaving"`
}

// Trip plan modes (Settings.TripPlans).
const (
	TripPlansManual    = "manual"
	TripPlansPlugIn    = "plug_in"
	TripPlansAutomatic = "automatic"
)

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
		{"minimum saving", s.ChargeAheadMinSaving, 0, 1},
		{"highest target when charging ahead", float64(s.ChargeAheadMaxSoC), 50, 100},
		{"household use", s.HouseholdKW, 0, 10},
		{"tax not paid on own solar", s.SolarTaxSaving, 0, 1},
	}
	for _, c := range checks {
		if c.v < c.lo || c.v > c.h {
			return fmt.Errorf("%s must be between %g and %g", c.name, c.lo, c.h)
		}
	}
	switch s.TripPlans {
	case TripPlansManual, TripPlansPlugIn, TripPlansAutomatic:
	default:
		return fmt.Errorf("trip plans must be %q, %q, or %q", TripPlansManual, TripPlansPlugIn, TripPlansAutomatic)
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

// SoC reading sources. Each keeps its last reading.
const (
	SoCSourceEVCC          = "evcc"          // only while the car is plugged in
	SoCSourceHomeAssistant = "homeassistant" // also while it is unplugged
)

// SoCReading is the last state of charge read from one source.
type SoCReading struct {
	Percent    float64
	RangeKm    *float64 // the source's range estimate, when it reports one
	ObservedAt time.Time
	// ChangedAt is when the value last changed, if the source says
	// (Home Assistant); zero means ObservedAt.
	ChangedAt time.Time
	Source    string
}

// Changed is when the reading's value last changed.
func (r SoCReading) Changed() time.Time {
	if r.ChangedAt.IsZero() || r.ChangedAt.After(r.ObservedAt) {
		return r.ObservedAt
	}
	return r.ChangedAt
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

// AdviceSettings control daily charging advice. Advice is advisory: plans are
// only created automatically when AutoApply is on and the user has set a
// concrete target for each charge indicator, a ready-by time, and an expiry.
type AdviceSettings struct {
	Enabled           bool   `json:"enabled"`
	AutoApply         bool   `json:"autoApply"`
	AutoMinConfidence int    `json:"autoMinConfidence"` // 2 medium or 3 high
	TargetLittle      *int   `json:"targetLittle"`      // SoC % per charge indicator
	TargetMedium      *int   `json:"targetMedium"`
	TargetHigh        *int   `json:"targetHigh"`
	ReadyBy           string `json:"readyBy"`     // local HH:MM on the advice day
	ExpiryHours       *int   `json:"expiryHours"` // after the ready-by time
	AutoPostpone      bool   `json:"autoPostpone"`
}

// DefaultAdviceSettings shows advice and never acts on it.
func DefaultAdviceSettings() AdviceSettings {
	return AdviceSettings{Enabled: true, AutoMinConfidence: 3}
}

// Validate reports the first invalid field.
func (a AdviceSettings) Validate() error {
	if a.AutoMinConfidence != 2 && a.AutoMinConfidence != 3 {
		return fmt.Errorf("minimum confidence must be 2 (medium) or 3 (high)")
	}
	prev := 0
	for _, t := range []struct {
		name string
		v    *int
	}{{"little", a.TargetLittle}, {"medium", a.TargetMedium}, {"high", a.TargetHigh}} {
		if t.v == nil {
			continue
		}
		if *t.v < 10 || *t.v > 100 {
			return fmt.Errorf("the %s target must be between 10 and 100%%", t.name)
		}
		if *t.v < prev {
			return fmt.Errorf("targets must not decrease from little to high")
		}
		prev = *t.v
	}
	if a.ReadyBy != "" {
		if _, err := time.Parse("15:04", a.ReadyBy); err != nil {
			return fmt.Errorf("ready-by time must be HH:MM")
		}
	}
	if a.ExpiryHours != nil && (*a.ExpiryHours < 0 || *a.ExpiryHours > 48) {
		return fmt.Errorf("expiry must be 0 to 48 hours after the ready-by time")
	}
	if a.AutoApply && (a.TargetLittle == nil || a.TargetMedium == nil || a.TargetHigh == nil || a.ReadyBy == "" || a.ExpiryHours == nil) {
		return fmt.Errorf("automatic plans need a target for each charge indicator, a ready-by time, and an expiry")
	}
	return nil
}

// Target returns the configured SoC for a charge indicator.
func (a AdviceSettings) Target(indicator string) (int, bool) {
	var v *int
	switch indicator {
	case "little":
		v = a.TargetLittle
	case "medium":
		v = a.TargetMedium
	case "high":
		v = a.TargetHigh
	}
	if v == nil {
		return 0, false
	}
	return *v, true
}

// Adjustment kinds and statuses.
const (
	AdjustExtraPlan = "extra_plan" // a temporary one-off SoC plan (charge ahead)
	AdjustPostpone  = "postpone"   // cheap-grid charging paused (defer)

	AdjustActive    = "active"
	AdjustRestored  = "restored"  // previous value written back
	AdjustKept      = "kept"      // changed in EVCC meanwhile; left as is
	AdjustCancelled = "cancelled" // ended early by the user; previous value restored
	AdjustFailed    = "failed"
)

// PlanValue is an EVCC one-off SoC plan.
type PlanValue struct {
	SoC  float64   `json:"soc"`
	Time time.Time `json:"time"`
}

// PlanControl is the state of automatic trip plans (AUTOMATION.md): the
// plan the planner last wrote to EVCC, and a pause while a plan someone
// chose covers the next departure.
type PlanControl struct {
	Managed *PlanValue `json:"managed,omitempty"`
	// Ahead lists the first trip key of each later trip that Managed
	// charges ahead, so they stay included while still cheaper.
	Ahead      []string   `json:"ahead,omitempty"`
	PausedAt   *time.Time `json:"pausedAt,omitempty"`
	PausedPlan *PlanValue `json:"pausedPlan,omitempty"`
}

// Paused reports whether a chosen plan has paused automatic changes.
func (c PlanControl) Paused() bool { return c.PausedAt != nil }

// NotificationSettings choose which phone notifications are sent and when
// (AUTOMATION.md). Notifications go through the Home Assistant Companion
// app.
type NotificationSettings struct {
	Enabled bool     `json:"enabled"`
	Phones  []string `json:"phones"` // Home Assistant notify services, e.g. mobile_app_phone
	// LinkURL is the planner's address as the phone reaches it, for View
	// plan; empty leaves that button out.
	LinkURL string `json:"linkUrl"`
	// LocationEntity, if set, is a Home Assistant entity whose state is
	// "home" while the car is home; reminders wait while it is elsewhere.
	LocationEntity string `json:"locationEntity"`
	Early          bool   `json:"early"`
	EarlyLeadMin   int    `json:"earlyLeadMin"`
	UrgentLeadMin  int    `json:"urgentLeadMin"`
	QuietFrom      string `json:"quietFrom"` // local HH:MM; equal to QuietTo: no quiet hours
	QuietTo        string `json:"quietTo"`
	UrgentInQuiet  bool   `json:"urgentInQuiet"`
	SnoozeMin      int    `json:"snoozeMin"`
	Problems       bool   `json:"problems"`
	Questions      bool   `json:"questions"`
	Ready          bool   `json:"ready"`
	// Solar asks to plug in when today's advice is to charge ahead from the
	// sun and the car is home but unplugged.
	Solar bool `json:"solar"`
}

// DefaultNotificationSettings are off, with the reminder rules ready.
func DefaultNotificationSettings() NotificationSettings {
	return NotificationSettings{
		Phones: []string{}, Early: true, EarlyLeadMin: 30, UrgentLeadMin: 30, QuietFrom: "23:00", QuietTo: "07:00",
		UrgentInQuiet: true, SnoozeMin: 30, Problems: true, Questions: true, Solar: true,
	}
}

var (
	phoneRe  = regexp.MustCompile(`^mobile_app_[a-z0-9_]{1,100}$`)
	entityRe = regexp.MustCompile(`^[a-z_]+\.[a-z0-9_]{1,200}$`)
)

// ValidPhone reports whether name is a Home Assistant mobile app notify
// service.
func ValidPhone(name string) bool { return phoneRe.MatchString(name) }

// Validate reports the first invalid field.
func (n NotificationSettings) Validate() error {
	for _, c := range []struct {
		name     string
		v, lo, h int
	}{{"early reminder", n.EarlyLeadMin, 0, 240}, {"urgent reminder", n.UrgentLeadMin, 0, 240}, {"snooze", n.SnoozeMin, 5, 240}} {
		if c.v < c.lo || c.v > c.h {
			return fmt.Errorf("%s must be between %d and %d minutes", c.name, c.lo, c.h)
		}
	}
	for _, t := range []string{n.QuietFrom, n.QuietTo} {
		if _, err := time.Parse("15:04", t); err != nil {
			return fmt.Errorf("quiet hours must be HH:MM")
		}
	}
	for _, p := range n.Phones {
		if !ValidPhone(p) {
			return fmt.Errorf("%q is not a Home Assistant mobile app (notify.mobile_app_…)", p)
		}
	}
	if n.Enabled && len(n.Phones) == 0 {
		return fmt.Errorf("choose at least one phone to turn notifications on")
	}
	if n.LinkURL != "" {
		if u, err := url.Parse(n.LinkURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("the link address must start with http:// or https://")
		}
	}
	if n.LocationEntity != "" && !entityRe.MatchString(n.LocationEntity) {
		return fmt.Errorf("the car location sensor must be an entity ID, e.g. device_tracker.car")
	}
	return nil
}

// Notification kinds, states, and button verbs.
const (
	NotifyTest      = "test"
	NotifyEarly     = "early"
	NotifyUrgent    = "urgent"
	NotifyLate      = "late"
	NotifyStopped   = "stopped"
	NotifyUnplugged = "unplugged"
	NotifyMode      = "mode" // EVCC not in Smart mode while plugged in
	NotifyMissed    = "missed"
	NotifyQuestion  = "question"
	NotifyReady     = "ready"
	NotifySolar     = "solar" // plug in to charge ahead from the sun
	NotifyInfo      = "info"  // a follow-up, e.g. after Skip trip

	NotifySent    = "sent"
	NotifySnoozed = "snoozed"
	NotifyDone    = "done"
	NotifyFailed  = "failed"

	VerbSnooze = "snooze"
	VerbSkip   = "skip"
	VerbView   = "view"
	VerbCount  = "count"
	VerbNoTrip = "notrip"
)

// Notification is a message to the phones, kept to avoid duplicates, to
// snooze it, and to act on its buttons.
type Notification struct {
	ID      int64  `json:"id"`
	Key     string `json:"key"` // kind and what it is about, unique
	Kind    string `json:"kind"`
	Subject string `json:"subject,omitempty"` // the trip it is about
	Title   string `json:"title"`
	Message string `json:"message"`
	// Verbs are the buttons, in order.
	Verbs  []string `json:"verbs"`
	Urgent bool     `json:"urgent"`
	State  string   `json:"state"`
	Note   string   `json:"note,omitempty"` // what happened to it
	// Latest bounds snoozes, e.g. the latest plug-in time.
	Latest       *time.Time `json:"latest,omitempty"`
	CreatedAt    time.Time  `json:"createdAt"`
	SentAt       *time.Time `json:"sentAt,omitempty"`
	SnoozedUntil *time.Time `json:"snoozedUntil,omitempty"`
	ResolvedAt   *time.Time `json:"resolvedAt,omitempty"`
}

// Open reports whether the notification still asks for something.
func (n Notification) Open() bool { return n.State == NotifySent || n.State == NotifySnoozed }

// Adjustment is a temporary EVCC change the app made from daily advice. When
// it expires (or is cancelled) the previous value is restored, unless it was
// changed in EVCC in the meantime.
type Adjustment struct {
	ID         int64      `json:"id"`
	Kind       string     `json:"kind"`
	Source     string     `json:"source"` // ui or auto
	AdviceDate string     `json:"adviceDate"`
	Vehicle    string     `json:"vehicle"`
	Loadpoint  int        `json:"loadpoint"`
	Plan       *PlanValue `json:"plan,omitempty"`     // extra plan: what the app set
	PrevPlan   *PlanValue `json:"prevPlan,omitempty"` // extra plan: what it replaced
	// Postpone: the cheap-grid charging limit set (nil = removed) and the
	// one it replaced.
	SmartCostLimit     *float64   `json:"smartCostLimit,omitempty"`
	PrevSmartCostLimit *float64   `json:"prevSmartCostLimit,omitempty"`
	ExpiresAt          time.Time  `json:"expiresAt"`
	Status             string     `json:"status"`
	Note               string     `json:"note"`
	CreatedAt          time.Time  `json:"createdAt"`
	EndedAt            *time.Time `json:"endedAt,omitempty"`
}

// Activity log event types, sources, and outcomes.
const (
	EventAction  = "action"   // a change made (or attempted) by the planner
	EventAPICall = "api_call" // a request to an external service

	SourceUI           = "ui"
	SourceJob          = "job" // scheduled job, e.g. restoring an EVCC setting at expiry
	SourceCalendarSync = "calendar_sync"
	SourceAutomation   = "automation"   // e.g. plans created automatically from advice
	SourceNotification = "notification" // a button pressed on a phone notification

	OutcomeSuccess = "success"
	OutcomeFailure = "failure"
)

// AuditEvent is one entry of the append-only activity log. Before and After
// hold the values that changed (a creation has only After, a deletion only
// Before). Secrets are redacted before an event is stored.
type AuditEvent struct {
	ID          int64           `json:"id"`
	At          time.Time       `json:"at"`
	Type        string          `json:"type"`
	Source      string          `json:"source"`
	Action      string          `json:"action"`      // e.g. trip.disable, plan.send, api.call
	SubjectType string          `json:"subjectType"` // trip, schedule, place, settings, plan, adjustment, advice, api
	SubjectID   string          `json:"subjectId,omitempty"`
	Related     string          `json:"related,omitempty"` // another subject whose history includes this event, e.g. schedule:3
	Summary     string          `json:"summary"`
	Before      json.RawMessage `json:"before,omitempty"`
	After       json.RawMessage `json:"after,omitempty"`
	Detail      json.RawMessage `json:"detail,omitempty"`
	Outcome     string          `json:"outcome"`
	Error       string          `json:"error,omitempty"`
	// External API calls only.
	Service    string `json:"service,omitempty"`
	StatusCode int    `json:"statusCode,omitempty"`
	DurationMs int64  `json:"durationMs,omitempty"`
	Bytes      int64  `json:"bytes,omitempty"`
}
