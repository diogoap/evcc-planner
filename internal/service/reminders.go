package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/diogoap/evcc-planner/internal/audit"
	"github.com/diogoap/evcc-planner/internal/evcc"
	"github.com/diogoap/evcc-planner/internal/homeassistant"
	"github.com/diogoap/evcc-planner/internal/localtime"
	"github.com/diogoap/evcc-planner/internal/model"
)

// notifyState is what the notification check remembers between EVCC
// checks: the connection, the SoC, how long a planned charge has been
// idle, and which targets were below while plugged in.
type notifyState struct {
	connected bool
	soc       float64
	idleSince time.Time
	modeSince time.Time // plugged in with EVCC in mode (not Smart) since
	mode      string
	below     map[string]bool
}

// Timings of the notification rules (AUTOMATION.md).
var (
	stoppedAfter    = 10 * time.Minute // a planned charge idle this long has stopped
	modeAfter       = 10 * time.Minute // plugged in this long outside Smart mode is a problem
	unpluggedBefore = 30 * time.Minute // unplugging earlier than this before a departure is early
	beforeQuiet     = 15 * time.Minute // early reminders due in quiet hours come this long before them
)

// reminderKinds ask the driver to plug in; plugging in ends them.
var reminderKinds = map[string]bool{model.NotifyEarly: true, model.NotifyUrgent: true, model.NotifyLate: true}

// CheckNotifications sends the phone notifications that are due: plug-in
// reminders, charging problems, questions about calendar events, and
// ready messages, and ends those no longer needed (AUTOMATION.md). It runs
// after each EVCC check and only acts on a fresh EVCC state. A missed
// target is recorded in the activity log even with notifications off.
func (s *Service) CheckNotifications(ctx context.Context) error {
	ctx = audit.WithSource(ctx, model.SourceAutomation)
	s.notifyMu.Lock()
	defer s.notifyMu.Unlock()
	snap := s.snapshot()
	if snap.state == nil || snap.err != nil || s.now().Sub(snap.okAt) > 3*s.Conn().EVCCPollInterval() {
		return nil
	}
	lp, ok := s.loadpoint(snap.state)
	if !ok {
		return nil
	}
	settings, err := s.NotificationSettings()
	if err != nil {
		return err
	}
	prev := s.notifyPrev
	cur := &notifyState{connected: lp.Connected, soc: lp.VehicleSoc, below: map[string]bool{}}
	if prev != nil {
		cur.idleSince, cur.modeSince, cur.mode, cur.below = prev.idleSince, prev.modeSince, prev.mode, prev.below
	}
	s.notifyPrev = cur
	on := settings.Enabled && len(settings.Phones) > 0 && s.Clients().Notify != nil
	unplugged := prev != nil && prev.connected && !lp.Connected
	if !on && !unplugged {
		return nil
	}
	view, err := s.Plan(ctx)
	if err != nil {
		return err
	}
	r := &reminders{s: s, ctx: ctx, settings: settings, view: view, lp: lp, now: s.now(), on: on}
	if unplugged {
		r.unplugged(prev.soc)
	}
	if !on {
		return r.err()
	}
	r.problems(cur)
	r.ready(cur)
	r.cleanUp()
	r.plugIn()
	r.snoozed()
	r.questions()
	return r.err()
}

// reminders evaluates the rules for one check.
type reminders struct {
	s        *Service
	ctx      context.Context
	settings model.NotificationSettings
	view     *PlanView
	lp       *evcc.Loadpoint
	now      time.Time
	on       bool
	errs     []error
}

func (r *reminders) err() error { return errors.Join(r.errs...) }

func (r *reminders) fail(err error) {
	if err != nil {
		r.errs = append(r.errs, err)
	}
}

// quiet reports whether t falls in the quiet hours.
func (r *reminders) quiet(t time.Time) bool {
	from, to := r.settings.QuietFrom, r.settings.QuietTo
	if from == to {
		return false
	}
	hm := t.In(r.s.loc()).Format("15:04")
	if from < to {
		return hm >= from && hm < to
	}
	return hm >= from || hm < to
}

// quietStart is when the quiet hours around t began.
func (r *reminders) quietStart(t time.Time) time.Time {
	h, m, _ := localtime.ParseClock(r.settings.QuietFrom)
	day := localtime.StartOfDay(t, r.s.loc())
	start := localtime.OnDay(day, h, m)
	if start.After(t) {
		start = localtime.OnDay(localtime.AddDays(day, -1), h, m)
	}
	return start
}

// allowed reports whether a notification may be sent now: urgent ones in
// quiet hours only when the settings say so.
func (r *reminders) allowed(urgent bool) bool {
	return !r.quiet(r.now) || (urgent && r.settings.UrgentInQuiet)
}

// send delivers a notification once per key. Informational ones are done
// right away; a reminder replaces open reminders for the same trip.
func (r *reminders) send(n model.Notification, info bool) {
	if existing, err := r.s.Store.NotificationByKey(n.Key); err != nil || existing != nil {
		r.fail(err)
		return
	}
	if reminderKinds[n.Kind] {
		r.endOpen(func(o model.Notification) bool { return reminderKinds[o.Kind] && o.Subject == n.Subject },
			"Replaced by a newer reminder")
	}
	if err := r.s.deliver(r.ctx, &n, r.settings); err != nil {
		r.fail(err)
		return
	}
	if info {
		n.State = model.NotifyDone
		_, err := r.s.Store.SaveNotification(n)
		r.fail(err)
	}
}

// endOpen resolves the open notifications that match.
func (r *reminders) endOpen(match func(model.Notification) bool, note string) {
	open, err := r.s.Store.OpenNotifications()
	if err != nil {
		r.fail(err)
		return
	}
	for i := range open {
		if match(open[i]) {
			r.fail(r.s.resolve(r.ctx, &open[i], note, r.settings))
		}
	}
}

// next is the next departure's block and its first trip key, if any.
func (r *reminders) next() (*BlockView, string) {
	if r.view.Next == nil {
		return nil, ""
	}
	return r.view.Next, r.view.Next.TripKeys[0]
}

func (r *reminders) title(b *BlockView) string { return b.FirstTitle }

// onTheRoad reports whether a trip is in progress.
func (r *reminders) onTheRoad() bool {
	for _, b := range r.view.Blocks {
		if b.Status == "in_progress" {
			return true
		}
	}
	return false
}

// carAway reports whether the optional car location sensor says the car
// is not home; without one (or when it cannot be read) the car counts as
// home.
func (r *reminders) carAway() bool {
	if r.settings.LocationEntity == "" {
		return false
	}
	sr, ok := r.s.Clients().Notify.(interface {
		State(context.Context, string) (*homeassistant.EntityState, error)
	})
	if !ok {
		return false
	}
	e, err := sr.State(r.ctx, r.settings.LocationEntity)
	if err != nil {
		r.s.Log.Warn("car location sensor", "entity", r.settings.LocationEntity, "err", err)
		return false
	}
	return e.State != "home"
}

// cleanUp ends open notifications that no longer apply: reminders once the
// car is plugged in or the trip is no longer next or covered, questions
// once answered, and problems once solved.
func (r *reminders) cleanUp() {
	next, key := r.next()
	review := map[string]bool{}
	for _, t := range r.view.Trips {
		if t.NeedsReview && t.Enabled && t.State == "upcoming" {
			review[t.Key] = true
		}
	}
	target := r.view.Control.Target
	r.endOpen(func(n model.Notification) bool {
		switch {
		case reminderKinds[n.Kind]:
			return r.lp.Connected || next == nil || n.Subject != key || (target != nil && target.Covered && n.Kind == model.NotifyEarly) ||
				(n.Kind != model.NotifyEarly && next.Status == "ready") || !r.now.Before(next.DepartAt)
		case n.Kind == model.NotifyQuestion:
			return !review[n.Subject]
		case n.Kind == model.NotifyStopped:
			return !r.lp.Connected || r.lp.Charging || !r.lp.PlanActive
		case n.Kind == model.NotifyMode:
			return !r.lp.Connected || n.Key != modeKey(r.lp.Mode, n.Subject)
		}
		return false
	}, "No longer needed")
}

// snoozed sends snoozed notifications again once their time has come.
func (r *reminders) snoozed() {
	open, err := r.s.Store.OpenNotifications()
	if err != nil {
		r.fail(err)
		return
	}
	for i := range open {
		n := &open[i]
		if n.State != model.NotifySnoozed || n.SnoozedUntil == nil || r.now.Before(*n.SnoozedUntil) || !r.allowed(n.Urgent) {
			continue
		}
		if n.Latest != nil && !r.now.Before(*n.Latest) {
			continue // past its latest time; a later rule replaces it
		}
		n.Note = "Sent again after a snooze"
		r.fail(r.s.deliver(r.ctx, n, r.settings))
	}
}

// plugIn sends the plug-in reminders for the next departure while the car
// is unplugged at home.
func (r *reminders) plugIn() {
	next, key := r.next()
	target := r.view.Control.Target
	if r.lp.Connected || next == nil || target == nil || r.onTheRoad() || !r.view.SoC.Known || r.carAway() {
		return
	}
	verbs := []string{model.VerbSnooze, model.VerbSkip, model.VerbView}
	latest := next.LatestChargeStart

	// Too late: plugging in now no longer reaches the departure's target.
	if latest != nil && !r.now.Before(*latest) && r.now.Before(next.DepartAt) {
		if !r.allowed(true) {
			return
		}
		start := r.view.SoC.Percent
		if next.StartSoC != nil {
			start = next.StartSoC
		}
		p := r.view.Settings
		hours := next.DepartAt.Add(-time.Duration(p.ChargeBufferMin) * time.Minute).Sub(r.now).Hours()
		reach := *start + math.Max(0, hours)*p.ChargePowerKW*p.ChargingEfficiencyPct/100/p.UsableCapacityKWh*100
		r.send(model.Notification{
			Key: "late:" + key, Kind: model.NotifyLate, Subject: key, Urgent: true, Title: "Plug in now",
			Message: fmt.Sprintf("Plugging in now gives about %d%% by %s (%s needs %d%%).",
				int(math.Min(100, math.Floor(reach))), r.s.clockText(next.DepartAt), r.title(next), next.TargetSoC),
			Verbs: []string{model.VerbSkip, model.VerbView},
		}, false)
		return
	}
	// Urgent: shortly before the latest plug-in time.
	if latest != nil && !r.now.Before(latest.Add(-time.Duration(r.settings.UrgentLeadMin)*time.Minute)) {
		if !r.allowed(true) {
			return
		}
		r.send(model.Notification{
			Key: "urgent:" + key, Kind: model.NotifyUrgent, Subject: key, Urgent: true, Latest: latest,
			Title:   "Plug in by " + r.s.clockText(*latest),
			Message: fmt.Sprintf("%s needs %d%% by %s.", r.title(next), next.TargetSoC, r.s.clockText(next.DepartAt)),
			Verbs:   verbs,
		}, false)
		return
	}
	// Early: before the first slot of the planned charging; just before
	// quiet hours when it falls in them.
	if !r.settings.Early || target.Covered || len(target.Charging) == 0 {
		return
	}
	first, last := target.Charging[0], target.Charging[len(target.Charging)-1]
	due := first.Start.Add(-time.Duration(r.settings.EarlyLeadMin) * time.Minute)
	if r.quiet(due) {
		due = r.quietStart(due).Add(-beforeQuiet)
	}
	if r.now.Before(due) || !r.now.Before(last.End) || !r.allowed(false) {
		return
	}
	when := r.s.clockText(first.Start)
	if !first.Start.After(r.now) {
		when = "now"
	}
	n := model.Notification{
		Key: "early:" + key, Kind: model.NotifyEarly, Subject: key, Latest: latest, Title: "Plug in to charge cheaply",
		Message: fmt.Sprintf("Charging is cheapest from %s (%s/kWh); %s needs %d%% by %s.",
			when, priceText(first.Value), target.Title, target.SoC, r.s.clockText(target.ReadyBy)),
		Verbs: verbs,
	}
	if len(target.Ahead) > 0 {
		n.Message = fmt.Sprintf("Charging is cheapest from %s (%s/kWh); the plan charges %d%% by %s, also for %s.",
			when, priceText(first.Value), target.SoC, r.s.clockText(target.ReadyBy), target.Ahead[0].Title)
	}
	r.send(n, false)
}

func priceText(v float64) string { return fmt.Sprintf("€%.2f", v) }

// problems warns when charging stopped during a planned charge, or EVCC
// is not in Smart mode, while the car is plugged in and the next departure
// still needs charge. The mode is only a problem after a while: another
// automation may switch EVCC to Off while the car is away and back to
// Smart soon after it arrives.
func (r *reminders) problems(cur *notifyState) {
	next, key := r.next()
	idle := r.lp.Connected && r.lp.PlanActive && !r.lp.Charging
	switch {
	case !idle:
		cur.idleSince = time.Time{}
	case cur.idleSince.IsZero():
		cur.idleSince = r.now
	}
	wrongMode := r.lp.Connected && !smartMode(r.lp.Mode)
	switch {
	case !wrongMode:
		cur.modeSince, cur.mode = time.Time{}, ""
	case cur.modeSince.IsZero() || cur.mode != r.lp.Mode:
		cur.modeSince, cur.mode = r.now, r.lp.Mode
	}
	if !r.settings.Problems || next == nil || !r.lp.Connected || r.lp.VehicleSoc >= float64(next.TargetSoC) || !r.allowed(true) {
		return
	}
	needs := fmt.Sprintf("%s needs %d%% by %s.", r.title(next), next.TargetSoC, r.s.clockText(next.DepartAt))
	soc := int(math.Round(r.lp.VehicleSoc))
	if idle && r.now.Sub(cur.idleSince) >= stoppedAfter {
		r.send(model.Notification{
			Key: fmt.Sprintf("stopped:%s:%d", key, next.DepartAt.Unix()), Kind: model.NotifyStopped, Subject: key, Urgent: true,
			Title: "Charging stopped", Message: fmt.Sprintf("Charging stopped at %d%%; %s", soc, needs), Verbs: []string{model.VerbView},
		}, false)
	}
	if wrongMode && r.now.Sub(cur.modeSince) >= modeAfter {
		n := model.Notification{
			Key: modeKey(r.lp.Mode, key), Kind: model.NotifyMode, Subject: key, Verbs: []string{model.VerbView},
			Title:   "EVCC is in " + modeName(r.lp.Mode) + " mode",
			Message: "It doesn't follow the plan; " + needs + " Set EVCC to Smart.",
		}
		switch modeName(r.lp.Mode) {
		case "Off":
			n.Title, n.Urgent = "Charging is off in EVCC", true
			n.Message = "The plan won't run; " + needs + " Set EVCC to Smart."
		case "Fast":
			n.Message = "It charges at full power now, not in the plan's cheapest slots or from solar. Set EVCC to Smart."
		}
		r.send(n, false)
	}
}

// smartMode reports whether EVCC's loadpoint mode follows plans: Smart,
// or no mode reported.
func smartMode(mode string) bool { return mode == "" || mode == "smart" }

// modeName is how EVCC's UI names a loadpoint mode.
func modeName(mode string) string {
	switch mode {
	case "off":
		return "Off"
	case "now", "fast":
		return "Fast"
	case "smart":
		return "Smart"
	}
	return mode
}

func modeKey(mode, tripKey string) string { return "mode:" + mode + ":" + tripKey }

// unplugged handles the car being unplugged: early, while the next
// departure still needs charge, or leaving with less than its target (a
// missed target, also recorded in the activity log).
func (r *reminders) unplugged(soc float64) {
	next, key := r.next()
	if next == nil || soc <= 0 || soc >= float64(next.TargetSoC) {
		return
	}
	at := int(math.Round(soc))
	if !r.now.Before(next.DepartAt.Add(-unpluggedBefore)) {
		msg := fmt.Sprintf("Left for %s with %d%% (target %d%%).", r.title(next), at, next.TargetSoC)
		if existing, err := r.s.Store.NotificationByKey("missed:" + key); err != nil || existing != nil {
			r.fail(err)
			return
		}
		r.s.record(r.ctx, model.AuditEvent{
			Action: "plan.missed", SubjectType: "plan", Related: "trip:" + key, Summary: msg,
			Detail: audit.Marshal(map[string]any{"tripKey": key, "targetSoc": next.TargetSoC, "soc": at}),
		})
		n := model.Notification{
			Key: "missed:" + key, Kind: model.NotifyMissed, Subject: key, Title: "Target missed", Message: msg,
			Verbs: []string{model.VerbView},
		}
		if !r.on || !r.settings.Problems {
			// Kept so a later check does not record it again.
			n.State, n.CreatedAt, n.Note = model.NotifyDone, r.now, "Recorded; notifications off"
			_, err := r.s.Store.SaveNotification(n)
			r.fail(err)
			return
		}
		r.send(n, true)
		return
	}
	if r.on && r.settings.Problems && r.allowed(true) {
		r.send(model.Notification{
			Key: fmt.Sprintf("unplugged:%s:%d", key, r.now.Unix()), Kind: model.NotifyUnplugged, Subject: key,
			Title:   "Unplugged early",
			Message: fmt.Sprintf("Unplugged at %d%%; %s needs %d%% by %s.", at, r.title(next), next.TargetSoC, r.s.clockText(next.DepartAt)),
			Verbs:   []string{model.VerbView},
		}, true)
	}
}

// ready tells the phone when a target is reached that needed charging
// while plugged in (optional).
func (r *reminders) ready(cur *notifyState) {
	target := r.view.Control.Target
	if target == nil || !r.lp.Connected {
		return
	}
	id := fmt.Sprintf("%s:%d", target.TripKey, target.SoC)
	if r.lp.VehicleSoc < float64(target.SoC) {
		cur.below[id] = true
		return
	}
	if !cur.below[id] || !r.settings.Ready || !r.allowed(false) {
		return
	}
	delete(cur.below, id)
	r.send(model.Notification{
		Key: "ready:" + id, Kind: model.NotifyReady, Subject: target.TripKey,
		Title: "Ready for " + target.Title, Message: fmt.Sprintf("Charged to %d%% for %s at %s.",
			int(math.Round(r.lp.VehicleSoc)), target.Title, r.s.clockText(target.ReadyBy)),
		Verbs: []string{model.VerbView},
	}, true)
}

// questions asks about calendar events marked Needs review, once each.
func (r *reminders) questions() {
	if !r.settings.Questions || !r.allowed(false) {
		return
	}
	for _, t := range r.view.Trips {
		if !t.NeedsReview || !t.Enabled || t.State != "upcoming" {
			continue
		}
		r.send(model.Notification{
			Key: "question:" + t.Key, Kind: model.NotifyQuestion, Subject: t.Key, Title: "Is this a car trip?",
			Message: fmt.Sprintf("%q on %s at %s, %s.", t.Title, r.s.dayText(t.ArriveAt), t.ArriveLocal, t.Destination),
			Verbs:   []string{model.VerbCount, model.VerbNoTrip, model.VerbView},
		}, false)
	}
}
