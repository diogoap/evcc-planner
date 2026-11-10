// Package model holds the domain types shared by storage, planning, and the
// HTTP API.
package model

import (
	"encoding/json"
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
	RangeKm    *float64 // EVCC's range estimate, when it reports one
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
	SourceAutomation   = "automation" // e.g. plans created automatically from advice

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
