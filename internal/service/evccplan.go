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
	"github.com/diogoap/evcc-planner/internal/config"
	"github.com/diogoap/evcc-planner/internal/evcc"
	"github.com/diogoap/evcc-planner/internal/localtime"
	"github.com/diogoap/evcc-planner/internal/model"
	"github.com/diogoap/evcc-planner/internal/store"
)

// StatusView is the EVCC status card.
type StatusView struct {
	Mode         string        `json:"mode"`
	Reachable    bool          `json:"reachable"`
	Error        string        `json:"error,omitempty"`
	UpdatedAt    *time.Time    `json:"updatedAt,omitempty"`
	Version      string        `json:"version,omitempty"`
	Charger      string        `json:"charger,omitempty"`
	Vehicle      string        `json:"vehicle,omitempty"`
	VehicleTitle string        `json:"vehicleTitle,omitempty"`
	Connected    bool          `json:"connected"`
	Charging     bool          `json:"charging"`
	ChargePowerW float64       `json:"chargePowerW"`
	ChargeMode   string        `json:"chargeMode,omitempty"`
	LimitSoC     int           `json:"limitSoc,omitempty"`
	GridPrice    *float64      `json:"gridPrice"`
	PVPowerW     float64       `json:"pvPowerW"`
	Currency     string        `json:"currency,omitempty"`
	PriceSlots   []evcc.Rate   `json:"priceSlots"`
	Solar        []SolarView   `json:"solar"`
	Plan         *EVCCPlanView `json:"plan"`
	// Location is where Home Assistant's car location sensor puts the car;
	// nil without a sensor or a known state.
	Location *LocationView `json:"location"`
}

// LocationView is the car's location: "home", "not_home", or the name of a
// Home Assistant zone.
type LocationView struct {
	State string    `json:"state"`
	Home  bool      `json:"home"`
	Since time.Time `json:"since"`
}

// SolarView is a point of EVCC's PV forecast for the chart: power at an
// instant, linear in between, with EVCC's correction applied.
type SolarView struct {
	Time time.Time `json:"time"`
	KW   float64   `json:"kw"`
}

// EVCCPlanView is the vehicle's current one-off plan in EVCC.
type EVCCPlanView struct {
	SoC          float64   `json:"soc"`
	Time         time.Time `json:"time"`
	SetByPlanner bool      `json:"setByPlanner"`
}

// Status returns the cached EVCC state for the UI.
func (s *Service) Status() StatusView {
	snap := s.snapshot()
	v := StatusView{Mode: s.Cfg.EVCC.Mode, PriceSlots: []evcc.Rate{}, Solar: []SolarView{}}
	s.mu.RLock()
	v.Location = s.carLoc
	s.mu.RUnlock()
	if snap.err != nil {
		v.Error = snap.err.Error()
	}
	if snap.state == nil {
		return v
	}
	st := snap.state
	v.Reachable = snap.err == nil
	v.UpdatedAt = &snap.okAt
	v.Version, v.GridPrice, v.PVPowerW, v.Currency = st.Version, st.GridPrice, st.PVPowerW, st.Currency
	if lp, ok := s.loadpoint(st); ok {
		v.Charger, v.Connected, v.Charging = lp.Title, lp.Connected, lp.Charging
		v.ChargePowerW, v.ChargeMode, v.LimitSoC = lp.ChargePowerW, lp.Mode, lp.EffectiveLimitSoc
	}
	if veh, ok := s.resolveVehicle(st); ok {
		v.Vehicle, v.VehicleTitle = veh.Name, veh.Title
		v.Plan = s.planView(veh)
	}
	// Show slots from the start of the current hour onward.
	cutoff := s.now().Truncate(time.Hour)
	for _, r := range st.PriceSlots {
		if r.End.After(cutoff) {
			v.PriceSlots = append(v.PriceSlots, r)
		}
	}
	v.Solar = solarView(st.Solar, cutoff)
	return v
}

// solarView lists the forecast from its last point at or before from, so the
// line starts at the chart's left edge.
func solarView(f *evcc.SolarForecast, from time.Time) []SolarView {
	out := []SolarView{}
	if f == nil {
		return out
	}
	scale := f.Scale
	if scale <= 0 {
		scale = 1
	}
	for i, p := range f.Points {
		if i+1 < len(f.Points) && !f.Points[i+1].Time.After(from) {
			continue
		}
		out = append(out, SolarView{Time: p.Time, KW: math.Round(p.PowerW*scale) / 1000})
	}
	return out
}

func (s *Service) planView(veh evcc.Vehicle) *EVCCPlanView {
	if veh.Plan == nil {
		return nil
	}
	p := &EVCCPlanView{SoC: veh.Plan.Soc, Time: veh.Plan.Time}
	if w, err := s.Store.LastPlanWrite(); err == nil && w != nil {
		p.SetByPlanner = w.Vehicle == veh.Name && int(math.Round(veh.Plan.Soc)) == w.SoC &&
			absDuration(veh.Plan.Time.Sub(w.TargetTime)) < time.Minute
	}
	return p
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// PreviewView is shown in the Send plan dialog.
type PreviewView struct {
	Available       bool          `json:"available"`
	Reason          string        `json:"reason,omitempty"`
	SoC             int           `json:"soc"`
	Time            time.Time     `json:"time"`
	DurationMinutes int           `json:"durationMinutes"`
	PowerW          float64       `json:"powerW"`
	Slots           []evcc.Rate   `json:"slots"`
	PriceSlots      []evcc.Rate   `json:"priceSlots"`
	CurrentPlan     *EVCCPlanView `json:"currentPlan"`
	Vehicle         string        `json:"vehicle"`
}

// Preview asks EVCC to simulate a SoC plan. EVCC assumes 0% while the car
// is unplugged, so the preview is only offered while it is connected.
func (s *Service) Preview(ctx context.Context, soc int, at time.Time) (*PreviewView, error) {
	if soc < 1 || soc > 100 {
		return nil, invalid("target SoC must be 1 to 100")
	}
	v := &PreviewView{SoC: soc, Time: at, Slots: []evcc.Rate{}, PriceSlots: []evcc.Rate{}}
	snap := s.snapshot()
	if snap.state == nil {
		v.Reason = "EVCC is not reachable."
		return v, nil
	}
	if veh, ok := s.resolveVehicle(snap.state); ok {
		v.Vehicle = veh.Name
		v.CurrentPlan = s.planView(veh)
	}
	now := s.now()
	for _, r := range snap.state.PriceSlots {
		if r.End.After(now) && r.Start.Before(at) {
			v.PriceSlots = append(v.PriceSlots, r)
		}
	}
	lp, ok := s.loadpoint(snap.state)
	switch {
	case !ok:
		v.Reason = "The configured EVCC loadpoint was not found."
		return v, nil
	case !lp.Connected || lp.VehicleSoc <= 0:
		v.Reason = "EVCC can preview charging once the car is plugged in; until then it does not know the SoC."
		return v, nil
	case !at.After(now):
		v.Reason = "The departure time has passed."
		return v, nil
	}
	p, err := s.Clients().EVCC.PreviewSocPlan(ctx, s.Conn().EVCCLoadpoint, soc, at)
	if err != nil {
		v.Reason = "EVCC preview failed: " + err.Error()
		return v, nil
	}
	v.Available = true
	v.DurationMinutes = int(math.Ceil(float64(p.Duration) / 60))
	v.PowerW = p.PowerW
	if p.Slots != nil {
		v.Slots = p.Slots
	}
	return v, nil
}

// SendPlanInput is a confirmed request to write a SoC plan.
type SendPlanInput struct {
	SoC     int       `json:"soc"`
	Time    time.Time `json:"time"`
	TripKey string    `json:"tripKey"`
	// Through is the last local date whose trips the target covers, when
	// it was chosen to cover more than the next departure.
	Through string `json:"through"`
	Confirm bool   `json:"confirm"`
	// Reason prefixes the activity entry of an automatic send.
	Reason string `json:"-"`
	// auto marks a send by the trip plan automation; control, if set, is
	// saved with the plan record.
	auto    bool
	control *model.PlanControl
}

// SendPlan writes the vehicle's one-off SoC plan to EVCC. EVCC keeps one
// such plan per vehicle; limits and repeating plans are left unchanged.
func (s *Service) SendPlan(ctx context.Context, in SendPlanInput) (*EVCCPlanView, error) {
	if !in.Confirm {
		return nil, invalid("confirm the plan before sending it to EVCC")
	}
	if in.SoC < 1 || in.SoC > 100 {
		return nil, invalid("target SoC must be 1 to 100")
	}
	if in.Through != "" {
		if _, err := localtime.ParseDate(s.loc(), in.Through); err != nil {
			return nil, invalid("through must be a date (YYYY-MM-DD)")
		}
	}
	now := s.now()
	if !in.Time.After(now) {
		return nil, invalid("the ready-by time must be in the future")
	}
	// The vehicle and the client come from the same settings.
	cs := s.state()
	snap := s.snapshot()
	if snap.state == nil {
		return nil, errors.New("EVCC is not reachable")
	}
	veh, ok := snap.state.ResolveVehicle(cs.settings.EVCCVehicle, cs.settings.EVCCLoadpoint)
	if !ok {
		return nil, invalid("no EVCC vehicle found; choose it under Settings → Connections")
	}
	at := in.Time.Truncate(time.Minute)
	ev := model.AuditEvent{
		Action: "plan.send", SubjectType: "plan", SubjectID: veh.Name,
		Summary: fmt.Sprintf("Sent plan %d%% by %s to EVCC", in.SoC, dateTimeText(at, s.loc())),
	}
	if in.Reason != "" {
		ev.Summary = in.Reason + ": s" + strings.TrimPrefix(ev.Summary, "S")
	}
	if in.TripKey != "" {
		ev.Related = "trip:" + in.TripKey
	}
	if in.Through != "" {
		ev.Summary += ", covering trips through " + s.dateText(in.Through)
	}
	prev := s.planText(0, time.Time{}, false)
	if veh.Plan != nil {
		prev = s.planText(veh.Plan.Soc, veh.Plan.Time, true)
	}
	ev.Before = audit.Marshal(map[string]string{"evccPlan": prev})
	ev.After = audit.Marshal(map[string]string{"evccPlan": s.planText(float64(in.SoC), at, true)})
	resp, err := cs.EVCC.SetSocPlan(ctx, veh.Name, in.SoC, at)
	if err != nil {
		ev.Outcome, ev.Error = model.OutcomeFailure, err.Error()
		s.record(ctx, ev)
		return nil, err
	}
	detail := map[string]any{"tripKey": in.TripKey, "evccResponse": resp}
	if in.Through != "" {
		detail["coversThrough"] = in.Through
	}
	ev.Detail = audit.Marshal(detail)
	if err := s.change(ctx, ev, func(tx *store.Store, e *model.AuditEvent) error {
		if _, err := tx.AddPlanWrite(model.PlanWrite{
			Vehicle: veh.Name, SoC: in.SoC, TargetTime: at, TripKey: in.TripKey, WrittenAt: now,
		}); err != nil {
			return err
		}
		if in.control != nil {
			return tx.SavePlanControl(*in.control)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	// An automatic write merges an active extra plan; a plan sent by hand
	// replaces it.
	if !in.auto {
		s.keepAfterPlanWrite(ctx)
	}
	s.Log.Info("plan sent to evcc", "vehicle", veh.Name, "soc", in.SoC, "time", at)
	if err := s.PollEVCC(ctx); err != nil {
		s.Log.Warn("evcc refresh after plan write failed", "err", err)
	}
	// A plan sent by hand is a chosen plan: in automatic mode it pauses
	// automatic changes right away (or is replaced when too low).
	if !in.auto {
		if err := s.SyncPlan(ctx); err != nil {
			s.Log.Warn("automatic plan check after a manual send failed", "err", err)
		}
	}
	return &EVCCPlanView{SoC: float64(in.SoC), Time: at, SetByPlanner: true}, nil
}

// ConnectionView is one integration's status.
type ConnectionView struct {
	Name          string     `json:"name"`
	Mode          string     `json:"mode"`
	Status        string     `json:"status"` // ok, error, stub, not_configured, planned
	Detail        string     `json:"detail"`
	LastSuccessAt *time.Time `json:"lastSuccessAt,omitempty"`
}

// Connections reports integration status without exposing secrets.
func (s *Service) Connections() ([]ConnectionView, error) {
	var out []ConnectionView
	snap := s.snapshot()
	ev := ConnectionView{Name: "EVCC", Mode: s.Cfg.EVCC.Mode}
	switch {
	case s.Cfg.EVCC.Mode == config.ModeStub:
		ev.Status, ev.Detail = "stub", "Demo feed"
	case snap.err != nil:
		ev.Status, ev.Detail = "error", snap.err.Error()
	case snap.state == nil:
		ev.Status, ev.Detail = "error", "Not checked yet"
	default:
		ev.Status, ev.Detail = "ok", "EVCC "+snap.state.Version
	}
	if !snap.okAt.IsZero() {
		ev.LastSuccessAt = &snap.okAt
	}
	out = append(out, ev)

	cal := ConnectionView{Name: "Google Calendar", Mode: s.Cfg.Calendar.Mode}
	statuses, err := s.Store.SyncStatuses()
	if err != nil {
		return nil, err
	}
	cals := s.Calendar.Calendars
	switch {
	case len(cals) == 0:
		cal.Status, cal.Detail = "not_configured", "Set "+s.Cfg.Calendar.URLEnv+" to one or more secret iCal addresses"
	default:
		cal.Status = "ok"
		if s.Cfg.Calendar.Mode == config.ModeStub {
			cal.Status = "stub"
		}
		var detail string
		for _, c := range cals {
			st := statuses[c.ID]
			name := st.Name
			if name == "" {
				name = c.Label
			}
			part := name + ": "
			switch {
			case st.LastError != "":
				cal.Status = "error"
				part += st.LastError
			case st.LastSuccessAt != nil:
				part += plural(st.EventCount, "event")
				if cal.LastSuccessAt == nil || st.LastSuccessAt.Before(*cal.LastSuccessAt) {
					cal.LastSuccessAt = st.LastSuccessAt
				}
			default:
				part += "not synced yet"
			}
			if detail != "" {
				detail += " · "
			}
			detail += part
		}
		cal.Detail = detail
	}
	out = append(out, cal)

	rt := ConnectionView{Name: "Google Routes", Mode: s.Cfg.Routes.Mode}
	switch {
	case s.Clients().Routes.Router.Name() == "stub":
		rt.Status, rt.Detail = "stub", "Stub distances"
		if s.Clients().RoutesNote != "" {
			rt.Detail = s.Clients().RoutesNote
		}
	default:
		rt.Status, rt.Detail = "ok", "API key configured; traffic-aware, cached routes"
	}
	out = append(out, rt)

	fc, err := s.forecastConnections()
	if err != nil {
		return nil, err
	}
	out = append(out, fc...)
	out = append(out, s.homeAssistantConnection(), s.notificationConnection())
	return out, nil
}

// homeAssistantConnection reports the always-on SoC source.
func (s *Service) homeAssistantConnection() ConnectionView {
	s.mu.RLock()
	r, err, okAt := s.haReading, s.haErr, s.haOKAt
	s.mu.RUnlock()
	c := ConnectionView{Name: "Home Assistant", Mode: s.Cfg.HomeAssistant.Mode}
	if !okAt.IsZero() {
		c.LastSuccessAt = &okAt
	}
	switch {
	case s.Clients().HA == nil:
		c.Status, c.Detail = "not_configured", s.Clients().HANote
	case err != nil:
		c.Status, c.Detail = "error", err.Error()
	case r == nil:
		c.Status, c.Detail = "error", "Not checked yet"
	default:
		c.Status = "ok"
		if s.Cfg.HomeAssistant.Mode == config.ModeStub {
			c.Status = "stub"
		}
		c.Detail = fmt.Sprintf("SoC %.0f%% from %s, reported %s", r.Percent, r.Entity,
			r.ObservedAt.In(s.loc()).Format("Mon 2 Jan 15:04"))
	}
	return c
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}
