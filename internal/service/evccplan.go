package service

import (
	"context"
	"errors"
	"math"
	"strconv"
	"time"

	"github.com/diogoap/evcc-planner/internal/config"
	"github.com/diogoap/evcc-planner/internal/evcc"
	"github.com/diogoap/evcc-planner/internal/model"
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
	Plan         *EVCCPlanView `json:"plan"`
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
	v := StatusView{Mode: s.Cfg.EVCC.Mode, PriceSlots: []evcc.Rate{}}
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
	if lp, ok := st.Loadpoint(s.Cfg.EVCC.Loadpoint); ok {
		v.Charger, v.Connected, v.Charging = lp.Title, lp.Connected, lp.Charging
		v.ChargePowerW, v.ChargeMode, v.LimitSoC = lp.ChargePowerW, lp.Mode, lp.EffectiveLimitSoc
	}
	if veh, ok := st.ResolveVehicle(s.Cfg.EVCC.Vehicle, s.Cfg.EVCC.Loadpoint); ok {
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
	return v
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
	if veh, ok := snap.state.ResolveVehicle(s.Cfg.EVCC.Vehicle, s.Cfg.EVCC.Loadpoint); ok {
		v.Vehicle = veh.Name
		v.CurrentPlan = s.planView(veh)
	}
	now := s.now()
	for _, r := range snap.state.PriceSlots {
		if r.End.After(now) && r.Start.Before(at) {
			v.PriceSlots = append(v.PriceSlots, r)
		}
	}
	lp, ok := snap.state.Loadpoint(s.Cfg.EVCC.Loadpoint)
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
	p, err := s.EVCC.PreviewSocPlan(ctx, s.Cfg.EVCC.Loadpoint, soc, at)
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
	Confirm bool      `json:"confirm"`
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
	now := s.now()
	if !in.Time.After(now) {
		return nil, invalid("the ready-by time must be in the future")
	}
	snap := s.snapshot()
	if snap.state == nil {
		return nil, errors.New("EVCC is not reachable")
	}
	veh, ok := snap.state.ResolveVehicle(s.Cfg.EVCC.Vehicle, s.Cfg.EVCC.Loadpoint)
	if !ok {
		return nil, invalid("no EVCC vehicle found; set evcc.vehicle in the config")
	}
	at := in.Time.Truncate(time.Minute)
	if _, err := s.EVCC.SetSocPlan(ctx, veh.Name, in.SoC, at); err != nil {
		return nil, err
	}
	if _, err := s.Store.AddPlanWrite(model.PlanWrite{
		Vehicle: veh.Name, SoC: in.SoC, TargetTime: at, TripKey: in.TripKey, WrittenAt: now,
	}); err != nil {
		return nil, err
	}
	s.Log.Info("plan sent to evcc", "vehicle", veh.Name, "soc", in.SoC, "time", at)
	if err := s.PollEVCC(ctx); err != nil {
		s.Log.Warn("evcc refresh after plan write failed", "err", err)
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
	case s.Routes.Router.Name() == "stub":
		rt.Status, rt.Detail = "stub", "Stub distances"
		if s.RoutesNote != "" {
			rt.Detail = s.RoutesNote
		}
	default:
		rt.Status, rt.Detail = "ok", "API key configured; traffic-aware, cached routes"
	}
	out = append(out, rt)

	out = append(out,
		ConnectionView{Name: "OpenRouter", Status: "planned", Detail: "Daily forecast extraction (V2)"},
		ConnectionView{Name: "KNMI", Status: "planned", Detail: "Weather forecasts (V2)"},
		ConnectionView{Name: "Home Assistant", Status: "planned", Detail: "Notifications (V3), always-on SoC (V6)"},
	)
	return out, nil
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}
