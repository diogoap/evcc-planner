package service

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/diogoap/evcc-planner/internal/audit"
	"github.com/diogoap/evcc-planner/internal/evcc"
	"github.com/diogoap/evcc-planner/internal/localtime"
	"github.com/diogoap/evcc-planner/internal/model"
	"github.com/diogoap/evcc-planner/internal/planner"
	"github.com/diogoap/evcc-planner/internal/store"
)

// connectedMeta remembers whether the car was plugged in at the last EVCC
// check, so a new connection is noticed exactly once, also across restarts.
const connectedMeta = "evcc.connected"

// ControlView is the state of trip plans shown on the Overview: the mode,
// a pause, and the target the automatic check sends.
type ControlView struct {
	Mode       string           `json:"mode"`
	Paused     bool             `json:"paused"`
	PausedAt   *time.Time       `json:"pausedAt,omitempty"`
	PausedPlan *model.PlanValue `json:"pausedPlan,omitempty"`
	// Target is the plan for the next departure; nil without one.
	Target *TargetView `json:"target"`
}

// TargetView is the plan for the next departure: its minimum target, plus
// later trips charged ahead (automatic mode).
type TargetView struct {
	SoC     int       `json:"soc"`
	MinSoC  int       `json:"minSoc"` // the next departure's own target
	ReadyBy time.Time `json:"readyBy"`
	TripKey string    `json:"tripKey"`
	Title   string    `json:"title"`
	// Covered: the projected SoC already reaches the target, so nothing
	// needs charging (unknown SoC: false).
	Covered bool `json:"covered"`
	// Ahead lists the later trips the target charges ahead, with the
	// prices that made it cheaper.
	Ahead     []AheadView `json:"ahead"`
	SavingEUR float64     `json:"savingEur"`
	// Charging is the planner's estimate of when the charge for the target
	// happens: the cheapest slots before the departure, solar first. EVCC
	// picks the actual slots.
	Charging   []evcc.Rate `json:"charging"`
	HorizonEnd *time.Time  `json:"horizonEnd,omitempty"`
}

// AheadView is a later trip charged ahead.
type AheadView struct {
	TripKey    string    `json:"tripKey"`
	Title      string    `json:"title"`
	DepartAt   time.Time `json:"departAt"`
	EnergyPct  float64   `json:"energyPct"`  // SoC points it adds
	PriceAhead float64   `json:"priceAhead"` // EUR/kWh before the next departure
	PriceLater *float64  `json:"priceLater"` // EUR/kWh at home before the trip; nil when that time is too short
	SavingEUR  float64   `json:"savingEur"`
}

// controlView builds the trip plan state for the plan view. In automatic
// mode with charging ahead on, the target includes later trips that are
// cheaper to charge before the next departure.
func (s *Service) controlView(settings model.Settings, ctl model.PlanControl, res planner.Result, p planner.Params,
	blocks []BlockView) ControlView {
	v := ControlView{Mode: settings.TripPlans}
	if settings.TripPlans == model.TripPlansAutomatic && ctl.Paused() {
		v.Paused, v.PausedAt, v.PausedPlan = true, ctl.PausedAt, ctl.PausedPlan
	}
	if res.Next < 0 {
		return v
	}
	next := blocks[res.Next]
	t := &TargetView{
		SoC: next.TargetSoC, MinSoC: next.TargetSoC, ReadyBy: next.DepartAt,
		TripKey: next.TripKeys[0], Title: next.FirstTitle, Ahead: []AheadView{}, Charging: []evcc.Rate{},
	}
	keep := map[string]bool{}
	for _, k := range ctl.Ahead {
		keep[k] = true
	}
	a := planner.ChargeAhead(res, p, s.aheadSlots(), planner.AheadParams{
		Enabled:   settings.TripPlans == model.TripPlansAutomatic && settings.ChargeAhead,
		MinSaving: settings.ChargeAheadMinSaving, MaxSoC: float64(settings.ChargeAheadMaxSoC),
		HouseholdKW: settings.HouseholdKW, SolarTaxSaving: settings.SolarTaxSaving, Keep: keep,
	})
	t.SoC = max(t.MinSoC, int(math.Min(100, math.Ceil(a.TargetSoC-1e-6))))
	for _, tr := range a.Trips {
		b := blocks[tr.Block]
		av := AheadView{
			TripKey: b.TripKeys[0], Title: b.FirstTitle, DepartAt: b.DepartAt, EnergyPct: round(tr.EnergyPct, 1),
			PriceAhead: round(tr.PriceAhead, 3), SavingEUR: round(tr.SavingEUR, 2),
		}
		if tr.Later {
			av.PriceLater = ptr(round(tr.PriceLater, 3))
		}
		t.Ahead = append(t.Ahead, av)
	}
	t.SavingEUR = round(a.SavingEUR, 2)
	for _, c := range a.Charging {
		t.Charging = append(t.Charging, evcc.Rate{Start: c.Start, End: c.End, Value: c.Price})
	}
	if !a.HorizonEnd.IsZero() {
		t.HorizonEnd = &a.HorizonEnd
	}
	t.Covered = next.StartSoC != nil && *next.StartSoC >= float64(t.SoC)-1e-6
	v.Target = t
	return v
}

// aheadSlots are EVCC's published prices from now on, with the average
// forecast solar power in each.
func (s *Service) aheadSlots() []planner.Slot {
	snap := s.snapshot()
	if snap.state == nil {
		return nil
	}
	now := s.now()
	var out []planner.Slot
	for _, r := range snap.state.PriceSlots {
		if !r.End.After(now) || !r.End.After(r.Start) {
			continue
		}
		sl := planner.Slot{Start: r.Start, End: r.End, Price: r.Value}
		if f := snap.state.Solar; f.Covers(r.End) {
			sl.SolarKW = f.EnergyKWh(r.Start, r.End) / r.End.Sub(r.Start).Hours()
		}
		out = append(out, sl)
	}
	return out
}

// aheadKeys lists the first trip key of each trip a target charges ahead.
func aheadKeys(t *TargetView) []string {
	if t == nil {
		return nil
	}
	var keys []string
	for _, a := range t.Ahead {
		keys = append(keys, a.TripKey)
	}
	return keys
}

// SyncPlan keeps EVCC's plan in line with the trips according to the trip
// plan mode (AUTOMATION.md). It runs after each EVCC check and shortly
// after changes to trips or settings, and only acts on a fresh EVCC state.
// The connection state is tracked in every mode, so the first check after
// a fresh start only learns it: a restart while plugged in is not a
// plug-in.
func (s *Service) SyncPlan(ctx context.Context) error {
	ctx = audit.WithSource(ctx, model.SourceAutomation)
	s.autoPlanMu.Lock()
	defer s.autoPlanMu.Unlock()
	snap := s.snapshot()
	if snap.state == nil || snap.err != nil || s.now().Sub(snap.okAt) > 3*s.Conn().EVCCPollInterval() {
		return nil
	}
	lp, ok := s.loadpoint(snap.state)
	if !ok {
		return nil
	}
	pluggedIn, err := s.noticePlugIn(lp.Connected)
	if err != nil {
		return err
	}
	settings, err := s.Settings()
	if err != nil {
		return err
	}
	switch {
	case settings.TripPlans == model.TripPlansPlugIn && pluggedIn:
		return s.planOnPlugIn(ctx, snap.state, lp)
	case settings.TripPlans == model.TripPlansAutomatic:
		return s.reconcile(ctx, snap.state, lp, pluggedIn)
	}
	return nil
}

// noticePlugIn stores the connection state and reports whether the car
// went from unplugged to plugged in since the last check.
func (s *Service) noticePlugIn(connected bool) (bool, error) {
	cur := "0"
	if connected {
		cur = "1"
	}
	prev, err := s.Store.Meta(connectedMeta)
	if err != nil || prev == cur {
		return false, err
	}
	if err := s.Store.SetMeta(connectedMeta, cur); err != nil {
		return false, err
	}
	return prev == "0" && cur == "1", nil
}

// planOnPlugIn sends the next departure's plan once when the car is
// plugged in (mode plug_in). It keeps a plan already in EVCC with an equal
// or higher SoC, whatever its ready-by time (someone chose it), replaces a
// lower one, and sends nothing when the car's charge already covers the
// departure. Each decision is recorded.
func (s *Service) planOnPlugIn(ctx context.Context, st *evcc.State, lp *evcc.Loadpoint) error {
	view, err := s.Plan(ctx)
	if err != nil {
		return err
	}
	veh, _ := s.resolveVehicle(st)
	next := view.Next
	ev := model.AuditEvent{Action: "plan.keep", SubjectType: "plan", SubjectID: veh.Name}
	if next != nil {
		ev.Related = "trip:" + next.TripKeys[0]
		ev.Detail = audit.Marshal(map[string]any{
			"tripKey": next.TripKeys[0], "targetSoc": next.TargetSoC, "departAt": s.localText(next.DepartAt),
		})
	}
	switch {
	case veh.Name == "":
		return nil
	case next == nil:
		ev.Summary = "Car plugged in: no upcoming trip, so no plan was sent"
	case veh.Plan != nil && int(math.Round(veh.Plan.Soc)) >= next.TargetSoC:
		ev.Summary = fmt.Sprintf("Car plugged in: kept the EVCC plan (%s); it has at least the %d%% %s needs",
			s.planText(veh.Plan.Soc, veh.Plan.Time, true), next.TargetSoC, next.FirstTitle)
	case lp.VehicleSoc > 0 && lp.VehicleSoc >= float64(next.TargetSoC):
		ev.Summary = fmt.Sprintf("Car plugged in at %d%%: enough for %s (%d%%), so no plan was sent",
			int(math.Round(lp.VehicleSoc)), next.FirstTitle, next.TargetSoC)
	default:
		s.Log.Info("car plugged in; sending the next departure's plan", "soc", next.TargetSoC, "time", next.DepartAt)
		_, err := s.SendPlan(ctx, SendPlanInput{
			SoC: next.TargetSoC, Time: next.DepartAt, TripKey: next.TripKeys[0], Confirm: true,
			Reason: "Car plugged in", auto: true,
		})
		return err
	}
	s.record(ctx, ev)
	return nil
}

// reconcile matches EVCC's plan to the automatic target while the car is
// plugged in: it writes the target when charging is needed, removes the
// planner's own plan when nothing needs it, merges an active extra plan
// from advice, and lets a chosen plan (set in EVCC or sent by hand) pause
// it while that plan covers the next departure. A chosen plan below the
// next departure's own target is replaced. While the car is unplugged it
// only ends a pause whose plan has finished.
func (s *Service) reconcile(ctx context.Context, st *evcc.State, lp *evcc.Loadpoint, pluggedIn bool) error {
	veh, ok := s.resolveVehicle(st)
	if !ok || veh.Name == "" {
		return nil
	}
	ctl, err := s.Store.PlanControl()
	if err != nil {
		return err
	}
	now := s.now()
	var cur *model.PlanValue
	if veh.Plan != nil {
		cur = &model.PlanValue{SoC: veh.Plan.Soc, Time: veh.Plan.Time}
	}
	if ctl.Paused() && (cur == nil || !cur.Time.After(now)) {
		if err := s.setPause(ctx, &ctl, nil, "Automatic plans resumed: the chosen plan has finished"); err != nil {
			return err
		}
	}
	if !lp.Connected {
		return nil
	}
	view, err := s.Plan(ctx)
	if err != nil {
		return err
	}
	target := view.Control.Target
	extra := s.activeExtraPlan(now)
	reason := "Automatic plan"
	if pluggedIn {
		reason = "Car plugged in"
	}
	// below reports whether a plan has less than the next departure needs.
	below := func(p *model.PlanValue) bool { return target != nil && int(math.Round(p.SoC)) < target.MinSoC }

	if ctl.Paused() {
		if !below(cur) {
			if !samePlan(cur, ctl.PausedPlan) {
				ctl.PausedPlan = cur
				return s.Store.SavePlanControl(ctl)
			}
			return nil
		}
		msg := fmt.Sprintf("Automatic plans resumed: %s now needs %d%%, more than the chosen plan (%s)",
			target.Title, target.MinSoC, s.planText(cur.SoC, cur.Time, true))
		if err := s.setPause(ctx, &ctl, nil, msg); err != nil {
			return err
		}
	}

	chosen := cur != nil && cur.Time.After(now) && !samePlan(cur, ctl.Managed) && !samePlan(cur, extra)
	if chosen {
		if !below(cur) {
			what := "plan chosen in EVCC"
			if s.planView(veh).SetByPlanner {
				what = "plan sent from the planner"
			}
			return s.setPause(ctx, &ctl, cur, fmt.Sprintf("Automatic plans paused: %s (%s)",
				what, s.planText(cur.SoC, cur.Time, true)))
		}
		reason = fmt.Sprintf("Plan in EVCC (%s) is below the %d%% %s needs",
			s.planText(cur.SoC, cur.Time, true), target.MinSoC, target.Title)
	}

	var want *model.PlanValue
	if target != nil && lp.VehicleSoc < float64(target.SoC)-1e-6 {
		want = &model.PlanValue{SoC: float64(target.SoC), Time: target.ReadyBy.Truncate(time.Minute)}
	}
	if extra != nil {
		want = mergePlans(want, extra)
	}
	switch {
	case want == nil:
		if cur != nil && samePlan(cur, ctl.Managed) {
			return s.removeManaged(ctx, veh.Name, &ctl, target)
		}
		return nil
	case samePlan(cur, want):
		if !samePlan(cur, extra) && !samePlan(ctl.Managed, want) {
			ctl.Managed, ctl.Ahead = want, aheadKeys(target)
			return s.Store.SavePlanControl(ctl)
		}
		return nil
	}
	ctl.Managed, ctl.Ahead = want, aheadKeys(target)
	in := SendPlanInput{
		SoC: int(math.Round(want.SoC)), Time: want.Time, Confirm: true, Reason: reason, auto: true, control: &ctl,
	}
	if target != nil {
		in.TripKey = target.TripKey
	}
	s.Log.Info("automatic plan", "soc", in.SoC, "time", in.Time, "reason", reason)
	_, err = s.SendPlan(ctx, in)
	return err
}

// removeManaged deletes the planner's own plan once nothing needs it.
func (s *Service) removeManaged(ctx context.Context, vehicle string, ctl *model.PlanControl, target *TargetView) error {
	prev := s.planText(ctl.Managed.SoC, ctl.Managed.Time, true)
	why := "no upcoming trip needs it"
	if target != nil {
		why = fmt.Sprintf("the charge already covers %s (%d%%)", target.Title, target.SoC)
	}
	ev := model.AuditEvent{
		Action: "plan.remove", SubjectType: "plan", SubjectID: vehicle,
		Summary: fmt.Sprintf("Automatic plan: removed the planner's plan (%s); %s", prev, why),
		Before:  audit.Marshal(map[string]string{"evccPlan": prev}),
		After:   audit.Marshal(map[string]string{"evccPlan": s.planText(0, time.Time{}, false)}),
	}
	if target != nil {
		ev.Related = "trip:" + target.TripKey
	}
	if err := s.Clients().EVCC.DeleteSocPlan(ctx, vehicle); err != nil {
		ev.Outcome, ev.Error = model.OutcomeFailure, err.Error()
		s.record(ctx, ev)
		return err
	}
	ctl.Managed, ctl.Ahead = nil, nil
	if err := s.change(ctx, ev, func(tx *store.Store, e *model.AuditEvent) error {
		return tx.SavePlanControl(*ctl)
	}); err != nil {
		return err
	}
	s.Log.Info("automatic plan removed", "vehicle", vehicle)
	return s.PollEVCC(ctx)
}

// setPause pauses automatic changes for a chosen plan, or resumes them
// (plan nil), with an activity entry.
func (s *Service) setPause(ctx context.Context, ctl *model.PlanControl, plan *model.PlanValue, summary string) error {
	ev := model.AuditEvent{Action: "control.resume", SubjectType: "plan", Summary: summary}
	before := map[string]string{"automaticPlans": "paused"}
	after := map[string]string{"automaticPlans": "active"}
	ctl.PausedAt, ctl.PausedPlan = nil, plan
	if plan != nil {
		now := s.now()
		ev.Action, ctl.PausedAt = "control.pause", &now
		before, after = after, before
	}
	ev.Before, ev.After = audit.Marshal(before), audit.Marshal(after)
	s.Log.Info("automatic plans", "paused", plan != nil, "summary", summary)
	return s.change(ctx, ev, func(tx *store.Store, e *model.AuditEvent) error {
		return tx.SavePlanControl(*ctl)
	})
}

// ResumeControl ends a pause from the UI: the plan in EVCC is handed to
// the planner, which updates or removes it right away.
func (s *Service) ResumeControl(ctx context.Context) error {
	settings, err := s.Settings()
	if err != nil {
		return err
	}
	if settings.TripPlans != model.TripPlansAutomatic {
		return invalid("trip plans are not automatic; choose Automatic under Settings → Planning")
	}
	s.autoPlanMu.Lock()
	ctl, err := s.Store.PlanControl()
	if err == nil && !ctl.Paused() {
		err = invalid("automatic plans are not paused")
	}
	if err == nil {
		ctl.Managed = ctl.PausedPlan
		if snap := s.snapshot(); snap.state != nil {
			if veh, ok := s.resolveVehicle(snap.state); ok && veh.Plan != nil {
				ctl.Managed = &model.PlanValue{SoC: veh.Plan.Soc, Time: veh.Plan.Time}
			}
		}
		err = s.setPause(ctx, &ctl, nil, "Automatic plans resumed")
	}
	s.autoPlanMu.Unlock()
	if err != nil {
		return err
	}
	return s.SyncPlan(ctx)
}

// activeExtraPlan returns the plan of an active extra plan from advice
// that is still ahead, or nil.
func (s *Service) activeExtraPlan(now time.Time) *model.PlanValue {
	list, err := s.Store.Adjustments(localtime.Date(now, s.loc()))
	if err != nil {
		s.Log.Warn("list adjustments", "err", err)
		return nil
	}
	for _, a := range list {
		if a.Status == model.AdjustActive && a.Kind == model.AdjustExtraPlan && a.Plan != nil && a.Plan.Time.After(now) {
			return a.Plan
		}
	}
	return nil
}

// samePlan reports whether two plans have the same whole-percent SoC and
// ready-by minute; nil equals only nil.
func samePlan(a, b *model.PlanValue) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return int(math.Round(a.SoC)) == int(math.Round(b.SoC)) && absDuration(a.Time.Sub(b.Time)) < time.Minute
}

// mergePlans combines the trip target with an extra plan: the higher SoC
// by the earlier time.
func mergePlans(trip, extra *model.PlanValue) *model.PlanValue {
	if trip == nil {
		return extra
	}
	return &model.PlanValue{SoC: math.Max(trip.SoC, extra.SoC), Time: minTime(trip.Time, extra.Time)}
}
