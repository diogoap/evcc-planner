package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/diogoap/evcc-planner/internal/advice"
	"github.com/diogoap/evcc-planner/internal/audit"
	"github.com/diogoap/evcc-planner/internal/evcc"
	"github.com/diogoap/evcc-planner/internal/localtime"
	"github.com/diogoap/evcc-planner/internal/model"
	"github.com/diogoap/evcc-planner/internal/store"
)

// pricesMeta holds where the published prices ended at the last review.
const pricesMeta = "prices.reviewed"

// newPricesGap is how much further the published prices must reach to be
// a new publication (tomorrow's, around 14:00).
const newPricesGap = time.Hour

// pricesEnd is the end of EVCC's last published slot (zero without any).
func pricesEnd(slots []evcc.Rate) time.Time {
	var last time.Time
	for _, r := range slots {
		if r.End.After(last) {
			last = r.End
		}
	}
	return last
}

// ReviewNewPrices acts once on each new publication of EVCC's prices
// (AUTOMATION.md): plans created automatically from advice follow the new
// advice (replaced, or ended when it no longer supports them); in automatic
// and AI mode, plans you chose (a plan set in EVCC or sent by hand, or an
// extra plan you created) are raised when the planner now wants more, never
// lowered; and advice auto-apply runs at once. It runs after each EVCC check
// (after AI mode's new plan) on a fresh EVCC state; the first look after a
// start only learns where the prices end.
func (s *Service) ReviewNewPrices(ctx context.Context) error {
	ctx = audit.WithSource(ctx, model.SourceAutomation)
	snap := s.snapshot()
	if !s.evccFresh(snap) {
		return nil
	}
	end := pricesEnd(snap.state.PriceSlots)
	if end.IsZero() {
		return nil
	}
	seen := end.UTC().Format(time.RFC3339)
	prev, err := s.Store.Meta(pricesMeta)
	if err != nil || prev == seen {
		return err
	}
	if err := s.Store.SetMeta(pricesMeta, seen); err != nil {
		return err
	}
	if last, err := time.Parse(time.RFC3339, prev); prev == "" || err != nil || end.Sub(last) < newPricesGap {
		return nil
	}
	s.Log.Info("new prices published", "until", end)
	settings, err := s.Settings()
	if err != nil {
		return err
	}
	errs := []error{s.reviewAdjustments(ctx, settings)}
	if settings.KeepsPlan() {
		errs = append(errs, s.raiseChosenPlan(ctx))
	}
	return errors.Join(append(errs, s.AutoApply(ctx))...)
}

// reviewAdjustments makes the active temporary EVCC changes follow the new
// advice: one created automatically is replaced when the advice changed and
// ended when it no longer supports it; an extra plan you created is raised
// to a higher charge extra target (automatic and AI mode only).
func (s *Service) reviewAdjustments(ctx context.Context, settings model.Settings) error {
	list, err := s.Store.Adjustments(localtime.Date(s.now(), s.loc()))
	if err != nil {
		return err
	}
	var active []model.Adjustment
	for _, a := range list {
		if a.Status == model.AdjustActive {
			active = append(active, a)
		}
	}
	if len(active) == 0 {
		return nil
	}
	view, err := s.Plan(ctx)
	if err != nil {
		return err
	}
	policy, err := s.autoPolicy()
	if err != nil {
		return err
	}
	days := map[string]*DayAdviceView{}
	if view.Advice != nil {
		for i := range view.Advice.Days {
			days[view.Advice.Days[i].Date] = &view.Advice.Days[i]
		}
	}
	var errs []error
	for i := range active {
		a, day := &active[i], days[active[i].AdviceDate]
		switch {
		case a.Source == "auto" && a.Kind == model.AdjustExtraPlan:
			errs = append(errs, s.reviewAutoExtra(ctx, a, day, view, policy))
		case a.Source == "auto" && a.Kind == model.AdjustPostpone:
			if day == nil || day.Recommendation != advice.Defer || (day.AI && !day.AIPostpone) {
				errs = append(errs, s.endAdjustment(ctx, a, "New prices: today's advice is no longer to defer. "))
			}
		case a.Source == "ui" && a.Kind == model.AdjustExtraPlan && settings.KeepsPlan():
			errs = append(errs, s.raiseExtraPlan(ctx, a, day))
		}
	}
	return errors.Join(errs...)
}

// chargesExtra reports whether a day's advice is to charge extra from the
// grid with a target.
func chargesExtra(day *DayAdviceView) bool {
	return day != nil && day.Recommendation == advice.ChargeExtra && !day.Solar && len(day.Blocks) > 0 && day.TargetSoC > 0
}

// reviewAutoExtra ends an extra plan made automatically when the new advice
// no longer supports it, and replaces it when its target or ready-by time
// changed.
func (s *Service) reviewAutoExtra(ctx context.Context, a *model.Adjustment, day *DayAdviceView, view *PlanView, policy autoPolicy) error {
	if !chargesExtra(day) || !actionable(day.Advice) || !policy.acts(day) {
		return s.endAdjustment(ctx, a, "New prices: the advice no longer charges extra. ")
	}
	soc, ready := day.TargetSoC, day.Blocks[len(day.Blocks)-1].Until
	if next := view.Next; next != nil {
		soc = max(soc, next.TargetSoC)
		ready = minTime(ready, next.DepartAt)
	}
	if samePlan(a.Plan, &model.PlanValue{SoC: float64(soc), Time: ready.Truncate(time.Minute)}) {
		return nil
	}
	why := fmt.Sprintf("New prices: the advice now charges extra to %d%% by %s. ", soc, s.clockText(ready))
	if err := s.endAdjustment(ctx, a, why); err != nil {
		return err
	}
	return s.autoApply(ctx, true)
}

// endAdjustment ends an active adjustment now and restores the previous
// value, as End now does, with the reason first in its note.
func (s *Service) endAdjustment(ctx context.Context, a *model.Adjustment, why string) error {
	s.adjustMu.Lock()
	err := s.settle(ctx, a, true, why)
	s.adjustMu.Unlock()
	if err != nil {
		return err
	}
	return s.PollEVCC(ctx)
}

// raiseExtraPlan raises an extra plan you created to the new advice's higher
// charge extra target, keeping its ready-by time. EVCC's plan is written now
// when it is that extra plan; when the trip target was merged into it, the
// automatic check writes the higher one.
func (s *Service) raiseExtraPlan(ctx context.Context, a *model.Adjustment, day *DayAdviceView) error {
	if !chargesExtra(day) || a.Plan == nil || day.TargetSoC <= int(math.Round(a.Plan.SoC)) || !a.Plan.Time.After(s.now()) {
		return nil
	}
	s.adjustMu.Lock()
	defer s.adjustMu.Unlock()
	old := *a.Plan
	a.Plan = &model.PlanValue{SoC: float64(day.TargetSoC), Time: old.Time}
	a.Note = fmt.Sprintf("Extra plan %d%% by %s", day.TargetSoC, dateTimeText(old.Time, s.loc()))
	ev := model.AuditEvent{
		Action: "adjustment.update", SubjectType: "adjustment", SubjectID: strconv.FormatInt(a.ID, 10), Related: "plan",
		Summary: fmt.Sprintf("New prices: raised the extra plan to %d%% by %s; the advice now charges extra to %d%%",
			day.TargetSoC, dateTimeText(old.Time, s.loc()), day.TargetSoC),
		Before: audit.Marshal(map[string]string{"extraPlan": s.adjustPlanText(&old)}),
		After:  audit.Marshal(map[string]string{"extraPlan": s.adjustPlanText(a.Plan)}),
	}
	var write *model.PlanWrite
	if snap := s.snapshot(); snap.state != nil {
		if v, ok := snap.state.Vehicles[a.Vehicle]; ok && v.Plan != nil &&
			samePlan(&model.PlanValue{SoC: v.Plan.Soc, Time: v.Plan.Time}, &old) {
			if _, err := s.Clients().EVCC.SetSocPlan(ctx, a.Vehicle, day.TargetSoC, old.Time); err != nil {
				ev.Outcome, ev.Error = model.OutcomeFailure, err.Error()
				s.record(ctx, ev)
				return err
			}
			write = &model.PlanWrite{Vehicle: a.Vehicle, SoC: day.TargetSoC, TargetTime: old.Time, TripKey: "advice:" + a.AdviceDate, WrittenAt: s.now()}
		}
	}
	if err := s.change(ctx, ev, func(tx *store.Store, e *model.AuditEvent) error {
		if write != nil {
			if _, err := tx.AddPlanWrite(*write); err != nil {
				return err
			}
		}
		return tx.UpdateAdjustment(*a)
	}); err != nil {
		return err
	}
	if write == nil {
		s.Kick()
	}
	return nil
}

// raiseChosenPlan raises a plan you chose (set in EVCC or sent by hand),
// which pauses automatic plans, to the planner's target when that is higher
// now: the higher SoC by the earlier time. It stays a chosen plan.
func (s *Service) raiseChosenPlan(ctx context.Context) error {
	s.autoPlanMu.Lock()
	defer s.autoPlanMu.Unlock()
	ctl, err := s.Store.PlanControl()
	if err != nil || !ctl.Paused() {
		return err
	}
	snap := s.snapshot()
	veh, ok := s.resolveVehicle(snap.state)
	lp, lpOK := s.loadpoint(snap.state)
	if !ok || !lpOK || veh.Plan == nil || !veh.Plan.Time.After(s.now()) {
		return nil
	}
	view, err := s.Plan(ctx)
	if err != nil {
		return err
	}
	t := view.Control.Target
	if t == nil || int(math.Round(veh.Plan.Soc)) >= t.SoC || lp.VehicleSoc >= float64(t.SoC) {
		return nil
	}
	want := &model.PlanValue{SoC: float64(t.SoC), Time: minTime(veh.Plan.Time, t.ReadyBy).Truncate(time.Minute)}
	ctl.PausedPlan = want
	_, err = s.SendPlan(ctx, SendPlanInput{
		SoC: t.SoC, Time: want.Time, TripKey: t.TripKey, Confirm: true, auto: true, control: &ctl,
		Reason: fmt.Sprintf("New prices: raised the chosen plan (%s)", s.planText(veh.Plan.Soc, veh.Plan.Time, true)),
	})
	return err
}
