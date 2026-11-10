package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/diogoap/evcc-planner/internal/evcc"
	"github.com/diogoap/evcc-planner/internal/localtime"
	"github.com/diogoap/evcc-planner/internal/model"
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
		veh, _ = snap.state.ResolveVehicle(s.Cfg.EVCC.Vehicle, s.Cfg.EVCC.Loadpoint)
		lp, _ = snap.state.Loadpoint(s.Cfg.EVCC.Loadpoint)
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
		return unavailable("No EVCC vehicle found; set evcc.vehicle in the config.")
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
		// Say so when no published price before then reaches the limit.
		cheapest, known := math.Inf(1), false
		for _, r := range snap.state.PriceSlots {
			if r.End.After(now) && r.Start.Before(until) {
				cheapest, known = math.Min(cheapest, r.Value), true
			}
		}
		if known && cheapest > *p.SmartCostLimit {
			p.Notes = append(p.Notes, fmt.Sprintf("No published price before then is at or below €%.2f/kWh (cheapest €%.2f), so EVCC would not charge from the grid anyway: postponing changes nothing in practice.",
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
	veh, _ := snap.state.ResolveVehicle(s.Cfg.EVCC.Vehicle, s.Cfg.EVCC.Loadpoint)
	a := model.Adjustment{
		Kind: in.Kind, Source: source, AdviceDate: in.Date, Vehicle: veh.Name, Loadpoint: s.Cfg.EVCC.Loadpoint,
		ExpiresAt: in.ExpiresAt.Truncate(time.Minute), Status: model.AdjustActive, CreatedAt: now,
	}

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
		if _, err := s.EVCC.SetSocPlan(ctx, veh.Name, in.SoC, at); err != nil {
			return nil, err
		}
		if err := s.recordPlanWrite(veh.Name, in.SoC, at, "advice:"+in.Date); err != nil {
			return nil, err
		}
		a.Plan = &model.PlanValue{SoC: float64(in.SoC), Time: at}
		a.Note = fmt.Sprintf("Extra plan %d%% by %s", in.SoC, dateTimeText(at, s.loc()))
	case model.AdjustPostpone:
		a.PrevSmartCostLimit = p.SmartCostLimit
		if err := s.EVCC.SetSmartCostLimit(ctx, a.Loadpoint, nil); err != nil {
			return nil, err
		}
		a.Note = fmt.Sprintf("Cheap-grid charging paused until %s", dateTimeText(a.ExpiresAt, s.loc()))
	}
	saved, err := s.Store.AddAdjustment(a)
	if err != nil {
		return nil, err
	}
	s.Log.Info("evcc adjusted from advice", "kind", a.Kind, "date", a.AdviceDate, "source", source, "note", a.Note)
	if err := s.PollEVCC(ctx); err != nil {
		s.Log.Warn("evcc refresh after adjustment failed", "err", err)
	}
	return &saved, nil
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
	if snap.state == nil || snap.err != nil || s.now().Sub(snap.okAt) > 3*s.Cfg.EVCC.PollInterval {
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
// kept; at expiry (or when cancelling) the previous value is restored.
func (s *Service) settle(ctx context.Context, a *model.Adjustment, cancel bool) error {
	now := s.now()
	snap := s.snapshot()
	end := func(status, note string) error {
		a.Status, a.Note, a.EndedAt = status, note, &now
		s.Log.Info("evcc adjustment ended", "id", a.ID, "status", status, "note", note)
		return s.Store.UpdateAdjustment(*a)
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
		ours := cur != nil && a.Plan != nil && int(math.Round(cur.Soc)) == int(math.Round(a.Plan.SoC)) &&
			absDuration(cur.Time.Sub(a.Plan.Time)) < time.Minute
		switch {
		case cur != nil && !ours:
			return end(model.AdjustKept, "The plan was changed in EVCC; your change is kept.")
		case cur == nil && !s.planFinished(a, now):
			return end(model.AdjustKept, "The plan was removed in EVCC; left as is.")
		case !cancel && now.Before(a.ExpiresAt):
			return nil
		}
		switch {
		case a.PrevPlan != nil && a.PrevPlan.Time.After(now):
			soc := int(math.Round(a.PrevPlan.SoC))
			if _, err := s.EVCC.SetSocPlan(ctx, a.Vehicle, soc, a.PrevPlan.Time); err != nil {
				return s.restoreFailed(a, err)
			}
			if err := s.recordPlanWrite(a.Vehicle, soc, a.PrevPlan.Time, "restore"); err != nil {
				return err
			}
			return end(done, fmt.Sprintf("Restored the previous plan: %d%% by %s.", soc, dateTimeText(a.PrevPlan.Time, s.loc())))
		case ours:
			if err := s.EVCC.DeleteSocPlan(ctx, a.Vehicle); err != nil {
				return s.restoreFailed(a, err)
			}
			return end(done, "Removed the extra plan; there was no earlier plan to restore.")
		default:
			return end(done, "The extra plan reached its target; there was no earlier plan to restore.")
		}
	case model.AdjustPostpone:
		var cur *float64
		if lp, ok := snap.state.Loadpoint(a.Loadpoint); ok {
			cur = lp.SmartCostLimit
		}
		if !sameLimit(cur, a.SmartCostLimit) {
			return end(model.AdjustKept, "The cheap-charging limit was changed in EVCC; your change is kept.")
		}
		if !cancel && now.Before(a.ExpiresAt) {
			return nil
		}
		if err := s.EVCC.SetSmartCostLimit(ctx, a.Loadpoint, a.PrevSmartCostLimit); err != nil {
			return s.restoreFailed(a, err)
		}
		note := "Cheap-grid charging restored."
		if a.PrevSmartCostLimit != nil {
			note = fmt.Sprintf("Cheap-grid charging restored at €%.2f/kWh.", *a.PrevSmartCostLimit)
		}
		return end(done, note)
	}
	return nil
}

// planFinished tells EVCC's own clean-up from a removal by the user: EVCC
// deletes a plan once the vehicle reaches its target, or after its time.
func (s *Service) planFinished(a *model.Adjustment, now time.Time) bool {
	if a.Plan == nil || !now.Before(a.Plan.Time) {
		return true
	}
	last, err := s.Store.LastSoC()
	return err == nil && last != nil && last.Percent >= a.Plan.SoC-0.5
}

// recordPlanWrite remembers a plan the app wrote, so the status shows it as
// set by the planner.
func (s *Service) recordPlanWrite(vehicle string, soc int, at time.Time, key string) error {
	_, err := s.Store.AddPlanWrite(model.PlanWrite{Vehicle: vehicle, SoC: soc, TargetTime: at, TripKey: key, WrittenAt: s.now()})
	return err
}

func (s *Service) restoreFailed(a *model.Adjustment, err error) error {
	a.Note = "Restoring failed, retrying: " + err.Error()
	s.Log.Warn("evcc adjustment restore failed", "id", a.ID, "err", err)
	return errors.Join(err, s.Store.UpdateAdjustment(*a))
}

// keepAfterPlanWrite marks active extra plans as replaced when the user
// sends a trip plan from the planner.
func (s *Service) keepAfterPlanWrite() {
	s.adjustMu.Lock()
	defer s.adjustMu.Unlock()
	list, err := s.Store.Adjustments(localtime.Date(s.now(), s.loc()))
	if err != nil {
		s.Log.Warn("list adjustments", "err", err)
		return
	}
	now := s.now()
	for _, a := range list {
		if a.Status == model.AdjustActive && a.Kind == model.AdjustExtraPlan {
			a.Status, a.Note, a.EndedAt = model.AdjustKept, "Replaced by a trip plan sent from the planner.", &now
			if err := s.Store.UpdateAdjustment(a); err != nil {
				s.Log.Warn("update adjustment", "err", err)
			}
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
		if veh, ok := snap.state.ResolveVehicle(s.Cfg.EVCC.Vehicle, s.Cfg.EVCC.Loadpoint); ok {
			if pv := s.planView(veh); pv != nil && !pv.SetByPlanner {
				s.Log.Info("auto-apply skipped: EVCC plan set manually", "date", today)
				return nil
			}
		}
		if lp, ok := snap.state.Loadpoint(s.Cfg.EVCC.Loadpoint); ok && lp.Mode == "off" {
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
