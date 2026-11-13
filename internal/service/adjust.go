package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/diogoap/evcc-planner/internal/audit"
	"github.com/diogoap/evcc-planner/internal/evcc"
	"github.com/diogoap/evcc-planner/internal/localtime"
	"github.com/diogoap/evcc-planner/internal/model"
	"github.com/diogoap/evcc-planner/internal/store"
)

// ProposalView is an editable extra plan or postponement, shown before
// anything is written to EVCC.
type ProposalView struct {
	Kind      string         `json:"kind"`
	Date      string         `json:"date"`
	Available bool           `json:"available"`
	Reason    string         `json:"reason,omitempty"`
	Advice    *DayAdviceView `json:"advice"`
	// Extra plan.
	SoC           int            `json:"soc,omitempty"`
	MinSoC        int            `json:"minSoc,omitempty"`
	StartSoC      *float64       `json:"startSoc"` // projected SoC when charging can start
	CapacityKWh   float64        `json:"capacityKWh"`
	ReadyBy       *time.Time     `json:"readyBy,omitempty"`
	LatestReadyBy *time.Time     `json:"latestReadyBy,omitempty"`
	Protected     *ProtectedView `json:"protected,omitempty"`
	CurrentPlan   *EVCCPlanView  `json:"currentPlan"`
	// Postponement: EVCC's current cheap-grid charging limit.
	SmartCostLimit *float64 `json:"smartCostLimit"`
	// When the previous EVCC value is restored.
	ExpiresAt    *time.Time `json:"expiresAt,omitempty"`
	MaxExpiresAt *time.Time `json:"maxExpiresAt,omitempty"`
	Notes        []string   `json:"notes"`
}

// ProtectedView is the next departure, whose target an extra plan must keep.
type ProtectedView struct {
	Title     string    `json:"title"`
	DepartAt  time.Time `json:"departAt"`
	TargetSoC int       `json:"targetSoc"`
}

// AdjustInput is a confirmed proposal.
type AdjustInput struct {
	Kind      string     `json:"kind"`
	Date      string     `json:"date"`
	SoC       int        `json:"soc"`
	ReadyBy   *time.Time `json:"readyBy"`
	ExpiresAt time.Time  `json:"expiresAt"`
	Confirm   bool       `json:"confirm"`
}

// maxExpiry bounds how long a temporary change may last after its ready-by.
const maxExpiry = 48 * time.Hour

// Proposal returns the default proposal for a day's advice. Opening it only
// reads EVCC.
func (s *Service) Proposal(ctx context.Context, kind, date string) (*ProposalView, error) {
	if kind != model.AdjustExtraPlan && kind != model.AdjustPostpone {
		return nil, invalid("kind must be %q or %q", model.AdjustExtraPlan, model.AdjustPostpone)
	}
	day, view, err := s.adviceFor(ctx, date)
	if err != nil {
		return nil, err
	}
	p := &ProposalView{Kind: kind, Date: date, Advice: day, Notes: []string{}, CapacityKWh: view.Settings.UsableCapacityKWh}
	snap := s.snapshot()
	var veh evcc.Vehicle
	var lp *evcc.Loadpoint
	if snap.state != nil {
		veh, _ = s.resolveVehicle(snap.state)
		lp, _ = s.loadpoint(snap.state)
		if veh.Name != "" {
			p.CurrentPlan = s.planView(veh)
		}
	}
	if lp != nil && lp.SmartCostLimit != nil {
		v := *lp.SmartCostLimit
		p.SmartCostLimit = &v
	}
	unavailable := func(format string, args ...any) (*ProposalView, error) {
		p.Reason = fmt.Sprintf(format, args...)
		return p, nil
	}
	switch {
	case day.Adjustment != nil:
		return unavailable("A change from this advice is already active.")
	case day.Dismissed:
		return unavailable("This advice was dismissed.")
	case day.Action != kind:
		return unavailable("%s", noActionReason(day, kind))
	case snap.state == nil || snap.err != nil:
		return unavailable("EVCC is not reachable.")
	case veh.Name == "":
		return unavailable("No EVCC vehicle found; choose it under Settings → Connections.")
	}

	now := s.now()
	start, _ := localtime.ParseDate(s.loc(), date)
	end := localtime.AddDays(start, 1)
	if kind == model.AdjustPostpone {
		if p.SmartCostLimit == nil {
			return unavailable("EVCC has no cheap-charging limit set, so there is no optional charging to postpone. Trip plans are not affected.")
		}
		until, _ := localtime.ParseDate(s.loc(), day.DeferUntil)
		p.ExpiresAt, p.MaxExpiresAt = &until, &view.To
		p.Notes = append(p.Notes,
			fmt.Sprintf("Pauses EVCC's cheap-grid charging (at or below €%.2f/kWh) until the cheaper day.", *p.SmartCostLimit),
			"Then it is restored, unless you change it in EVCC in the meantime.",
			"Plans for your trips stay in EVCC and still charge in time.")
		// When every price until then is published and above the limit, the
		// advice offers no postponement (see postponeNeedless); say so when
		// the prices published so far are.
		if cheapest, _ := cheapestPrice(snap.state.PriceSlots, now, until); !math.IsInf(cheapest, 1) && cheapest > *p.SmartCostLimit {
			p.Notes = append(p.Notes, fmt.Sprintf("No price published so far is at or below €%.2f/kWh (cheapest €%.2f): postponing matters only if a later price is.",
				*p.SmartCostLimit, cheapest))
		}
		p.Available = true
		return p, nil
	}

	next := view.Next
	if next != nil && !next.DepartAt.After(start) {
		return unavailable("EVCC keeps one plan per vehicle, and %s (leaving %s) needs its plan first. Create this extra plan after that departure.",
			next.FirstTitle, localtime.Clock(next.DepartAt, s.loc()))
	}
	readyBy, latest := end, view.To
	if next != nil {
		p.Protected = &ProtectedView{Title: next.FirstTitle, DepartAt: next.DepartAt, TargetSoC: next.TargetSoC}
		p.MinSoC = next.TargetSoC
		latest = next.DepartAt
		readyBy = minTime(readyBy, next.DepartAt)
	}
	readyBy = readyBy.Truncate(time.Minute)
	if !readyBy.After(now.Add(15 * time.Minute)) {
		return unavailable("Too late to charge on this day.")
	}
	p.ReadyBy, p.LatestReadyBy = &readyBy, &latest
	expires := readyBy
	maxExp := minTime(readyBy.Add(maxExpiry), view.To)
	p.ExpiresAt, p.MaxExpiresAt = &expires, &maxExp

	// Default target: charge on this day what the plan would otherwise add
	// for this day's and later departures.
	target := max(p.MinSoC, 80)
	if view.SoC.Known {
		base := 0.0
		if view.SoC.Projected != nil {
			base = *view.SoC.Projected
		}
		if next != nil && next.DepartAt.Before(end) && next.StartSoC != nil {
			base = *next.StartSoC
		}
		need := 0.0
		for _, b := range view.Blocks {
			if !b.DepartAt.Before(start) && b.DepartAt.After(now) {
				need += b.ChargePct
			}
		}
		p.StartSoC = ptr(round(base, 1))
		target = max(p.MinSoC, int(math.Ceil(base+need-1e-6)))
		// A battery-friendly default: at most EVCC's charge limit (the user
		// can raise it; plans charge past the limit).
		limit := 80
		if veh.LimitSoc > 0 {
			limit = veh.LimitSoc
		}
		if target > max(limit, p.MinSoC) {
			target = max(limit, p.MinSoC)
			p.Notes = append(p.Notes, fmt.Sprintf("Covering every later trip would need %d%%; the default stops at EVCC's charge limit (%d%%).",
				min(100, int(math.Ceil(base+need-1e-6))), limit))
		}
	} else {
		p.Notes = append(p.Notes, "The SoC is unknown until the car is plugged in; choose the target yourself.")
	}
	p.SoC = min(100, target)
	if p.CurrentPlan != nil {
		p.Notes = append(p.Notes, fmt.Sprintf("Replaces the current EVCC plan (%d%% by %s); it is restored when this plan expires, unless you change it in EVCC.",
			int(math.Round(p.CurrentPlan.SoC)), dateTimeText(p.CurrentPlan.Time, s.loc())))
	} else {
		p.Notes = append(p.Notes, "EVCC has no plan now; this one is removed when it expires.")
	}
	p.Notes = append(p.Notes, "The charge indicator is qualitative: choose the amount yourself.")
	p.Available = true
	return p, nil
}

func noActionReason(day *DayAdviceView, kind string) string {
	switch {
	case day.ActionNote != "":
		return day.ActionNote
	case day.NoSuggestion:
		return "There is not enough data for a suggestion on this day."
	case day.Confidence < 2 && day.Recommendation != "none":
		return "This advice has low confidence, so it offers no plan action."
	case kind == model.AdjustPostpone && day.Recommendation == "defer":
		return "A postponement starts right away, so it is only offered for today's advice."
	case kind == model.AdjustPostpone:
		return "This day's advice is not to postpone."
	}
	return "This day's advice is not to charge ahead."
}

// postponeNeedless says why pausing cheap-grid charging until a later day
// would change nothing: EVCC has no limit set, or every price until then is
// published and above it. Empty when it would, or when EVCC's state is
// unknown.
func (s *Service) postponeNeedless(snap evccSnapshot, now time.Time, deferUntil string) string {
	if snap.state == nil || snap.err != nil {
		return ""
	}
	lp, ok := s.loadpoint(snap.state)
	until, err := localtime.ParseDate(s.loc(), deferUntil)
	if !ok || err != nil || !until.After(now) {
		return ""
	}
	if lp.SmartCostLimit == nil {
		return "EVCC has no cheap-grid limit set, so there is no optional charging to postpone."
	}
	cheapest, covered := cheapestPrice(snap.state.PriceSlots, now, until)
	if !covered || cheapest <= *lp.SmartCostLimit {
		return ""
	}
	return fmt.Sprintf("No postponement needed: every price until then is above EVCC's cheap-grid limit (€%.2f/kWh; cheapest €%.2f), so EVCC will not charge from the grid anyway.",
		*lp.SmartCostLimit, cheapest)
}

// cheapestPrice returns the lowest published price in [from, to) and
// whether the published slots cover all of it.
func cheapestPrice(slots []evcc.Rate, from, to time.Time) (float64, bool) {
	cheapest, covered := math.Inf(1), time.Duration(0)
	for _, r := range slots {
		if start, end := maxTime(r.Start, from), minTime(r.End, to); end.After(start) {
			cheapest = math.Min(cheapest, r.Value)
			covered += end.Sub(start)
		}
	}
	return cheapest, covered >= to.Sub(from)
}

// ApplyAdjustment writes a confirmed proposal to EVCC and records it so the
// previous value can be restored.
func (s *Service) ApplyAdjustment(ctx context.Context, in AdjustInput) (*model.Adjustment, error) {
	if !in.Confirm {
		return nil, invalid("confirm the change before it is written to EVCC")
	}
	return s.applyAdjustment(ctx, in, "ui")
}

func (s *Service) applyAdjustment(ctx context.Context, in AdjustInput, source string) (*model.Adjustment, error) {
	s.adjustMu.Lock()
	defer s.adjustMu.Unlock()
	p, err := s.Proposal(ctx, in.Kind, in.Date)
	if err != nil {
		return nil, err
	}
	if !p.Available {
		return nil, invalid("%s", p.Reason)
	}
	active, err := s.Store.Adjustments(localtime.Date(s.now(), s.loc()))
	if err != nil {
		return nil, err
	}
	for _, a := range active {
		if a.Status == model.AdjustActive && a.Kind == in.Kind {
			return nil, invalid("another %s is active; cancel it first", kindText(a.Kind))
		}
	}
	now := s.now()
	if !in.ExpiresAt.After(now) || in.ExpiresAt.After(*p.MaxExpiresAt) {
		return nil, invalid("the expiry must be in the future and no later than %s", dateTimeText(*p.MaxExpiresAt, s.loc()))
	}
	snap := s.snapshot()
	veh, _ := s.resolveVehicle(snap.state)
	a := model.Adjustment{
		Kind: in.Kind, Source: source, AdviceDate: in.Date, Vehicle: veh.Name, Loadpoint: s.Conn().EVCCLoadpoint,
		ExpiresAt: in.ExpiresAt.Truncate(time.Minute), Status: model.AdjustActive, CreatedAt: now,
	}

	ev := model.AuditEvent{Action: "adjustment.create", SubjectType: "adjustment", Detail: adviceDetail(p.Advice)}
	var write *model.PlanWrite
	switch in.Kind {
	case model.AdjustExtraPlan:
		switch {
		case in.ReadyBy == nil:
			return nil, invalid("a ready-by time is required")
		case in.SoC < 1 || in.SoC > 100:
			return nil, invalid("target SoC must be 1 to 100")
		case in.SoC < p.MinSoC:
			return nil, invalid("the target must be at least %d%%, the target of %s", p.MinSoC, p.Protected.Title)
		case !in.ReadyBy.After(now):
			return nil, invalid("the ready-by time must be in the future")
		case in.ReadyBy.After(*p.LatestReadyBy):
			return nil, invalid("ready-by must be no later than %s so the next trip's target stays protected",
				dateTimeText(*p.LatestReadyBy, s.loc()))
		case in.ExpiresAt.Before(*in.ReadyBy):
			return nil, invalid("the expiry must not be before the ready-by time")
		}
		at := in.ReadyBy.Truncate(time.Minute)
		if veh.Plan != nil {
			a.PrevPlan = &model.PlanValue{SoC: veh.Plan.Soc, Time: veh.Plan.Time}
		}
		a.Plan = &model.PlanValue{SoC: float64(in.SoC), Time: at}
		a.Note = fmt.Sprintf("Extra plan %d%% by %s", in.SoC, dateTimeText(at, s.loc()))
		ev.Related = "plan"
		ev.Summary = fmt.Sprintf("Created an extra plan from the advice for %s: %d%% by %s, until %s",
			s.dateText(in.Date), in.SoC, dateTimeText(at, s.loc()), dateTimeText(a.ExpiresAt, s.loc()))
		ev.Before = audit.Marshal(map[string]string{"evccPlan": s.adjustPlanText(a.PrevPlan)})
		ev.After = audit.Marshal(map[string]string{"evccPlan": s.adjustPlanText(a.Plan)})
		if _, err := s.Clients().EVCC.SetSocPlan(ctx, veh.Name, in.SoC, at); err != nil {
			return nil, s.failedAdjustment(ctx, ev, err)
		}
		write = &model.PlanWrite{Vehicle: veh.Name, SoC: in.SoC, TargetTime: at, TripKey: "advice:" + in.Date, WrittenAt: now}
	case model.AdjustPostpone:
		a.PrevSmartCostLimit = p.SmartCostLimit
		a.Note = fmt.Sprintf("Cheap-grid charging paused until %s", dateTimeText(a.ExpiresAt, s.loc()))
		ev.Summary = fmt.Sprintf("Paused cheap-grid charging until %s (advice for %s)",
			dateTimeText(a.ExpiresAt, s.loc()), s.dateText(in.Date))
		ev.Before = audit.Marshal(map[string]string{"cheapChargingLimit": limitText(a.PrevSmartCostLimit)})
		ev.After = audit.Marshal(map[string]string{"cheapChargingLimit": limitText(nil)})
		if err := s.Clients().EVCC.SetSmartCostLimit(ctx, a.Loadpoint, nil); err != nil {
			return nil, s.failedAdjustment(ctx, ev, err)
		}
	}
	var saved model.Adjustment
	err = s.change(ctx, ev, func(tx *store.Store, e *model.AuditEvent) error {
		if write != nil {
			if _, err := tx.AddPlanWrite(*write); err != nil {
				return err
			}
		}
		var err error
		if saved, err = tx.AddAdjustment(a); err != nil {
			return err
		}
		e.SubjectID = strconv.FormatInt(saved.ID, 10)
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.Log.Info("evcc adjusted from advice", "kind", a.Kind, "date", a.AdviceDate, "source", source, "note", a.Note)
	if err := s.PollEVCC(ctx); err != nil {
		s.Log.Warn("evcc refresh after adjustment failed", "err", err)
	}
	return &saved, nil
}

// failedAdjustment records an EVCC write that failed; nothing was changed.
func (s *Service) failedAdjustment(ctx context.Context, ev model.AuditEvent, err error) error {
	ev.Outcome, ev.Error = model.OutcomeFailure, err.Error()
	s.record(ctx, ev)
	return err
}

func (s *Service) adjustPlanText(p *model.PlanValue) string {
	if p == nil {
		return s.planText(0, time.Time{}, false)
	}
	return s.planText(p.SoC, p.Time, true)
}

// CancelAdjustment ends an active adjustment now and restores the previous
// value, unless it was changed in EVCC meanwhile.
func (s *Service) CancelAdjustment(ctx context.Context, id int64) (*model.Adjustment, error) {
	s.adjustMu.Lock()
	defer s.adjustMu.Unlock()
	a, err := s.Store.Adjustment(id)
	if err != nil {
		return nil, err
	}
	if a.Status != model.AdjustActive {
		return nil, invalid("this change has already ended")
	}
	if err := s.PollEVCC(ctx); err != nil {
		return nil, fmt.Errorf("EVCC is not reachable: %w", err)
	}
	if err := s.settle(ctx, &a, true); err != nil {
		return nil, err
	}
	if err := s.PollEVCC(ctx); err != nil {
		s.Log.Warn("evcc refresh after restoring failed", "err", err)
	}
	return &a, nil
}

// CheckAdjustments restores expired adjustments and notices manual changes
// in EVCC. It runs after each EVCC poll and only acts on a fresh state.
func (s *Service) CheckAdjustments(ctx context.Context) error {
	s.adjustMu.Lock()
	defer s.adjustMu.Unlock()
	snap := s.snapshot()
	if snap.state == nil || snap.err != nil || s.now().Sub(snap.okAt) > 3*s.Conn().EVCCPollInterval() {
		return nil
	}
	list, err := s.Store.Adjustments(localtime.Date(s.now(), s.loc()))
	if err != nil {
		return err
	}
	var errs []error
	ended := false
	for i := range list {
		if list[i].Status == model.AdjustActive {
			if err := s.settle(ctx, &list[i], false); err != nil {
				errs = append(errs, err)
			}
			ended = ended || list[i].Status != model.AdjustActive
		}
	}
	if ended {
		// Show the restored values right away.
		if err := s.PollEVCC(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// settle compares an active adjustment with EVCC: a value changed in EVCC is
// kept; at expiry (or when cancelling) the previous value is restored. Each
// ending is recorded with the value it leaves in EVCC.
func (s *Service) settle(ctx context.Context, a *model.Adjustment, cancel bool) error {
	now := s.now()
	snap := s.snapshot()
	// end stores the outcome, a restored plan's record (or the automatic
	// plan state), and the activity entry together. before and after are
	// the EVCC values.
	end := func(status, note string, before, after map[string]string, write *model.PlanWrite, ctl *model.PlanControl) error {
		a.Status, a.Note, a.EndedAt = status, note, &now
		s.Log.Info("evcc adjustment ended", "id", a.ID, "status", status, "note", note)
		ev := model.AuditEvent{
			Action: "adjustment.end", SubjectType: "adjustment", SubjectID: strconv.FormatInt(a.ID, 10),
			Summary: kindTitle(a.Kind) + " ended: " + note,
			Before:  audit.Marshal(before), After: audit.Marshal(after),
			Detail: audit.Marshal(map[string]string{"status": status, "adviceDate": a.AdviceDate}),
		}
		if a.Kind == model.AdjustExtraPlan {
			ev.Related = "plan"
		}
		return s.change(ctx, ev, func(tx *store.Store, e *model.AuditEvent) error {
			if write != nil {
				if _, err := tx.AddPlanWrite(*write); err != nil {
					return err
				}
			}
			if ctl != nil {
				if err := tx.SavePlanControl(*ctl); err != nil {
					return err
				}
			}
			return tx.UpdateAdjustment(*a)
		})
	}
	done := model.AdjustRestored
	if cancel {
		done = model.AdjustCancelled
	}
	switch a.Kind {
	case model.AdjustExtraPlan:
		var cur *evcc.SocPlan
		if v, ok := snap.state.Vehicles[a.Vehicle]; ok {
			cur = v.Plan
		}
		var curPlan *model.PlanValue
		if cur != nil {
			curPlan = &model.PlanValue{SoC: cur.Soc, Time: cur.Time}
		}
		ours := samePlan(curPlan, a.Plan)
		// In automatic mode the trip plan check may have merged the extra
		// plan into its own.
		settings, err := s.Settings()
		if err != nil {
			return err
		}
		automatic := settings.TripPlans == model.TripPlansAutomatic
		ctl, err := s.Store.PlanControl()
		if err != nil {
			return err
		}
		if automatic && cur != nil && samePlan(curPlan, ctl.Managed) {
			ours = true
		}
		set := map[string]string{"evccPlan": s.adjustPlanText(a.Plan)}
		switch {
		case cur != nil && !ours:
			return end(model.AdjustKept, "The plan was changed in EVCC; your change is kept.", set,
				map[string]string{"evccPlan": s.planText(cur.Soc, cur.Time, true)}, nil, nil)
		case cur == nil && !s.planFinished(a, now):
			return end(model.AdjustKept, "The plan was removed in EVCC; left as is.", set,
				map[string]string{"evccPlan": s.adjustPlanText(nil)}, nil, nil)
		case !cancel && now.Before(a.ExpiresAt):
			return nil
		}
		// In automatic mode the check takes over the plan left in EVCC (it
		// updates or removes it), unless a chosen plan has paused it.
		handBack := func(left *model.PlanValue) *model.PlanControl {
			if !automatic || ctl.Paused() {
				return nil
			}
			ctl.Managed, ctl.Ahead = left, nil
			s.Kick()
			return &ctl
		}
		switch {
		case a.PrevPlan != nil && a.PrevPlan.Time.After(now):
			soc := int(math.Round(a.PrevPlan.SoC))
			if _, err := s.Clients().EVCC.SetSocPlan(ctx, a.Vehicle, soc, a.PrevPlan.Time); err != nil {
				return s.restoreFailed(ctx, a, err)
			}
			write := &model.PlanWrite{Vehicle: a.Vehicle, SoC: soc, TargetTime: a.PrevPlan.Time, TripKey: "restore", WrittenAt: now}
			return end(done, fmt.Sprintf("Restored the previous plan: %d%% by %s.", soc, dateTimeText(a.PrevPlan.Time, s.loc())),
				set, map[string]string{"evccPlan": s.adjustPlanText(a.PrevPlan)}, write, handBack(a.PrevPlan))
		case ours:
			if err := s.Clients().EVCC.DeleteSocPlan(ctx, a.Vehicle); err != nil {
				return s.restoreFailed(ctx, a, err)
			}
			return end(done, "Removed the extra plan; there was no earlier plan to restore.", set,
				map[string]string{"evccPlan": s.adjustPlanText(nil)}, nil, handBack(nil))
		default:
			return end(done, "The extra plan reached its target; there was no earlier plan to restore.", set,
				map[string]string{"evccPlan": s.adjustPlanText(nil)}, nil, handBack(nil))
		}
	case model.AdjustPostpone:
		var cur *float64
		if lp, ok := snap.state.Loadpoint(a.Loadpoint); ok {
			cur = lp.SmartCostLimit
		}
		set := map[string]string{"cheapChargingLimit": limitText(a.SmartCostLimit)}
		if !sameLimit(cur, a.SmartCostLimit) {
			return end(model.AdjustKept, "The cheap-charging limit was changed in EVCC; your change is kept.", set,
				map[string]string{"cheapChargingLimit": limitText(cur)}, nil, nil)
		}
		if !cancel && now.Before(a.ExpiresAt) {
			return nil
		}
		if err := s.Clients().EVCC.SetSmartCostLimit(ctx, a.Loadpoint, a.PrevSmartCostLimit); err != nil {
			return s.restoreFailed(ctx, a, err)
		}
		note := "Cheap-grid charging restored."
		if a.PrevSmartCostLimit != nil {
			note = fmt.Sprintf("Cheap-grid charging restored at €%.2f/kWh.", *a.PrevSmartCostLimit)
		}
		return end(done, note, set, map[string]string{"cheapChargingLimit": limitText(a.PrevSmartCostLimit)}, nil, nil)
	}
	return nil
}

func kindTitle(kind string) string {
	if kind == model.AdjustPostpone {
		return "Postponement"
	}
	return "Extra plan"
}

// planFinished tells EVCC's own clean-up from a removal by the user: EVCC
// deletes a plan once the vehicle reaches its target, or after its time.
func (s *Service) planFinished(a *model.Adjustment, now time.Time) bool {
	if a.Plan == nil || !now.Before(a.Plan.Time) {
		return true
	}
	soc, _ := s.currentSoC()
	return soc.Known && soc.Percent >= a.Plan.SoC-0.5
}

// restoreFailed keeps the adjustment active so the next check retries, and
// records the first failure (not every retry).
func (s *Service) restoreFailed(ctx context.Context, a *model.Adjustment, err error) error {
	note := "Restoring failed, retrying: " + err.Error()
	if a.Note != note {
		s.record(ctx, model.AuditEvent{
			Action: "adjustment.restore", SubjectType: "adjustment", SubjectID: strconv.FormatInt(a.ID, 10),
			Summary: "Restoring the previous EVCC setting after the " + kindText(a.Kind) + " failed; retrying",
			Outcome: model.OutcomeFailure, Error: err.Error(),
		})
	}
	a.Note = note
	s.Log.Warn("evcc adjustment restore failed", "id", a.ID, "err", err)
	return errors.Join(err, s.Store.UpdateAdjustment(*a))
}

// keepAfterPlanWrite marks active extra plans as replaced when the user
// sends a trip plan from the planner.
func (s *Service) keepAfterPlanWrite(ctx context.Context) {
	s.adjustMu.Lock()
	defer s.adjustMu.Unlock()
	list, err := s.Store.Adjustments(localtime.Date(s.now(), s.loc()))
	if err != nil {
		s.Log.Warn("list adjustments", "err", err)
		return
	}
	now := s.now()
	for _, a := range list {
		if a.Status != model.AdjustActive || a.Kind != model.AdjustExtraPlan {
			continue
		}
		a.Status, a.Note, a.EndedAt = model.AdjustKept, "Replaced by a trip plan sent from the planner.", &now
		ev := model.AuditEvent{
			Action: "adjustment.end", SubjectType: "adjustment", SubjectID: strconv.FormatInt(a.ID, 10), Related: "plan",
			Summary: "Extra plan ended: " + a.Note,
			Before:  audit.Marshal(map[string]string{"status": model.AdjustActive}),
			After:   audit.Marshal(map[string]string{"status": model.AdjustKept}),
		}
		if err := s.change(ctx, ev, func(tx *store.Store, e *model.AuditEvent) error {
			return tx.UpdateAdjustment(a)
		}); err != nil {
			s.Log.Warn("update adjustment", "err", err)
		}
	}
}

func sameLimit(a, b *float64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return math.Abs(*a-*b) < 1e-4
}

func kindText(kind string) string {
	if kind == model.AdjustPostpone {
		return "postponement"
	}
	return "extra plan"
}

func dateTimeText(t time.Time, loc *time.Location) string {
	return t.In(loc).Format("Mon 2 Jan, 15:04")
}

// AutoApply acts on today's advice when automatic plans are enabled and a
// concrete policy is configured. It skips weak, dismissed, or already
// handled advice, and an EVCC plan the user set manually.
func (s *Service) AutoApply(ctx context.Context) error {
	ctx = audit.WithSource(ctx, model.SourceAutomation)
	settings, err := s.AdviceSettings()
	if err != nil || !settings.Enabled || !settings.AutoApply {
		return err
	}
	today := localtime.Date(s.now(), s.loc())
	day, _, err := s.adviceFor(ctx, today)
	if err != nil {
		var ve ValidationError
		if errors.As(err, &ve) {
			return nil
		}
		return err
	}
	if day.Action == "" || day.Confidence < settings.AutoMinConfidence {
		return nil
	}
	list, err := s.Store.Adjustments(today)
	if err != nil {
		return err
	}
	for _, a := range list {
		if a.AdviceDate == today && a.Kind == day.Action {
			return nil // handled once per day
		}
	}
	snap := s.snapshot()
	if snap.state != nil {
		if veh, ok := s.resolveVehicle(snap.state); ok {
			if pv := s.planView(veh); pv != nil && !pv.SetByPlanner {
				s.Log.Info("auto-apply skipped: EVCC plan set manually", "date", today)
				return nil
			}
		}
		if lp, ok := s.loadpoint(snap.state); ok && lp.Mode == "off" {
			s.Log.Info("auto-apply skipped: charging is off in EVCC", "date", today)
			return nil
		}
	}

	in := AdjustInput{Kind: day.Action, Date: today}
	switch day.Action {
	case model.AdjustExtraPlan:
		target, ok := settings.Target(day.Indicator)
		if !ok {
			return nil
		}
		p, err := s.Proposal(ctx, in.Kind, today)
		if err != nil || !p.Available {
			return err
		}
		h, m, _ := localtime.ParseClock(settings.ReadyBy)
		readyBy := minTime(localtime.OnDay(localtime.StartOfDay(s.now(), s.loc()), h, m), *p.LatestReadyBy)
		if !readyBy.After(s.now().Add(15 * time.Minute)) {
			return nil
		}
		in.SoC, in.ReadyBy = max(target, p.MinSoC), &readyBy
		in.ExpiresAt = readyBy.Add(time.Duration(*settings.ExpiryHours) * time.Hour)
		if in.ExpiresAt.After(*p.MaxExpiresAt) {
			in.ExpiresAt = *p.MaxExpiresAt
		}
	case model.AdjustPostpone:
		if !settings.AutoPostpone {
			return nil
		}
		until, _ := localtime.ParseDate(s.loc(), day.DeferUntil)
		in.ExpiresAt = until
	}
	a, err := s.applyAdjustment(ctx, in, "auto")
	if err != nil {
		var ve ValidationError
		if errors.As(err, &ve) {
			s.Log.Info("auto-apply skipped", "date", today, "reason", ve.Msg)
			return nil
		}
		return err
	}
	s.Log.Info("auto-applied advice", "date", today, "kind", a.Kind, "note", a.Note)
	return nil
}
