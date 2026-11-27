package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/diogoap/evcc-planner/internal/advice"
	"github.com/diogoap/evcc-planner/internal/audit"
	"github.com/diogoap/evcc-planner/internal/evcc"
	"github.com/diogoap/evcc-planner/internal/homeassistant"
	"github.com/diogoap/evcc-planner/internal/localtime"
	"github.com/diogoap/evcc-planner/internal/model"
	"github.com/diogoap/evcc-planner/internal/planner"
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
	// The car location sensor's last known state: away, or home.
	away, awayKnown bool
	// Where the published prices ended, and what the plan sent then.
	pricesUntil time.Time
	plan        planSig
}

// planSig is what the plan sends: the target for the next departure while
// it needs charging, and the charge extra plan of today or tomorrow.
type planSig struct {
	soc   int
	ready time.Time
	extra string
}

// Timings of the notification rules (AUTOMATION.md).
var (
	stoppedAfter    = 10 * time.Minute // a planned charge idle this long has stopped
	modeAfter       = 10 * time.Minute // plugged in this long outside Smart mode is a problem
	unpluggedBefore = 30 * time.Minute // unplugging earlier than this before a departure is early
	beforeQuiet     = 15 * time.Minute // plug-in reminders due in quiet hours come this long before them
	arrivalWindow   = 30 * time.Minute // the location sensor turned home this recently: an arrival
	arrivalHold     = 30 * time.Minute // other non-urgent plug-in reminders wait this long after a back-home one
	retryLate       = 15 * time.Minute // a failed send not tried again within this of its time is given up
)

// reminderKinds ask the driver to plug in; plugging in ends them.
var reminderKinds = map[string]bool{model.NotifyEarly: true, model.NotifyUrgent: true, model.NotifyLate: true}

// CheckNotifications sends the phone notifications that are due: plug-in
// reminders (for trips, the sun, charge extra, back home, and the evening), charging problems,
// questions about calendar events, and ready messages, and ends those no
// longer needed (AUTOMATION.md). It runs
// after each EVCC check and only acts on a fresh EVCC state. A missed
// target is recorded in the activity log even with notifications off.
func (s *Service) CheckNotifications(ctx context.Context) error {
	ctx = audit.WithSource(ctx, model.SourceAutomation)
	s.notifyMu.Lock()
	defer s.notifyMu.Unlock()
	snap := s.snapshot()
	if !s.evccFresh(snap) {
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
		cur.away, cur.awayKnown = prev.away, prev.awayKnown
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
	r.planMessages()
	r.problems(cur)
	r.ready(cur)
	r.cleanUp()
	r.newPrices(prev, cur)
	r.arrived(prev, cur)
	r.evening()
	r.plugIn()
	r.solar()
	r.extra()
	r.snoozed()
	r.retry()
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
	// The car location sensor, read once per check.
	loc     *homeassistant.EntityState
	locRead bool
}

func (r *reminders) err() error { return errors.Join(r.errs...) }

func (r *reminders) fail(err error) {
	if err != nil {
		r.errs = append(r.errs, err)
	}
}

// quiet reports whether t falls in the quiet hours.
func (r *reminders) quiet(t time.Time) bool {
	return r.settings.Quiet(t.In(r.s.loc()).Format("15:04"))
}

// clockAt is the last time at or before t that the local clock reads hhmm.
func (r *reminders) clockAt(t time.Time, hhmm string) time.Time {
	h, m, _ := localtime.ParseClock(hhmm)
	day := localtime.StartOfDay(t, r.s.loc())
	at := localtime.OnDay(day, h, m)
	if at.After(t) {
		at = localtime.OnDay(localtime.AddDays(day, -1), h, m)
	}
	return at
}

// clockAfter is the first time after t that the local clock reads hhmm.
func (r *reminders) clockAfter(t time.Time, hhmm string) time.Time {
	h, m, _ := localtime.ParseClock(hhmm)
	return localtime.OnDay(localtime.AddDays(localtime.StartOfDay(r.clockAt(t, hhmm), r.s.loc()), 1), h, m)
}

// remindAt is when the early or charge extra reminder for charging from
// start comes: lead before it, or just before quiet hours when that falls
// in them.
func (r *reminders) remindAt(start time.Time, lead time.Duration) time.Time {
	due := start.Add(-lead)
	if r.quiet(due) {
		due = r.clockAt(due, r.settings.QuietFrom).Add(-beforeQuiet)
	}
	return due
}

// tonight is the evening reminder's night: from the last evening time
// until quiet hours end the next morning.
func (r *reminders) tonight() (from, until time.Time) {
	from = r.clockAt(r.now, r.settings.EveningAt)
	return from, r.clockAfter(from, r.settings.QuietTo)
}

// allowed reports whether a notification may be sent now: urgent ones in
// quiet hours only when the settings say so.
func (r *reminders) allowed(urgent bool) bool {
	return !r.quiet(r.now) || (urgent && r.settings.UrgentInQuiet)
}

// allowedNow is allowed for a notification sent before: a back-home
// reminder follows its own quiet-hours setting, and car heating's messages
// come in quiet hours too (the car is about to leave).
func (r *reminders) allowedNow(n *model.Notification) bool {
	switch n.Kind {
	case model.NotifyArrived:
		return r.arrivalAllowed()
	case model.NotifyHeatAsk, model.NotifyHeatOn:
		return true
	}
	return r.allowed(n.Urgent)
}

// arrivalAllowed reports whether a back-home reminder may be sent now: in
// quiet hours when its setting says so (on by default), as the car needs
// charging and you're at it.
func (r *reminders) arrivalAllowed() bool {
	return !r.quiet(r.now) || r.settings.ArrivalInQuiet
}

// send delivers a notification once per key. Informational ones are done
// right away; a reminder replaces open reminders for the same trip.
func (r *reminders) send(n model.Notification) {
	if existing, err := r.s.Store.NotificationByKey(n.Key); err != nil || existing != nil {
		r.fail(err)
		return
	}
	if reminderKinds[n.Kind] {
		r.endOpen(func(o model.Notification) bool { return reminderKinds[o.Kind] && o.Subject == n.Subject },
			"Replaced by a newer reminder")
	}
	// A later plug-in reminder says it again, and more precisely.
	if reminderKinds[n.Kind] || n.Kind == model.NotifySolar || n.Kind == model.NotifyExtra || n.Kind == model.NotifyEvening {
		r.endOpen(func(o model.Notification) bool { return o.Kind == model.NotifyArrived || o.Kind == model.NotifyEvening },
			"Replaced by a newer reminder")
	}
	if err := r.s.deliver(r.ctx, &n, r.settings); err != nil {
		r.fail(err)
		return
	}
	if infoKinds[n.Kind] {
		n.State = model.NotifyDone
		_, err := r.s.Store.SaveNotification(n)
		r.fail(err)
	}
}

// planMessageMaxAge is how long a message about a plan may wait for a
// check (EVCC unreachable meanwhile) before it is too old to send.
const planMessageMaxAge = 15 * time.Minute

// planMessages sends the messages about plans the planner made by itself
// since the last check (Plan sent, Extra plan created): only the one with
// the highest target, so plans made at the same time come as one message.
// The others are done without being sent; that departure or extra plan
// counts as told.
func (r *reminders) planMessages() {
	list, err := r.s.Store.PendingNotifications()
	if err != nil || len(list) == 0 {
		r.fail(err)
		return
	}
	best := -1
	for i, n := range list {
		if r.now.Sub(n.CreatedAt) <= planMessageMaxAge && (best < 0 || n.PlanSoC >= list[best].PlanSoC) {
			best = i
		}
	}
	for i := range list {
		if i == best {
			continue
		}
		n := &list[i]
		n.State, n.ResolvedAt, n.Note = model.NotifyDone, &r.now, "Not sent: too old"
		if best >= 0 {
			n.Note = fmt.Sprintf("Not sent: the message about a %d%% plan came at the same time", list[best].PlanSoC)
		}
		_, err := r.s.Store.SaveNotification(*n)
		r.fail(err)
	}
	if best < 0 {
		return
	}
	n := list[best]
	if err := r.s.deliver(r.ctx, &n, r.settings); err != nil {
		r.fail(err)
		return
	}
	n.State, n.Note = model.NotifyDone, "Information"
	_, err = r.s.Store.SaveNotification(n)
	r.fail(err)
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

// location is the optional car location sensor's state, read once per
// check; nil without one, or when it cannot be read.
func (r *reminders) location() *homeassistant.EntityState {
	if r.locRead {
		return r.loc
	}
	r.locRead = true
	r.loc = r.s.carLocation(r.ctx, r.settings.LocationEntity)
	return r.loc
}

// carAway reports whether the optional car location sensor says the car
// is not home; without one (or when it cannot be read) the car counts as
// home.
func (r *reminders) carAway() bool {
	e := r.location()
	return e != nil && e.State != "home"
}

// arrived asks to plug in when the car gets back home (the location sensor
// turns home from elsewhere) from the day's last trip, unplugged, while it
// still needs charging: for the next departure, for later trips charged
// ahead, or for charge extra's plan. Once per arrival. It may come in quiet
// hours (its own setting, on by default): you're at the car.
func (r *reminders) arrived(prev, cur *notifyState) {
	e := r.location()
	if e == nil || e.State == "" || e.State == "unknown" || e.State == "unavailable" {
		return
	}
	home := e.State == "home"
	cur.away, cur.awayKnown = !home, true
	if !home || prev == nil || !prev.awayKnown || !prev.away || r.now.Sub(e.LastChanged) > arrivalWindow ||
		!r.settings.Arrival || r.lp.Connected || !r.arrivalAllowed() {
		return
	}
	loc := r.s.loc()
	next, key := r.next()
	if next != nil && localtime.Date(next.DepartAt, loc) == localtime.Date(r.now, loc) {
		return // another trip today: after that one
	}
	var parts []string
	var latest *time.Time
	later := func(t time.Time) {
		if latest == nil || t.After(*latest) {
			latest = &t
		}
	}
	target := r.view.Control.Target
	switch {
	case next == nil:
	case target != nil && !target.Covered && len(target.Ahead) > 0:
		parts = append(parts, fmt.Sprintf("The plan charges %d%% by %s, also for %s.", target.SoC, r.s.clockText(target.ReadyBy), target.Ahead[0].Title))
		later(next.DepartAt)
	case next.Status == string(planner.StatusCharge) || next.Status == string(planner.StatusShortfall) ||
		next.Status == string(planner.StatusExceeds):
		parts = append(parts, fmt.Sprintf("%s needs %d%% by %s.", r.title(next), next.TargetSoC, r.s.clockText(next.DepartAt)))
		later(next.DepartAt)
	}
	if a := r.extraAdvice(); a != nil {
		text := a.Summary
		if a.Adjustment == nil && !r.autoExtra(a) {
			text += " Create the plan in the planner."
		}
		parts = append(parts, text)
		later(a.Blocks[len(a.Blocks)-1].Until)
	}
	if len(parts) == 0 {
		return
	}
	r.send(model.Notification{
		Key: fmt.Sprintf("arrived:%d", e.LastChanged.Unix()), Kind: model.NotifyArrived, Subject: key, Urgent: true, Latest: latest,
		Title: "Back home: plug in", Message: strings.Join(parts, " "),
		Verbs: []string{model.VerbSnooze, model.VerbView},
	})
}

// arrivedRecently reports whether a back-home reminder went out in the last
// half hour and is still open: other non-urgent plug-in reminders wait.
func (r *reminders) arrivedRecently() bool {
	open, err := r.s.Store.OpenNotifications()
	if err != nil {
		r.fail(err)
		return false
	}
	for _, n := range open {
		if n.Kind == model.NotifyArrived && n.SentAt != nil && r.now.Sub(*n.SentAt) < arrivalHold {
			return true
		}
	}
	return false
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
		case n.Kind == model.NotifySolar:
			a := r.solarAdvice()
			return r.lp.Connected || a == nil || n.Subject != "solar:"+a.Date || !r.now.Before(*a.SolarUntil)
		case n.Kind == model.NotifyExtra:
			a := r.extraAdvice()
			return r.lp.Connected || a == nil || n.Subject != "extra:"+a.Date || !r.now.Before(a.Blocks[len(a.Blocks)-1].Until)
		case n.Kind == model.NotifyArrived || n.Kind == model.NotifyEvening:
			return r.lp.Connected || r.carAway() || (n.Latest != nil && !r.now.Before(*n.Latest))
		case n.Kind == model.NotifyPrices:
			return r.lp.Connected
		}
		return false
	}, "No longer needed")
}

// planSig is what the plan sends now.
func (r *reminders) planSig() planSig {
	var p planSig
	if t := r.view.Control.Target; t != nil && !t.Covered {
		p.soc, p.ready = t.SoC, t.ReadyBy
	}
	if a := r.extraAdvice(); a != nil {
		p.extra = fmt.Sprintf("%s:%d:%d", a.Date, a.TargetSoC, a.Blocks[len(a.Blocks)-1].Until.Unix())
	}
	return p
}

// newPrices tells the phone, in automatic and AI mode, when new prices
// change what the plan sends, or bring charge extra, while the car is
// unplugged: once per publication, outside quiet hours. Plugging in clears
// it.
func (r *reminders) newPrices(prev, cur *notifyState) {
	if snap := r.s.snapshot(); snap.state != nil {
		cur.pricesUntil = pricesEnd(snap.state.PriceSlots)
	}
	cur.plan = r.planSig()
	if prev == nil || prev.pricesUntil.IsZero() || cur.pricesUntil.Sub(prev.pricesUntil) < newPricesGap ||
		!r.settings.Prices || !r.view.Settings.KeepsPlan() || r.lp.Connected || !r.allowed(false) {
		return
	}
	var parts []string
	was, now := prev.plan, cur.plan
	if t := r.view.Control.Target; now.soc > 0 && (now.soc != was.soc || !now.ready.Equal(was.ready)) {
		text := fmt.Sprintf("The plan for %s is now %d%% by %s", t.Title, t.SoC, r.s.clockText(t.ReadyBy))
		if was.soc > 0 && was.soc != now.soc {
			text += fmt.Sprintf(" (was %d%%)", was.soc)
		}
		if len(t.Ahead) > 0 {
			text += ", also for " + t.Ahead[0].Title
		}
		parts = append(parts, text+".")
	}
	if a := r.extraAdvice(); a != nil && now.extra != was.extra {
		parts = append(parts, a.Summary+r.extraPlanNote(a))
	}
	if len(parts) == 0 {
		return
	}
	r.send(model.Notification{
		Key: fmt.Sprintf("prices:%d", cur.pricesUntil.Unix()), Kind: model.NotifyPrices, Title: "New prices: plug in",
		Message: strings.Join(parts, " ") + " Plug in so it can charge.", Verbs: []string{model.VerbView},
	})
}

// extraPlanNote says what becomes of charge extra's plan (extraPlanWhat).
func (r *reminders) extraPlanNote(a *DayAdviceView) string {
	switch {
	case a.Adjustment != nil && a.Adjustment.Source == "auto":
		return " The extra plan was created automatically."
	case a.Adjustment != nil:
		return ""
	case a.Date != localtime.Date(r.now, r.s.loc()) && r.autoExtra(a):
		return " The plan is created automatically when the day starts."
	}
	return " Create the plan in the planner."
}

// evening is an extra plug-in reminder at the evening time, while there is
// time to go out and plug in: the car is home and unplugged, and charging
// is planned for tonight (until quiet hours end) for the next departure or
// charge extra, whose usual reminder is still to come. Those still come if
// the car isn't plugged in by then, and replace it on the phone.
func (r *reminders) evening() {
	if !r.settings.Evening || r.lp.Connected || r.onTheRoad() || r.carAway() {
		return
	}
	from, until := r.tonight()
	if !r.now.Before(until) || !r.allowed(false) || r.arrivedRecently() {
		return
	}
	date := localtime.Date(from, r.s.loc())
	subject := "evening:" + date
	var parts []string
	var latest *time.Time
	next, key := r.next()
	if target := r.view.Control.Target; next != nil && target != nil && !target.Covered && len(target.Charging) > 0 {
		first, last := target.Charging[0], target.Charging[len(target.Charging)-1]
		latestStart := next.LatestChargeStart
		if first.Start.Before(until) && r.now.Before(r.remindAt(first.Start, time.Duration(r.settings.EarlyLeadMin)*time.Minute)) &&
			(latestStart == nil || r.now.Before(latestStart.Add(-time.Duration(r.settings.UrgentLeadMin)*time.Minute))) {
			parts = append(parts, r.earlyText(target, first))
			subject, latest = key, &last.End
		}
	}
	if a := r.extraAdvice(); a != nil {
		start, end := a.Blocks[0].From, a.Blocks[len(a.Blocks)-1].Until
		if start.Before(until) && r.now.Before(r.remindAt(start, 30*time.Minute)) {
			parts = append(parts, a.Summary+r.extraPlanNote(a))
			if latest == nil || end.After(*latest) {
				latest = &end
			}
		}
	}
	if len(parts) == 0 {
		return
	}
	r.send(model.Notification{
		Key: "evening:" + date, Kind: model.NotifyEvening, Subject: subject, Latest: latest,
		Title: "Plug in tonight", Message: strings.Join(parts, " "),
		Verbs: []string{model.VerbSnooze, model.VerbView},
	})
}

// solarAdvice is today's advice when it is to charge extra from the sun and
// has not been dismissed. Any confidence will do: plugging in changes
// nothing in EVCC by itself.
func (r *reminders) solarAdvice() *DayAdviceView {
	if r.view.Advice == nil {
		return nil
	}
	today := localtime.Date(r.now, r.s.loc())
	for i := range r.view.Advice.Days {
		a := &r.view.Advice.Days[i]
		if a.Date == today && a.Solar && !a.Dismissed && a.SolarFrom != nil && a.SolarUntil != nil {
			return a
		}
	}
	return nil
}

// solar asks to plug in while the car is home and unplugged on a day whose
// advice is to charge extra from the sun: from half an hour before the
// solar surplus starts until half an hour before it ends. EVCC charges from
// the sun by itself once the car is plugged in.
func (r *reminders) solar() {
	if !r.settings.Solar || r.lp.Connected || r.onTheRoad() || r.carAway() {
		return
	}
	a := r.solarAdvice()
	if a == nil || r.now.Before(a.SolarFrom.Add(-30*time.Minute)) || !r.now.Before(a.SolarUntil.Add(-30*time.Minute)) ||
		!r.allowed(false) || r.arrivedRecently() {
		return
	}
	than := "no more than tomorrow's lowest price"
	if ref := a.Reference; ref != nil {
		switch ref.Basis {
		case advice.RefOutlook:
			than = "no more than " + ref.Day + "'s expected lowest price"
		case advice.RefHistory:
			than = "no more than this week's lowest prices"
		}
	}
	if a.AI {
		than = "which the AI plan advises to use"
	}
	r.send(model.Notification{
		Key: "solar:" + a.Date, Kind: model.NotifySolar, Subject: "solar:" + a.Date, Latest: a.SolarUntil,
		Title: "Plug in for the sun",
		Message: fmt.Sprintf("Sunshine until about %s charges the car at about €%.2f/kWh (€%.2f without the tax on own solar), %s. Plug in to use it.",
			r.s.clockText(*a.SolarUntil), solarGridPrice(a), *a.SolarPrice, than),
		Verbs: []string{model.VerbSnooze, model.VerbView},
	})
}

// extraAdvice is today's or tomorrow's advice, the first whose cheap
// blocks are still ahead, when it is to charge extra from the grid with a
// plan, offered or made, and has not been dismissed. Tomorrow's matters
// for a cheap night, which starts in quiet hours. While a trip before its
// cheap blocks is still ahead or under way, there is no point plugging in
// for it yet: the car leaves again first.
func (r *reminders) extraAdvice() *DayAdviceView {
	if r.view.Advice == nil {
		return nil
	}
	today := localtime.Date(r.now, r.s.loc())
	tomorrow := localtime.Date(localtime.AddDays(localtime.StartOfDay(r.now, r.s.loc()), 1), r.s.loc())
	for i := range r.view.Advice.Days {
		a := &r.view.Advice.Days[i]
		planned := a.Adjustment != nil && a.Adjustment.Kind == model.AdjustExtraPlan
		if (a.Date == today || a.Date == tomorrow) && a.Recommendation == advice.ChargeExtra && !a.Solar && !a.Dismissed &&
			len(a.Blocks) > 0 && a.Blocks[len(a.Blocks)-1].Until.After(r.now) && (a.Action == model.AdjustExtraPlan || planned) &&
			!r.tripBefore(a.Blocks[0].From) {
			return a
		}
	}
	return nil
}

// tripBefore reports whether a trip that leaves before t is not over yet.
// With a car location sensor, one under way is over once the car is home,
// also before its planned return.
func (r *reminders) tripBefore(t time.Time) bool {
	for _, b := range r.view.Blocks {
		if !b.DepartAt.Before(t) || !b.ReturnAt.After(r.now) {
			continue
		}
		if e := r.location(); b.DepartAt.After(r.now) || e == nil || e.State != "home" {
			return true
		}
	}
	return false
}

// extra asks to plug in while the car is home and unplugged on a day whose
// advice is to charge extra with a plan: from half an hour before its cheap
// blocks start (or earlier, see remindAt) until half an hour before they
// end. Without the plan yet, it also asks to create it, unless advice
// auto-apply will when tomorrow starts.
func (r *reminders) extra() {
	if n, _ := r.extraReminder(); n != nil {
		r.send(*n)
	}
}

// extraReminder is charge extra's plug-in reminder (extra) with its advice
// when it is due now and was not sent yet, else nil.
func (r *reminders) extraReminder() (*model.Notification, *DayAdviceView) {
	if !r.settings.Extra || r.lp.Connected || r.onTheRoad() || r.carAway() {
		return nil, nil
	}
	a := r.extraAdvice()
	if a == nil {
		return nil, nil
	}
	from, until := a.Blocks[0].From, a.Blocks[len(a.Blocks)-1].Until
	due := r.remindAt(from, 30*time.Minute)
	if r.now.Before(due) || !r.now.Before(until.Add(-30*time.Minute)) || !r.allowed(false) || r.arrivedRecently() {
		return nil, nil
	}
	key := "extra:" + a.Date
	if old, err := r.s.Store.NotificationByKey(key); err != nil || old != nil {
		r.fail(err)
		return nil, nil
	}
	then := " Plug in so the plan can charge."
	switch {
	case a.Adjustment != nil:
	case a.Date != localtime.Date(r.now, r.s.loc()) && r.autoExtra(a):
		then = " The plan is created automatically when the day starts; plug in so it can charge."
	default:
		then = " Create the plan in the planner and plug in."
	}
	return &model.Notification{
		Key: key, Kind: model.NotifyExtra, Subject: key, Latest: &until,
		Title: "Plug in for charge extra", Message: a.Summary + then,
		Verbs: []string{model.VerbSnooze, model.VerbView},
	}, a
}

// sendWithExtra sends a plug-in reminder for the next departure, with
// charge extra's in the same message when that is due now too, so the
// phone gets one message asking to plug in. Charge extra's is then kept as
// not sent and counts as told.
func (r *reminders) sendWithExtra(n model.Notification) {
	if old, err := r.s.Store.NotificationByKey(n.Key); err != nil || old != nil {
		r.fail(err)
		return
	}
	x, a := r.extraReminder()
	if x != nil {
		n.Message += " " + a.Summary + r.extraPlanNote(a)
		if n.Latest == nil || x.Latest.After(*n.Latest) {
			n.Latest = x.Latest
		}
	}
	title := n.Title
	r.send(n)
	if x == nil {
		return
	}
	x.Title = phoneTitle(x.Kind, x.Title, r.view.Settings.TripPlans == model.TripPlansAI)
	x.State, x.CreatedAt, x.ResolvedAt = model.NotifyDone, r.now, &r.now
	x.Note = fmt.Sprintf("Not sent: said in “%s” at the same time", title)
	_, err := r.s.Store.SaveNotification(*x)
	r.fail(err)
}

// autoExtra reports whether advice auto-apply will create the day's extra
// plan once the day is today.
func (r *reminders) autoExtra(a *DayAdviceView) bool {
	policy, err := r.s.autoPolicy()
	r.fail(err)
	return err == nil && policy.acts(a)
}

// solarGridPrice is the grid price in the sun's slots (the solar price when
// it is missing).
func solarGridPrice(a *DayAdviceView) float64 {
	if a.SolarGridPrice != nil {
		return *a.SolarGridPrice
	}
	return *a.SolarPrice
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
		if n.State != model.NotifySnoozed || n.SnoozedUntil == nil || r.now.Before(*n.SnoozedUntil) || !r.allowedNow(n) {
			continue
		}
		if n.Latest != nil && !r.now.Before(*n.Latest) {
			continue // past its latest time; a later rule replaces it
		}
		n.Note = "Sent again after a snooze"
		r.fail(r.s.deliver(r.ctx, n, r.settings))
	}
}

// retry sends notifications again whose send failed, once their wait is
// over (retryAfter). cleanUp and newer reminders have already ended those
// no longer needed; it gives up past the notification's latest time, in
// quiet hours it may not come in, or when the checks were paused too long
// to try in time.
func (r *reminders) retry() {
	open, err := r.s.Store.OpenNotifications()
	if err != nil {
		r.fail(err)
		return
	}
	for i := range open {
		n := &open[i]
		if n.State != model.NotifyRetrying || n.RetryAt == nil || r.now.Before(*n.RetryAt) {
			continue
		}
		switch {
		case r.now.Sub(*n.RetryAt) > retryLate:
			r.fail(r.s.stopRetrying(r.ctx, n, model.NotifyFailed, "too late to try again"))
		case n.Latest != nil && !r.now.Before(*n.Latest):
			r.fail(r.s.stopRetrying(r.ctx, n, model.NotifyFailed, "past its latest time"))
		case !r.allowedNow(n):
			r.fail(r.s.stopRetrying(r.ctx, n, model.NotifyFailed, "quiet hours"))
		default:
			if err := r.s.deliver(r.ctx, n, r.settings); err != nil {
				r.fail(err)
			} else if infoKinds[n.Kind] {
				n.State = model.NotifyDone
				_, err := r.s.Store.SaveNotification(*n)
				r.fail(err)
			}
		}
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
		})
		return
	}
	// Urgent: shortly before the latest plug-in time.
	if latest != nil && !r.now.Before(latest.Add(-time.Duration(r.settings.UrgentLeadMin)*time.Minute)) {
		if !r.allowed(true) {
			return
		}
		r.sendWithExtra(model.Notification{
			Key: "urgent:" + key, Kind: model.NotifyUrgent, Subject: key, Urgent: true, Latest: latest,
			Title:   "Plug in by " + r.s.clockText(*latest),
			Message: fmt.Sprintf("%s needs %d%% by %s.", r.title(next), next.TargetSoC, r.s.clockText(next.DepartAt)),
			Verbs:   verbs,
		})
		return
	}
	// Early: before the first slot of the planned charging (or earlier, see
	// remindAt).
	if !r.settings.Early || target.Covered || len(target.Charging) == 0 {
		return
	}
	first, last := target.Charging[0], target.Charging[len(target.Charging)-1]
	due := r.remindAt(first.Start, time.Duration(r.settings.EarlyLeadMin)*time.Minute)
	if r.now.Before(due) || !r.now.Before(last.End) || !r.allowed(false) || r.arrivedRecently() {
		return
	}
	r.sendWithExtra(model.Notification{
		Key: "early:" + key, Kind: model.NotifyEarly, Subject: key, Latest: latest, Title: "Plug in to charge cheaply",
		Message: r.earlyText(target, first), Verbs: verbs,
	})
}

// earlyText says when the planned charging is cheapest and what it charges
// for.
func (r *reminders) earlyText(target *TargetView, first evcc.Rate) string {
	when := r.s.clockText(first.Start)
	if !first.Start.After(r.now) {
		when = "now"
	}
	if len(target.Ahead) > 0 {
		return fmt.Sprintf("Charging is cheapest from %s (%s/kWh); the plan charges %d%% by %s, also for %s.",
			when, priceText(first.Value), target.SoC, r.s.clockText(target.ReadyBy), target.Ahead[0].Title)
	}
	return fmt.Sprintf("Charging is cheapest from %s (%s/kWh); %s needs %d%% by %s.",
		when, priceText(first.Value), target.Title, target.SoC, r.s.clockText(target.ReadyBy))
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
		})
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
		r.send(n)
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
		r.send(n)
		return
	}
	if r.on && r.settings.Problems && r.allowed(true) {
		r.send(model.Notification{
			Key: fmt.Sprintf("unplugged:%s:%d", key, r.now.Unix()), Kind: model.NotifyUnplugged, Subject: key,
			Title:   "Unplugged early",
			Message: fmt.Sprintf("Unplugged at %d%%; %s needs %d%% by %s.", at, r.title(next), next.TargetSoC, r.s.clockText(next.DepartAt)),
			Verbs:   []string{model.VerbView},
		})
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
	})
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
		})
	}
}
