package service

import (
	"context"
	"fmt"
	"math"

	"github.com/diogoap/evcc-planner/internal/audit"
	"github.com/diogoap/evcc-planner/internal/model"
)

// connectedMeta remembers whether the car was plugged in at the last EVCC
// check, so a new connection is noticed exactly once, also across restarts.
const connectedMeta = "evcc.connected"

// PlanOnPlugIn sends the next departure's plan to EVCC once when the car
// goes from unplugged to plugged in (setting autoPlanOnPlugIn). It keeps a
// plan already in EVCC with an equal or higher SoC, whatever its ready-by
// time (someone chose it), replaces a lower one, and sends nothing when the
// car's charge already covers the departure. The first check after a
// fresh start only learns the state. It runs after each EVCC poll and
// records its decision in the activity log.
func (s *Service) PlanOnPlugIn(ctx context.Context) error {
	ctx = audit.WithSource(ctx, model.SourceAutomation)
	s.autoPlanMu.Lock()
	defer s.autoPlanMu.Unlock()
	snap := s.snapshot()
	if snap.state == nil || snap.err != nil || s.now().Sub(snap.okAt) > 3*s.Cfg.EVCC.PollInterval {
		return nil
	}
	lp, ok := snap.state.Loadpoint(s.Cfg.EVCC.Loadpoint)
	if !ok {
		return nil
	}
	cur := "0"
	if lp.Connected {
		cur = "1"
	}
	prev, err := s.Store.Meta(connectedMeta)
	if err != nil || prev == cur {
		return err
	}
	if err := s.Store.SetMeta(connectedMeta, cur); err != nil {
		return err
	}
	if prev != "0" || cur != "1" {
		return nil // unplugged, or the first check
	}
	settings, err := s.Settings()
	if err != nil || !settings.AutoPlanOnPlugIn {
		return err
	}
	view, err := s.Plan(ctx)
	if err != nil {
		return err
	}
	veh, _ := snap.state.ResolveVehicle(s.Cfg.EVCC.Vehicle, s.Cfg.EVCC.Loadpoint)
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
			SoC: next.TargetSoC, Time: next.DepartAt, TripKey: next.TripKeys[0], Confirm: true, Reason: "Car plugged in",
		})
		return err
	}
	s.record(ctx, ev)
	return nil
}
