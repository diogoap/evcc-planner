package service

import (
	"context"
	"errors"
	"slices"

	"github.com/diogoap/evcc-planner/internal/model"
)

// ConnectionAction does what a connection offers now (Settings →
// Connections): check it, sync the calendars, fetch the forecasts, send a
// test notification, make an AI plan, or send the plan to EVCC again. A
// check, sync, or refresh is recorded with its outcome (the others record
// themselves). A failure shows in the connections it returns; only an
// action the connection doesn't offer now, or one that can't start, is an
// error.
func (s *Service) ConnectionAction(ctx context.Context, name, action string) ([]ConnectionView, error) {
	list, err := s.Connections()
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(list, func(c ConnectionView) bool { return c.Name == name })
	if i < 0 || !slices.Contains(list[i].Actions, action) {
		return nil, invalid("%s offers no %q now", name, action)
	}
	var aerr error
	switch action {
	case ActionCheck:
		switch name {
		case "EVCC":
			aerr = s.PollEVCC(ctx)
		case "Home Assistant":
			aerr = s.PollHomeAssistant(ctx)
		case "Google Routes":
			aerr = s.checkRoutes(ctx)
		}
	case ActionSync:
		aerr = s.Calendar.SyncAll(ctx)
		// As after the hourly sync: the automatic plan check follows the trips.
		s.Kick()
	case ActionRefresh:
		aerr = s.RefreshForecasts(ctx, true)
	case ActionTest:
		_, aerr = s.SendTestNotification(ctx)
	case ActionRun:
		_, aerr = s.RunAI(ctx, model.AITriggerManual)
	case ActionSend:
		if aerr = s.PollEVCC(ctx); aerr == nil {
			aerr = s.SyncPlan(ctx)
		}
	}
	var verr ValidationError
	if errors.As(aerr, &verr) {
		return nil, aerr
	}
	if what, ok := checkText[action]; ok {
		if action == ActionCheck {
			what += " " + name
		}
		ev := model.AuditEvent{Action: "connection.check", SubjectType: "connection", SubjectID: name,
			Summary: what + ": works"}
		if aerr != nil {
			ev.Summary = what + ": " + aerr.Error()
			ev.Outcome, ev.Error = model.OutcomeFailure, aerr.Error()
		}
		s.record(ctx, ev)
	}
	return s.Connections()
}

// checkText starts the activity log entry of a check, sync, or refresh.
var checkText = map[string]string{
	ActionCheck: "Checked", ActionSync: "Synced the calendars", ActionRefresh: "Fetched the forecasts",
}

// checkRoutes looks up the route from home to a saved place (or the next
// trip's) with the provider, without the cache.
func (s *Service) checkRoutes(ctx context.Context) error {
	settings, err := s.Settings()
	if err != nil {
		return err
	}
	if settings.HomeAddress == "" {
		return invalid("set your home address first: lookups start there")
	}
	places, err := s.Store.Places()
	if err != nil {
		return err
	}
	for _, p := range places {
		if p.Address != "" && p.Address != settings.HomeAddress {
			return s.Clients().Routes.Check(ctx, settings.HomeAddress, p.Address)
		}
	}
	view, err := s.Plan(ctx)
	if err != nil {
		return err
	}
	for _, t := range view.Trips {
		if t.Address != "" {
			return s.Clients().Routes.Check(ctx, settings.HomeAddress, t.Address)
		}
	}
	return invalid("add a place or a trip first: a check looks up the route to one")
}
