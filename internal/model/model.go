// Package model holds the domain types shared by storage, planning, and the
// HTTP API.
package model

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Settings are the personal planning choices edited in the UI. Config files
// provide bootstrap defaults; values saved through the UI take precedence.
type Settings struct {
	HomeAddress            string  `json:"homeAddress"`
	UsableCapacityKWh      float64 `json:"usableCapacityKWh"`
	ChargePowerKW          float64 `json:"chargePowerKW"`
	ConsumptionKWhPer100Km float64 `json:"consumptionKWhPer100Km"`
	// ConsumptionUnit is how the consumption is entered and shown; it is
	// always stored in kWh/100 km.
	ConsumptionUnit string `json:"consumptionUnit"`
	// ConsumptionEntity, if set, is a Home Assistant sensor with the car's
	// consumption in ConsumptionEntityUnit. Its last valid value replaces
	// ConsumptionKWhPer100Km in the plan.
	ConsumptionEntity     string  `json:"consumptionEntity"`
	ConsumptionEntityUnit string  `json:"consumptionEntityUnit"`
	ArrivalReservePct     float64 `json:"arrivalReservePct"`
	ChargingEfficiencyPct float64 `json:"chargingEfficiencyPct"`
	DepartureBufferMin    int     `json:"departureBufferMin"`
	ChargeBufferMin       int     `json:"chargeBufferMin"`
	DefaultStayMin        int     `json:"defaultStayMin"`
	// TripPlans is how trip plans reach EVCC: manual, plug_in (once when
	// the car is plugged in), automatic (kept in line with the trips), or ai
	// (like automatic, with an AI model deciding charge ahead, charge extra,
	// and deferring). See AUTOMATION.md.
	TripPlans string `json:"tripPlans"`
	// ChargeAhead adds later trips to the target (all modes but Manual)
	// when charging for them before the next departure is cheaper by at
	// least ChargeAheadMinSaving (EUR/kWh), up to ChargeAheadMaxSoC.
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

// Consumption units: how a consumption value is given.
const (
	UnitKWhPer100Km = "kwh_per_100km"
	UnitKmPerKWh    = "km_per_kwh"
)

// The plausible consumption, in kWh/100 km.
const minConsumption, maxConsumption = 5.0, 60.0

// KWhPer100Km converts a consumption given in unit to kWh/100 km.
func KWhPer100Km(v float64, unit string) float64 {
	if unit == UnitKmPerKWh {
		if v <= 0 {
			return 0
		}
		return 100 / v
	}
	return v
}

// ConsumptionInRange reports whether a consumption in kWh/100 km is
// plausible.
func ConsumptionInRange(kwh float64) bool { return kwh >= minConsumption && kwh <= maxConsumption }

// ConsumptionUnitText is a unit as shown, e.g. km/kWh.
func ConsumptionUnitText(unit string) string {
	if unit == UnitKmPerKWh {
		return "km/kWh"
	}
	return "kWh/100 km"
}

// ConsumptionRangeText is the plausible range in a unit, e.g. 1.7 to 20 km/kWh.
func ConsumptionRangeText(unit string) string {
	if unit == UnitKmPerKWh {
		return fmt.Sprintf("%.1f to %g km/kWh", 100/maxConsumption, 100/minConsumption)
	}
	return fmt.Sprintf("%g to %g kWh/100 km", minConsumption, maxConsumption)
}

// Trip plan modes (Settings.TripPlans).
const (
	TripPlansManual    = "manual"
	TripPlansPlugIn    = "plug_in"
	TripPlansAutomatic = "automatic"
	TripPlansAI        = "ai"
)

// Automates reports whether the planner writes plans on its own: every
// mode but Manual. Charge ahead and acting on advice automatically apply
// then.
func (s Settings) Automates() bool { return s.TripPlans != TripPlansManual }

// KeepsPlan reports whether the planner keeps EVCC's plan in line with the
// trips (automatic and AI mode), with pauses for chosen plans.
func (s Settings) KeepsPlan() bool {
	return s.TripPlans == TripPlansAutomatic || s.TripPlans == TripPlansAI
}

// Validate reports the first out-of-range field.
func (s Settings) Validate() error {
	checks := []struct {
		name     string
		v, lo, h float64
	}{
		{"usable capacity", s.UsableCapacityKWh, 10, 200},
		{"charging power", s.ChargePowerKW, 1, 50},
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
	for _, u := range []string{s.ConsumptionUnit, s.ConsumptionEntityUnit} {
		if u != UnitKWhPer100Km && u != UnitKmPerKWh {
			return fmt.Errorf("consumption units must be %q or %q", UnitKWhPer100Km, UnitKmPerKWh)
		}
	}
	if !ConsumptionInRange(s.ConsumptionKWhPer100Km) {
		return fmt.Errorf("consumption must be from %s", ConsumptionRangeText(s.ConsumptionUnit))
	}
	if s.ConsumptionEntity != "" && !entityRe.MatchString(s.ConsumptionEntity) {
		return fmt.Errorf("the consumption sensor must be an entity ID, e.g. sensor.car_efficiency")
	}
	switch s.TripPlans {
	case TripPlansManual, TripPlansPlugIn, TripPlansAutomatic, TripPlansAI:
	default:
		return fmt.Errorf("trip plans must be %q, %q, %q, or %q", TripPlansManual, TripPlansPlugIn, TripPlansAutomatic, TripPlansAI)
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

// SensorReading is the last valid value of a Home Assistant sensor, such as
// the car's consumption, and when Home Assistant received it.
type SensorReading struct {
	Entity     string
	Value      float64
	ObservedAt time.Time
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
// only created automatically when AutoApply is on, and never in Manual
// mode. AI mode always shows advice and acts on it by its own settings
// (AISettings.AutoApply), so Enabled and the Auto fields don't apply there. An automatic extra plan
// is ready by the end of charge extra's cheap blocks (or the next
// departure) and ends then.
type AdviceSettings struct {
	Enabled           bool `json:"enabled"`
	AutoApply         bool `json:"autoApply"`
	AutoMinConfidence int  `json:"autoMinConfidence"` // 2 medium or 3 high
	AutoPostpone      bool `json:"autoPostpone"`
	// What charge extra does at each indication of cheaper days ahead.
	ExtraLittle ExtraLevel `json:"extraLittle"`
	ExtraMedium ExtraLevel `json:"extraMedium"`
	ExtraHigh   ExtraLevel `json:"extraHigh"`
}

// Indications of cheaper days ahead, from the weekly outlook and the
// wind and sun forecasts.
const (
	IndicationLittle = "little"
	IndicationMedium = "medium"
	IndicationHigh   = "high"
)

// ExtraLevel is charge extra at one indication of cheaper days ahead: off,
// or three steps, each charging to a higher target with blocks that are
// cheaper still.
type ExtraLevel struct {
	Enabled bool        `json:"enabled"`
	Steps   []ExtraStep `json:"steps"`
}

// ExtraStep charges extra up to TargetSoC with the time blocks at least
// MinDiscountPct cheaper than the reference price.
type ExtraStep struct {
	MinDiscountPct float64 `json:"minDiscountPct"`
	TargetSoC      int     `json:"targetSoc"`
}

func extraLevel(enabled bool, steps ...[2]float64) ExtraLevel {
	l := ExtraLevel{Enabled: enabled}
	for _, s := range steps {
		l.Steps = append(l.Steps, ExtraStep{MinDiscountPct: s[0], TargetSoC: int(s[1])})
	}
	return l
}

// DefaultAdviceSettings show advice and never act on it. Charge extra
// charges more the cheaper the blocks are, and less (from deeper
// discounts) the more the coming days look cheaper.
func DefaultAdviceSettings() AdviceSettings {
	return AdviceSettings{
		Enabled: true, AutoMinConfidence: 3,
		ExtraLittle: extraLevel(true, [2]float64{20, 50}, [2]float64{30, 80}, [2]float64{50, 100}),
		ExtraMedium: extraLevel(true, [2]float64{30, 40}, [2]float64{45, 60}, [2]float64{60, 80}),
		ExtraHigh:   extraLevel(false, [2]float64{40, 30}, [2]float64{60, 50}, [2]float64{75, 70}),
	}
}

// Level returns the charge-extra steps for an indication.
func (a AdviceSettings) Level(indication string) ExtraLevel {
	switch indication {
	case IndicationMedium:
		return a.ExtraMedium
	case IndicationHigh:
		return a.ExtraHigh
	}
	return a.ExtraLittle
}

// Validate reports the first invalid field.
func (a AdviceSettings) Validate() error {
	if a.AutoMinConfidence != 2 && a.AutoMinConfidence != 3 {
		return fmt.Errorf("minimum confidence must be 2 (medium) or 3 (high)")
	}
	for _, l := range []struct {
		name  string
		level ExtraLevel
	}{{IndicationLittle, a.ExtraLittle}, {IndicationMedium, a.ExtraMedium}, {IndicationHigh, a.ExtraHigh}} {
		if len(l.level.Steps) != 3 {
			return fmt.Errorf("charge extra (%s) needs three steps", l.name)
		}
		prevDiscount, prevTarget := -1.0, 0
		for _, st := range l.level.Steps {
			switch {
			case st.MinDiscountPct < 0 || st.MinDiscountPct > 100:
				return fmt.Errorf("charge extra (%s): discounts must be between 0 and 100%%", l.name)
			case st.MinDiscountPct <= prevDiscount:
				return fmt.Errorf("charge extra (%s): each step needs a bigger discount than the one before", l.name)
			case st.TargetSoC < 10 || st.TargetSoC > 100:
				return fmt.Errorf("charge extra (%s): targets must be between 10 and 100%%", l.name)
			case st.TargetSoC < prevTarget:
				return fmt.Errorf("charge extra (%s): targets must not go down from step to step", l.name)
			}
			prevDiscount, prevTarget = st.MinDiscountPct, st.TargetSoC
		}
	}
	return nil
}

// Adjustment kinds and statuses.
const (
	AdjustExtraPlan = "extra_plan" // a temporary one-off SoC plan (charge extra)
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

// AISettings control AI mode (Settings → Advice → AI mode): when the AI plan runs, the
// model, and the prompt. An empty Model uses DefaultAIModel; an empty Prompt
// the default prompt.
type AISettings struct {
	// MaxRunsPerDay limits the automatic runs per local day (every run
	// counts, also those started by hand, which are always allowed).
	MaxRunsPerDay int    `json:"maxRunsPerDay"`
	OnPrices      bool   `json:"onPrices"` // run when new prices are published
	OnPlugIn      bool   `json:"onPlugIn"` // run when the car is plugged in
	Model         string `json:"model"`
	Prompt        string `json:"prompt"`
	// Goals fill {{goals}}; empty: the planner's rules from the settings.
	Goals string `json:"goals"`
	// AutoApply creates plans from the AI's advice automatically, like
	// AdviceSettings.AutoApply does from the rules' advice in the other
	// modes: at AutoMinConfidence or more, postponing only with AutoPostpone.
	// Until AutoSet (they were changed here), the three follow the advice
	// settings.
	AutoApply         bool `json:"autoApply"`
	AutoMinConfidence int  `json:"autoMinConfidence"` // 2 medium or 3 high
	AutoPostpone      bool `json:"autoPostpone"`
	AutoSet           bool `json:"autoSet"`
}

// DefaultAIModel is the OpenRouter model AI mode uses until another is set.
const DefaultAIModel = "~anthropic/claude-haiku-latest"

// AIModel is a model offered for AI mode, with OpenRouter's price (USD
// per million tokens, as of AIModelPricesDate) and what a typical run costs
// at it.
type AIModel struct {
	ID         string  `json:"id"`
	InputPerM  float64 `json:"inputPerM"`
	OutputPerM float64 `json:"outputPerM"`
	RunUSD     float64 `json:"runUsd"`
}

// AIModelPricesDate is when the prices below were read from OpenRouter;
// they change over time.
const AIModelPricesDate = "2026-11-18"

// A typical AI run: the default prompt with a week of data, and the answer
// (tokens).
const (
	AIRunInputTokens  = 7000
	AIRunOutputTokens = 4500
)

// AIModels are the models offered under Settings → Advice → AI mode,
// sorted by name (ignoring the "~"); any other OpenRouter model can be
// typed in. All support strict structured outputs. OpenRouter's "~…-latest"
// aliases follow a family's newest model; the others are fixed versions,
// chosen for a low price per run.
var AIModels = sortModels([]AIModel{
	// Anthropic
	{ID: "~anthropic/claude-fable-latest", InputPerM: 8, OutputPerM: 40},
	{ID: DefaultAIModel, InputPerM: 0.10, OutputPerM: 0.50},
	{ID: "~anthropic/claude-opus-latest", InputPerM: 4, OutputPerM: 20},
	{ID: "~anthropic/claude-sonnet-latest", InputPerM: 2, OutputPerM: 10},
	// OpenAI
	{ID: "~openai/gpt-astra-latest", InputPerM: 8, OutputPerM: 40},
	{ID: "~openai/gpt-luna-latest", InputPerM: 0.10, OutputPerM: 0.50},
	{ID: "~openai/gpt-sol-latest", InputPerM: 2, OutputPerM: 10},
	{ID: "~openai/gpt-terra-latest", InputPerM: 1, OutputPerM: 5},
	{ID: "openai/gpt-oss-120b", InputPerM: 0.15, OutputPerM: 0.60},
	// Others
	{ID: "~deepseek/deepseek-flash-latest", InputPerM: 0.036, OutputPerM: 0.50},
	{ID: "google/gemini-3.5-flash-lite", InputPerM: 0.30, OutputPerM: 2.50},
	{ID: "meta/muse-ember", InputPerM: 0.10, OutputPerM: 0.50},
	{ID: "minimax/minimax-m3.1-flash", InputPerM: 0.075, OutputPerM: 0.30},
	{ID: "mistralai/mistral-small-2603", InputPerM: 0.15, OutputPerM: 0.60},
	{ID: "qwen/qwen3.8-flash", InputPerM: 0.15, OutputPerM: 0.47},
	{ID: "~x-ai/grok-latest", InputPerM: 2, OutputPerM: 6},
	{ID: "xiaomi/mimo-v2.6-flash", InputPerM: 0.14, OutputPerM: 0.28},
	{ID: "~z-ai/glm-flash-latest", InputPerM: 0.04, OutputPerM: 0.078},
	{ID: "~z-ai/glm-latest", InputPerM: 0.07, OutputPerM: 1.40},
})

// sortModels sorts the models by name, ignoring the "~" of aliases, and
// adds what a typical run costs.
func sortModels(list []AIModel) []AIModel {
	for i := range list {
		m := &list[i]
		m.RunUSD = (AIRunInputTokens*m.InputPerM + AIRunOutputTokens*m.OutputPerM) / 1e6
	}
	sort.Slice(list, func(i, j int) bool {
		return strings.TrimPrefix(list[i].ID, "~") < strings.TrimPrefix(list[j].ID, "~")
	})
	return list
}

// DefaultAISettings run after new prices and on plug-in, up to three times
// a day, with the default model and prompt, and never act on the AI's
// advice by themselves.
func DefaultAISettings() AISettings {
	return AISettings{MaxRunsPerDay: 3, OnPrices: true, OnPlugIn: true, AutoMinConfidence: 3}
}

// MaxPromptChars bounds the prompt (before its data is filled in), and
// MaxGoalsChars the goals.
const (
	MaxPromptChars = 20000
	MaxGoalsChars  = 4000
)

// modelRe matches OpenRouter model IDs, including the "~" of its aliases
// for a family's latest model (e.g. ~anthropic/claude-haiku-latest).
var modelRe = regexp.MustCompile(`^~?[A-Za-z0-9._:/@+-]{1,200}$`)

// Validate reports the first invalid field; the prompt's placeholders are
// checked by the service.
func (a AISettings) Validate() error {
	switch {
	case a.MaxRunsPerDay < 0 || a.MaxRunsPerDay > 10:
		return fmt.Errorf("runs per day must be between 0 and 10")
	case a.Model != "" && !modelRe.MatchString(a.Model):
		return fmt.Errorf("the model must be an OpenRouter model ID, e.g. anthropic/claude-sonnet-5.5")
	case len([]rune(a.Prompt)) > MaxPromptChars:
		return fmt.Errorf("the prompt must be at most %d characters", MaxPromptChars)
	case len([]rune(a.Goals)) > MaxGoalsChars:
		return fmt.Errorf("the goals must be at most %d characters", MaxGoalsChars)
	case a.AutoMinConfidence != 2 && a.AutoMinConfidence != 3:
		return fmt.Errorf("minimum confidence must be 2 (medium) or 3 (high)")
	}
	return nil
}

// AI run triggers and outcomes.
const (
	AITriggerPrices = "prices"  // new prices published (or the first run)
	AITriggerPlugIn = "plug_in" // the car was plugged in
	AITriggerManual = "manual"  // Run now in the UI

	AIRunOK     = "ok"
	AIRunFailed = "failed"
)

// AIRun is one AI plan: what was sent, what came back, and the checked
// decision. Result holds the validated decision (aiplan.Result) and Trips
// maps the trip IDs in the prompt (T1, T2, …) to trip keys.
type AIRun struct {
	ID         int64     `json:"id"`
	StartedAt  time.Time `json:"startedAt"`
	FinishedAt time.Time `json:"finishedAt"`
	Trigger    string    `json:"trigger"`
	Status     string    `json:"status"`
	Error      string    `json:"error,omitempty"`
	// Decider is how the decision was made, e.g. "OpenRouter <model>".
	Decider          string `json:"decider"`
	Model            string `json:"model"`
	PromptTokens     int    `json:"promptTokens"`
	CompletionTokens int    `json:"completionTokens"`
	// PricesUntil is the end of the published prices the run saw.
	PricesUntil *time.Time        `json:"pricesUntil,omitempty"`
	NextTripKey string            `json:"nextTripKey,omitempty"` // the next departure then
	Trips       map[string]string `json:"trips,omitempty"`
	Prompt      string            `json:"prompt,omitempty"`   // the user message, with the data
	Response    string            `json:"response,omitempty"` // the model's raw answer
	Result      json.RawMessage   `json:"result,omitempty"`
}

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
	// Solar asks to plug in when today's advice is to charge extra from the
	// sun and the car is home but unplugged.
	Solar bool `json:"solar"`
	// Extra asks to plug in when today's advice is to charge extra with a
	// plan and the car is home but unplugged, and says when an extra plan
	// is created automatically.
	Extra bool `json:"extra"`
	// Arrival asks to plug in when the car gets back home from the day's
	// last trip (by the location sensor) and still needs charging.
	Arrival bool `json:"arrival"`
	// Prices says, in automatic and AI mode, when new prices change the
	// plan or bring charge extra while the car is unplugged.
	Prices bool `json:"prices"`
}

// DefaultNotificationSettings are off, with the reminder rules ready.
func DefaultNotificationSettings() NotificationSettings {
	return NotificationSettings{
		Phones: []string{}, Early: true, EarlyLeadMin: 30, UrgentLeadMin: 30, QuietFrom: "23:00", QuietTo: "07:00",
		UrgentInQuiet: true, SnoozeMin: 30, Problems: true, Questions: true, Solar: true, Extra: true, Arrival: true,
		Prices: true,
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

// DayStats are one local day's figures, kept indefinitely for history:
// EVCC's published prices (EUR/kWh incl. VAT; PriceHours is how much of the
// day they cover), the driving planned that day and the energy it uses, and
// KNMI's wind and sunshine forecast for it. Nil means unknown.
type DayStats struct {
	Date        string    `json:"date"`
	PriceMin    *float64  `json:"priceMin"`
	PriceAvg    *float64  `json:"priceAvg"`
	PriceMax    *float64  `json:"priceMax"`
	PriceHours  float64   `json:"priceHours"`
	DistanceKm  *float64  `json:"distanceKm"`
	EnergyKWh   *float64  `json:"energyKWh"`
	WindBft     *int      `json:"windBft"`
	SunshinePct *int      `json:"sunshinePct"`
	UpdatedAt   time.Time `json:"updatedAt"`
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
	NotifySolar     = "solar"      // plug in to charge extra from the sun
	NotifyExtra     = "extra"      // plug in for charge extra's plan
	NotifyExtraPlan = "extra_plan" // an extra plan was created automatically
	NotifyArrived   = "arrived"    // back home from the day's last trip: plug in
	NotifyPrices    = "prices"     // new prices changed the plan while unplugged
	NotifyInfo      = "info"       // a follow-up, e.g. after Skip trip

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
