package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/diogoap/evcc-planner/internal/audit"
	"github.com/diogoap/evcc-planner/internal/homeassistant"
	"github.com/diogoap/evcc-planner/internal/localtime"
	"github.com/diogoap/evcc-planner/internal/model"
	"github.com/diogoap/evcc-planner/internal/store"
)

// Car heating (AUTOMATION.md): before a departure from home, when it is
// cold outside, the car's climate is turned on in Home Assistant, by
// itself or after Heat now on the phone. The planner never turns it off;
// the car's own timer does.

const (
	// heatClearAfter is when heating messages leave the phone, and when a
	// question without an answer is given up.
	heatClearAfter = 30 * time.Minute
	// outsideMaxAge is how old the outside sensor's reading may be; an
	// older one counts as none, and the forecast stands in.
	outsideMaxAge = 2 * time.Hour
	// heatTries is how often turning on is tried before the departure.
	heatTries = 3
	// heatRecent is how long after its departure a decision still shows on
	// the Overview.
	heatRecent = 12 * time.Hour
)

// ---- settings ----

var heatingLabels = map[string]string{
	"enabled": "car heating", "belowC": "heat below", "leadMin": "minutes before departure",
	"climateEntity": "car climate", "outsideEntity": "outside temperature sensor", "targetC": "target temperature",
	"askPlugged": "ask while plugged in", "askUnplugged": "ask while unplugged",
}

// HeatingSettings returns the car heating settings.
func (s *Service) HeatingSettings() (model.HeatingSettings, error) {
	return s.Store.HeatingSettings(model.DefaultHeatingSettings())
}

// SaveHeatingSettings validates and stores the car heating settings.
func (s *Service) SaveHeatingSettings(ctx context.Context, v model.HeatingSettings) (model.HeatingSettings, error) {
	v.ClimateEntity, v.OutsideEntity = strings.TrimSpace(v.ClimateEntity), strings.TrimSpace(v.OutsideEntity)
	if err := v.Validate(); err != nil {
		return v, ValidationError{Msg: err.Error()}
	}
	ev := model.AuditEvent{Action: "settings.update", SubjectType: "settings", SubjectID: "heating"}
	err := s.change(ctx, ev, func(tx *store.Store, e *model.AuditEvent) error {
		before, err := tx.HeatingSettings(model.DefaultHeatingSettings())
		if err != nil {
			return err
		}
		if err := tx.SaveHeatingSettings(v); err != nil {
			return err
		}
		e.Before, e.After = changes(before, v)
		e.Summary = "Saved car heating settings (no changes)"
		if fields := fieldList(e.After, heatingLabels); fields != "" {
			e.Summary = "Changed car heating settings: " + fields
		}
		return nil
	})
	if err == nil {
		s.pollClimate(ctx, v.ClimateEntity) // the Overview's badge follows a new entity right away
	}
	return v, err
}

// ---- the decision ----

// heatInput is what a departure's decision depends on.
type heatInput struct {
	settings model.HeatingSettings
	// away says where the car is when the location sensor puts it away
	// from home; empty while home or without a sensor.
	away string
	// outside is the temperature to compare, from source ("sensor" or
	// "forecast"); noTemp says why there is none.
	outside    *float64
	source     string
	noTemp     string
	climate    *homeassistant.Climate
	climateErr error
	plugged    *bool // nil: unknown (EVCC's data too old)
	// noAsk says why no question can be asked; empty when one can.
	noAsk string
}

// heatDecision is what to do for a departure.
type heatDecision struct {
	outcome string // a model.Heat* outcome; HeatHeated: turn it on now
	reason  string
	retry   bool
	mode    string
	target  float64
}

// asks reports whether the settings want a question first, by whether the
// car is plugged in (unknown counts as unplugged).
func (in heatInput) asks() bool {
	if in.plugged != nil && *in.plugged {
		return in.settings.AskPlugged
	}
	return in.settings.AskUnplugged
}

// decideHeating decides a departure's car heating. It is pure.
func decideHeating(in heatInput) heatDecision {
	switch {
	case in.away != "":
		return heatDecision{outcome: model.HeatSkipped, reason: in.away}
	case in.outside == nil:
		return heatDecision{outcome: model.HeatSkipped, reason: in.noTemp}
	case *in.outside >= in.settings.BelowC:
		return heatDecision{outcome: model.HeatWarm}
	case in.climateErr != nil:
		return heatDecision{outcome: model.HeatFailed, reason: "couldn't read the car's climate: " + in.climateErr.Error(), retry: true}
	case in.climate.On():
		return heatDecision{outcome: model.HeatSkipped, reason: fmt.Sprintf("the climate is already on (%s)", in.climate.State)}
	}
	mode, ok := in.climate.HeatMode()
	if !ok {
		return heatDecision{outcome: model.HeatFailed,
			reason: fmt.Sprintf("%s has no heating mode (it offers %s)", in.climate.EntityID, strings.Join(in.climate.Modes, ", "))}
	}
	d := heatDecision{outcome: model.HeatHeated, mode: mode, target: in.climate.Clamp(in.settings.TargetC)}
	if in.asks() {
		if in.noAsk != "" {
			return heatDecision{outcome: model.HeatSkipped, reason: fmt.Sprintf("it asks first %s, but %s", pluggedText(in.plugged), in.noAsk)}
		}
		d.outcome = model.HeatAsked
	}
	return d
}

// pluggedText says which ask setting applied.
func pluggedText(plugged *bool) string {
	switch {
	case plugged == nil:
		return "while unplugged (EVCC's data is too old to tell)"
	case *plugged:
		return "while plugged in"
	}
	return "while unplugged"
}

// CheckHeating clears heating messages older than heatClearAfter (also
// when car heating is off), and decides the next departure's heating once
// its time has come: from LeadMin minutes before it until it leaves. It
// runs every minute, apart from the EVCC check, so it also works while
// EVCC is unreachable or notifications are off.
func (s *Service) CheckHeating(ctx context.Context) error {
	ctx = audit.WithSource(ctx, model.SourceAutomation)
	s.heatMu.Lock()
	defer s.heatMu.Unlock()
	notify, err := s.NotificationSettings()
	if err != nil {
		return err
	}
	errEnd := s.endHeatMessages(ctx, notify)
	hs, err := s.HeatingSettings()
	if err != nil {
		return errors.Join(errEnd, err)
	}
	s.pollClimate(ctx, hs.ClimateEntity)
	if !hs.Enabled {
		return errEnd
	}
	view, err := s.Plan(ctx)
	if err != nil {
		return errors.Join(errEnd, err)
	}
	next, now := view.Next, s.now()
	if next == nil || onTheRoad(view) || now.Before(next.DepartAt.Add(-time.Duration(hs.LeadMin)*time.Minute)) ||
		!now.Before(next.DepartAt) {
		return errEnd
	}
	run, err := s.Store.HeatingRunByKey("heat:" + next.TripKeys[0])
	if err != nil || (run != nil && !run.Retry) {
		return errors.Join(errEnd, err)
	}
	in := s.heatInput(ctx, hs, notify, next)
	return errors.Join(errEnd, s.applyHeating(ctx, hs, notify, next, run, in, decideHeating(in)))
}

// onTheRoad reports whether a trip is in progress.
func onTheRoad(view *PlanView) bool {
	for _, b := range view.Blocks {
		if b.Status == "in_progress" {
			return true
		}
	}
	return false
}

// heatInput reads what the decision needs: the car's location, the
// outside temperature (the sensor, else the forecast's lowest for the
// day), the climate, whether the car is plugged in, and whether a question
// can be asked.
func (s *Service) heatInput(ctx context.Context, hs model.HeatingSettings, notify model.NotificationSettings,
	next *BlockView) heatInput {
	in := heatInput{settings: hs}
	if e := s.carLocation(ctx, notify.LocationEntity); e != nil && e.State != "home" {
		in.away = fmt.Sprintf("the car isn't home (%s is %s)", notify.LocationEntity, e.State)
	}
	cl := s.Clients().Climate
	in.outside, in.source, in.noTemp = s.outsideTemp(ctx, cl, hs.OutsideEntity, next.DepartAt)
	if cl == nil {
		in.climateErr = errors.New(s.Clients().ClimateNote)
	} else if e, err := cl.State(ctx, hs.ClimateEntity); err != nil {
		in.climateErr = err
	} else if c, err := homeassistant.ParseClimate(*e); err != nil {
		in.climateErr = err
	} else {
		in.climate = &c
	}
	if snap := s.snapshot(); s.evccFresh(snap) {
		if lp, ok := s.loadpoint(snap.state); ok {
			v := lp.Connected
			in.plugged = &v
		}
	}
	in.noAsk = s.noAsk(notify)
	return in
}

// outsideTemp is the outside temperature: the sensor's reading when it is
// valid and recent, else the weather forecast's lowest for the departure's
// day. Without either, the last value says why.
func (s *Service) outsideTemp(ctx context.Context, cl homeassistant.ClimateController, entity string,
	departAt time.Time) (*float64, string, string) {
	why := "no outside temperature sensor"
	if entity != "" && cl != nil {
		why = s.sensorTemp(ctx, cl, entity)
		if why == "" {
			e, _ := cl.State(ctx, entity)
			v, _ := homeassistant.ParseTemperature(*e)
			return &v, "sensor", ""
		}
	}
	date := localtime.Date(departAt, s.loc())
	if w, _, err := s.weather(); err == nil && w != nil {
		if d := w.Day(date); d != nil && d.TempMinLowC != nil {
			v := *d.TempMinLowC
			return &v, "forecast", ""
		}
	}
	return nil, "", fmt.Sprintf("no outside temperature: %s, and the weather forecast has no low for %s", why, s.dayText(departAt))
}

// sensorTemp checks the outside sensor: empty when its reading is usable,
// else why not.
func (s *Service) sensorTemp(ctx context.Context, cl homeassistant.ClimateController, entity string) string {
	e, err := cl.State(ctx, entity)
	if err != nil {
		return err.Error()
	}
	if _, err := homeassistant.ParseTemperature(*e); err != nil {
		return err.Error()
	}
	if at := e.Reported(); s.now().Sub(at) > outsideMaxAge {
		return fmt.Sprintf("%s was last updated %s", entity, s.clockText(at))
	}
	return ""
}

// noAsk says why no heating question can be asked now, or "".
func (s *Service) noAsk(notify model.NotificationSettings) string {
	s.mu.RLock()
	lerr := s.listenErr
	s.mu.RUnlock()
	switch {
	case s.Clients().Notify == nil:
		return strings.ToLower(s.Clients().NotifyNote[:1]) + s.Clients().NotifyNote[1:]
	case !notify.Enabled || len(notify.Phones) == 0:
		return "notifications are off (Settings → Notifications)"
	case errors.Is(lerr, homeassistant.ErrNotAdmin):
		return "its button needs a token of a Home Assistant administrator"
	}
	return ""
}

// applyHeating carries out a decision and records it.
func (s *Service) applyHeating(ctx context.Context, hs model.HeatingSettings, notify model.NotificationSettings,
	next *BlockView, prev *model.HeatingRun, in heatInput, d heatDecision) error {
	r := model.HeatingRun{
		Key: "heat:" + next.TripKeys[0], TripKey: next.TripKeys[0], Title: next.FirstTitle, DepartAt: next.DepartAt,
		DecidedAt: s.now(), Outcome: d.outcome, Reason: d.reason, OutsideC: in.outside, OutsideSource: in.source,
		BelowC: hs.BelowC, Plugged: in.plugged, TargetC: d.target, HVACMode: d.mode,
	}
	if prev != nil {
		r.ID, r.Tries = prev.ID, prev.Tries
	}
	ev := model.AuditEvent{Action: "heating.skip", SubjectType: "heating", SubjectID: hs.ClimateEntity, Related: "trip:" + r.TripKey,
		Detail: s.heatDetail(r)}
	switch d.outcome {
	case model.HeatWarm:
		// Not cold enough: kept for the Overview, without an event, so
		// mild days stay quiet.
		_, err := s.Store.SaveHeatingRun(r)
		return err
	case model.HeatSkipped:
		ev.Summary = fmt.Sprintf("Car heating not turned on for %s: %s", r.Title, d.reason)
		return s.change(ctx, ev, func(tx *store.Store, e *model.AuditEvent) error {
			_, err := tx.SaveHeatingRun(r)
			return err
		})
	case model.HeatFailed:
		return s.heatFailed(ctx, &r, ev, errors.New(d.reason), d.retry)
	case model.HeatAsked:
		n := &model.Notification{
			Key: "heat_ask:" + r.TripKey, Kind: model.NotifyHeatAsk, Subject: r.TripKey, Urgent: true, Latest: &r.DepartAt,
			Title: "Heat the car?", Verbs: []string{model.VerbHeat, model.VerbView},
			Message: fmt.Sprintf("%s, %s leaves at %s. Heat it to %s?", capitalize(s.outsideText(r)), r.Title,
				s.clockText(r.DepartAt), tempText(r.TargetC)),
		}
		errSend := s.deliver(ctx, n, notify) // a failed send is tried again (reminders.retry)
		r.NotificationID = n.ID
		_, err := s.Store.SaveHeatingRun(r)
		return errors.Join(errSend, err)
	}
	if err := s.turnOnHeating(ctx, &r, hs.ClimateEntity, in.climate, model.HeatHeated, ""); err != nil {
		return s.heatFailed(ctx, &r, ev, err, true)
	}
	return s.notifyHeatOn(ctx, &r, notify)
}

// notifyHeatOn tells the phones that the car heating is on, by itself or
// after Heat now (Settings → Notifications: Car heating on), also in quiet
// hours. A failed send is tried again (reminders.retry).
func (s *Service) notifyHeatOn(ctx context.Context, r *model.HeatingRun, notify model.NotificationSettings) error {
	if !notify.HeatingOn || !notify.Enabled || len(notify.Phones) == 0 || s.Clients().Notify == nil {
		return nil
	}
	n := &model.Notification{
		Key: "heat_on:" + r.TripKey, Kind: model.NotifyHeatOn, Subject: r.TripKey, Latest: &r.DepartAt,
		Title: "Car heating on", Verbs: []string{model.VerbView},
		Message: fmt.Sprintf("Heating the car to %s: %s, %s leaves at %s.", tempText(r.TargetC), s.outsideText(*r), r.Title,
			s.clockText(r.DepartAt)),
	}
	errSend := s.deliver(ctx, n, notify)
	r.NotificationID = n.ID
	saved, err := s.Store.SaveHeatingRun(*r)
	*r = saved
	return errors.Join(errSend, err)
}

// turnOnHeating turns the car's climate on in the run's mode and target,
// and records it with the run's new outcome. A failure changes nothing
// and is returned.
func (s *Service) turnOnHeating(ctx context.Context, r *model.HeatingRun, entity string, before *homeassistant.Climate,
	outcome, how string) error {
	cl := s.Clients().Climate
	if cl == nil {
		return errors.New(s.Clients().ClimateNote)
	}
	if err := cl.SetClimate(ctx, entity, r.TargetC, r.HVACMode); err != nil {
		return err
	}
	now := s.now()
	tries := r.Tries
	r.Outcome, r.Reason, r.HeatedAt, r.Retry = outcome, "", &now, false
	ev := model.AuditEvent{
		Action: "heating.on", SubjectType: "heating", SubjectID: entity, Related: "trip:" + r.TripKey,
		Summary: fmt.Sprintf("Turned on the car heating for %s%s: %s, %s", r.Title, how, tempText(r.TargetC), s.outsideText(*r)),
		Before:  audit.Marshal(map[string]string{"climate": climateText(before)}),
		After:   audit.Marshal(map[string]string{"climate": fmt.Sprintf("%s, %s", r.HVACMode, tempText(r.TargetC))}),
		Detail:  s.heatDetail(*r),
	}
	if tries > 0 {
		ev.Summary += fmt.Sprintf(" (on try %d)", tries+1)
	}
	err := s.change(ctx, ev, func(tx *store.Store, e *model.AuditEvent) error {
		saved, err := tx.SaveHeatingRun(*r)
		*r = saved
		return err
	})
	s.pollClimate(ctx, entity) // the Overview's badge shows it right away
	return err
}

// ClimateStatus is the car's climate as the Overview's badge shows it.
type ClimateStatus struct {
	// Mode is the HVAC mode (off, heat_cool, heat, cool, ...); Action
	// what it does (heating, cooling, idle, off, ...), when reported.
	Mode     string    `json:"mode"`
	Action   string    `json:"action,omitempty"`
	TargetC  *float64  `json:"targetC,omitempty"`
	CurrentC *float64  `json:"currentC,omitempty"`
	Since    time.Time `json:"since"`
}

// pollClimate reads the car's climate entity for the Overview's badge
// (every minute, with car heating's check); nil without one, or when it
// can't be read.
func (s *Service) pollClimate(ctx context.Context, entity string) {
	var st *ClimateStatus
	if cl := s.Clients().Climate; cl != nil && entity != "" {
		if e, err := cl.State(ctx, entity); err != nil {
			s.Log.Debug("car climate", "entity", entity, "err", err)
		} else if c, err := homeassistant.ParseClimate(*e); err == nil {
			st = &ClimateStatus{Mode: c.State, Action: c.Action, TargetC: c.Target, CurrentC: c.Current, Since: e.LastChanged}
		}
	}
	s.mu.Lock()
	s.carClimate = st
	s.mu.Unlock()
}

// heatFailed records a failed attempt: tried again before the departure
// while retry allows, up to heatTries. Only the first and the last failure
// are in the activity log.
func (s *Service) heatFailed(ctx context.Context, r *model.HeatingRun, ev model.AuditEvent, err error, retry bool) error {
	r.Tries++
	r.Outcome, r.Reason = model.HeatFailed, err.Error()
	r.Retry = retry && r.Tries < heatTries && s.now().Before(r.DepartAt)
	if r.Tries == 1 || !r.Retry {
		ev.Action, ev.Outcome, ev.Error = "heating.on", model.OutcomeFailure, err.Error()
		ev.Summary = fmt.Sprintf("Car heating for %s failed: %s", r.Title, err.Error())
		if r.Retry {
			ev.Summary += "; trying again"
		}
		s.record(ctx, ev)
	}
	saved, serr := s.Store.SaveHeatingRun(*r)
	*r = saved
	return serr
}

// heatNow turns the heating on after Heat now was pressed on its question
// (HandleAction): only while the question still waits. A failure leaves
// the question open, to press again.
func (s *Service) heatNow(ctx context.Context, n model.Notification, notify model.NotificationSettings, title string) error {
	s.heatMu.Lock()
	defer s.heatMu.Unlock()
	run, err := s.Store.HeatingRunByKey("heat:" + n.Subject)
	if err != nil {
		return err
	}
	if run == nil || run.Outcome != model.HeatAsked {
		return s.resolve(ctx, &n, "No longer asked", notify)
	}
	hs, err := s.HeatingSettings()
	if err != nil {
		return err
	}
	ev := model.AuditEvent{Action: "notification.action", SubjectType: "notification", SubjectID: strconv.FormatInt(n.ID, 10),
		Related: "trip:" + n.Subject}
	cl := s.Clients().Climate
	if cl == nil {
		return invalid("%s", s.Clients().ClimateNote)
	}
	e, err := cl.State(ctx, hs.ClimateEntity)
	if err != nil {
		return err
	}
	c, err := homeassistant.ParseClimate(*e)
	if err != nil {
		return err
	}
	if c.On() {
		run.Outcome, run.Reason = model.HeatSkipped, fmt.Sprintf("the climate was already on (%s) when Heat now was pressed", c.State)
		if _, err := s.Store.SaveHeatingRun(*run); err != nil {
			return err
		}
		ev.Summary = fmt.Sprintf("%s pressed on %q: the car heating was already on", title, n.Title)
		s.record(ctx, ev)
		return s.resolve(ctx, &n, "Already on", notify)
	}
	mode, ok := c.HeatMode()
	if !ok {
		return invalid("%s has no heating mode", hs.ClimateEntity)
	}
	run.HVACMode, run.TargetC = mode, c.Clamp(hs.TargetC)
	if err := s.turnOnHeating(ctx, run, hs.ClimateEntity, &c, model.HeatConfirmed, " (Heat now)"); err != nil {
		ev.Outcome, ev.Error = model.OutcomeFailure, err.Error()
		ev.Summary = fmt.Sprintf("%s pressed on %q: turning on the car heating failed", title, n.Title)
		s.record(ctx, ev)
		return err
	}
	ev.Summary = fmt.Sprintf("%s pressed on %q: car heating on at %s", title, n.Title, tempText(run.TargetC))
	s.record(ctx, ev)
	if err := s.resolve(ctx, &n, "Heating turned on", notify); err != nil {
		return err
	}
	return s.notifyHeatOn(ctx, run, notify)
}

// endHeatMessages takes heating messages off the phone heatClearAfter
// after they were sent, and gives up questions without an answer by then
// (or that never reached a phone).
func (s *Service) endHeatMessages(ctx context.Context, notify model.NotificationSettings) error {
	now := s.now()
	open, err := s.Store.OpenNotifications()
	if err != nil {
		return err
	}
	var errs []error
	for i := range open {
		n := &open[i]
		if n.Kind != model.NotifyHeatAsk && n.Kind != model.NotifyHeatOn {
			continue
		}
		sent := n.CreatedAt
		if n.SentAt != nil {
			sent = *n.SentAt
		}
		if now.Sub(sent) < heatClearAfter {
			continue
		}
		note := "Cleared after 30 minutes"
		if n.Kind == model.NotifyHeatAsk {
			note = "No answer within 30 minutes"
		}
		errs = append(errs, s.resolve(ctx, n, note, notify))
	}
	asked, err := s.Store.AskedHeatingRuns()
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	for i := range asked {
		r := &asked[i]
		n, err := s.Store.Notification(r.NotificationID)
		if err == nil && n.Open() && now.Sub(r.DecidedAt) < heatClearAfter {
			continue // still waiting
		}
		r.Outcome, r.Reason = model.HeatNoAnswer, "no answer within 30 minutes"
		if err == nil && n.State == model.NotifyFailed {
			r.Outcome, r.Reason = model.HeatFailed, "the question didn't reach the phone"
		}
		ev := model.AuditEvent{Action: "heating.skip", SubjectType: "heating", Related: "trip:" + r.TripKey,
			Summary: fmt.Sprintf("Car heating not turned on for %s: %s", r.Title, r.Reason), Detail: s.heatDetail(*r)}
		errs = append(errs, s.change(ctx, ev, func(tx *store.Store, e *model.AuditEvent) error {
			_, err := tx.SaveHeatingRun(*r)
			return err
		}))
	}
	return errors.Join(errs...)
}

// heatDetail is a decision's facts for the activity log.
func (s *Service) heatDetail(r model.HeatingRun) []byte {
	d := map[string]string{"tripKey": r.TripKey, "departAt": s.localText(r.DepartAt), "belowC": strconv.FormatFloat(r.BelowC, 'f', -1, 64)}
	if r.OutsideC != nil {
		d["outside"], d["outsideSource"] = tempText(*r.OutsideC), r.OutsideSource
	}
	switch {
	case r.Plugged == nil:
		d["plugged"] = "unknown"
	case *r.Plugged:
		d["plugged"] = "yes"
	default:
		d["plugged"] = "no"
	}
	return audit.Marshal(d)
}

// outsideText is the temperature a decision used, in a few words: "1 °C
// outside", or "a low of 1 °C forecast".
func (s *Service) outsideText(r model.HeatingRun) string {
	switch {
	case r.OutsideC == nil:
		return "outside temperature unknown"
	case r.OutsideSource == "forecast":
		return "a low of " + tempText(*r.OutsideC) + " forecast"
	}
	return tempText(*r.OutsideC) + " outside"
}

// tempText is a temperature to one decimal when it isn't whole: "2.6 °C",
// so a reading never shows as the threshold it is below.
func tempText(v float64) string {
	v = math.Round(v*10) / 10
	if v == 0 {
		v = 0 // no "-0"
	}
	return strconv.FormatFloat(v, 'f', -1, 64) + " °C"
}

// climateText is a climate's state for the activity log.
func climateText(c *homeassistant.Climate) string {
	if c == nil {
		return "unknown"
	}
	if c.Target == nil {
		return c.State
	}
	return fmt.Sprintf("%s, %s", c.State, tempText(*c.Target))
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// carLocation is the optional car location sensor's state; nil without
// one, or when it cannot be read.
func (s *Service) carLocation(ctx context.Context, entity string) *homeassistant.EntityState {
	if entity == "" {
		return nil
	}
	sr, ok := s.Clients().Notify.(interface {
		State(context.Context, string) (*homeassistant.EntityState, error)
	})
	if !ok {
		return nil
	}
	e, err := sr.State(ctx, entity)
	if err != nil {
		s.Log.Warn("car location sensor", "entity", entity, "err", err)
		return nil
	}
	return e
}

// ---- views ----

// HeatingSummary is car heating on the Overview: its settings and the
// latest decision whose departure is ahead or recent.
type HeatingSummary struct {
	Enabled      bool              `json:"enabled"`
	BelowC       float64           `json:"belowC"`
	LeadMin      int               `json:"leadMin"`
	TargetC      float64           `json:"targetC"`
	AskPlugged   bool              `json:"askPlugged"`
	AskUnplugged bool              `json:"askUnplugged"`
	Last         *model.HeatingRun `json:"last,omitempty"`
}

// heatingSummary is nil while car heating is off and nothing was decided
// recently.
func (s *Service) heatingSummary() *HeatingSummary {
	hs, err := s.HeatingSettings()
	if err != nil {
		return nil
	}
	var last *model.HeatingRun
	if recent, err := s.Store.RecentHeatingRuns(1); err == nil && len(recent) == 1 && s.now().Sub(recent[0].DepartAt) < heatRecent {
		last = &recent[0]
	}
	if !hs.Enabled && last == nil {
		return nil
	}
	return &HeatingSummary{Enabled: hs.Enabled, BelowC: hs.BelowC, LeadMin: hs.LeadMin, TargetC: hs.TargetC, AskPlugged: hs.AskPlugged,
		AskUnplugged: hs.AskUnplugged, Last: last}
}

// HeatingView is car heating under Settings: what the sensors read now,
// what would be sent, what keeps it from working, and recent decisions.
type HeatingView struct {
	Settings model.HeatingSettings `json:"settings"`
	Outside  OutsideView           `json:"outside"`
	Climate  ClimateView           `json:"climate"`
	Notes    []string              `json:"notes"`
	Recent   []model.HeatingRun    `json:"recent"`
}

// OutsideView is the outside temperature now: from the sensor, else the
// forecast's lowest today.
type OutsideView struct {
	C      *float64   `json:"c"`
	Source string     `json:"source,omitempty"` // sensor or forecast
	At     *time.Time `json:"at,omitempty"`     // the sensor's reading
	Note   string     `json:"note,omitempty"`   // why the sensor isn't used
}

// ClimateView is the car's climate now and what turning it on would send.
type ClimateView struct {
	State   string   `json:"state,omitempty"`
	TargetC *float64 `json:"targetC,omitempty"`
	MinC    float64  `json:"minC,omitempty"`
	MaxC    float64  `json:"maxC,omitempty"`
	Modes   []string `json:"modes"`
	Mode    string   `json:"mode,omitempty"`  // the mode it would turn on
	SendC   *float64 `json:"sendC,omitempty"` // the target it would send
	Error   string   `json:"error,omitempty"`
}

// Heating reads the sensors live and returns the car heating view.
func (s *Service) Heating(ctx context.Context) (*HeatingView, error) {
	hs, err := s.HeatingSettings()
	if err != nil {
		return nil, err
	}
	notify, err := s.NotificationSettings()
	if err != nil {
		return nil, err
	}
	v := &HeatingView{Settings: hs, Notes: []string{}, Climate: ClimateView{Modes: []string{}}}
	if v.Recent, err = s.Store.RecentHeatingRuns(10); err != nil {
		return nil, err
	}
	cl := s.Clients().Climate
	if cl == nil {
		v.Notes = append(v.Notes, s.Clients().ClimateNote+".")
	}
	if hs.OutsideEntity != "" && cl != nil {
		if why := s.sensorTemp(ctx, cl, hs.OutsideEntity); why != "" {
			v.Outside.Note = why
		} else if e, err := cl.State(ctx, hs.OutsideEntity); err == nil {
			t, _ := homeassistant.ParseTemperature(*e)
			at := e.Reported()
			v.Outside.C, v.Outside.Source, v.Outside.At = &t, "sensor", &at
		}
	}
	if v.Outside.C == nil {
		if w, _, err := s.weather(); err == nil && w != nil {
			if d := w.Day(localtime.Date(s.now(), s.loc())); d != nil && d.TempMinLowC != nil {
				t := *d.TempMinLowC
				v.Outside.C, v.Outside.Source = &t, "forecast"
			}
		}
	}
	switch {
	case hs.ClimateEntity == "":
		v.Notes = append(v.Notes, "Choose the car's climate entity.")
	case cl != nil:
		if e, err := cl.State(ctx, hs.ClimateEntity); err != nil {
			v.Climate.Error = err.Error()
		} else if c, err := homeassistant.ParseClimate(*e); err != nil {
			v.Climate.Error = err.Error()
		} else {
			v.Climate.State, v.Climate.TargetC, v.Climate.MinC, v.Climate.MaxC = c.State, c.Target, c.Min, c.Max
			if c.Modes != nil {
				v.Climate.Modes = c.Modes
			}
			if mode, ok := c.HeatMode(); ok {
				send := c.Clamp(hs.TargetC)
				v.Climate.Mode, v.Climate.SendC = mode, &send
			} else {
				v.Climate.Error = fmt.Sprintf("%s has no heating mode", hs.ClimateEntity)
			}
		}
	}
	if (hs.AskPlugged || hs.AskUnplugged) && s.noAsk(notify) != "" {
		v.Notes = append(v.Notes, "Asking first needs phone notifications: "+s.noAsk(notify)+". Until then, a departure that "+
			"would ask is skipped.")
	}
	if notify.LocationEntity == "" {
		v.Notes = append(v.Notes, "Without a car location sensor (Settings → Notifications), the car counts as home.")
	}
	return v, nil
}
