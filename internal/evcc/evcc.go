// Package evcc reads EVCC state, previews charging plans, and writes SoC plans
// through EVCC's REST API. A stub with the same contract serves demo mode.
package evcc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Client is the EVCC contract used by the planner.
type Client interface {
	State(ctx context.Context) (*State, error)
	PreviewSocPlan(ctx context.Context, loadpoint int, soc int, at time.Time) (*Preview, error)
	SetSocPlan(ctx context.Context, vehicle string, soc int, at time.Time) (*SocPlan, error)
}

// State is the subset of /api/state the planner uses.
type State struct {
	Version     string
	SiteTitle   string
	Currency    string
	GridPrice   *float64 // current price incl. VAT, per kWh
	PVPowerW    float64
	HomePowerW  float64
	Loadpoints  []Loadpoint
	Vehicles    map[string]Vehicle
	PriceSlots  []Rate // published grid prices incl. VAT
	RetrievedAt time.Time
}

// Loadpoint is one charge point.
type Loadpoint struct {
	Title              string
	Mode               string
	Connected          bool
	Charging           bool
	ChargePowerW       float64
	VehicleName        string
	VehicleTitle       string
	VehicleSoc         float64
	EffectiveLimitSoc  int
	PlanActive         bool
	EffectivePlanSoc   float64
	EffectivePlanTime  *time.Time
	PlanProjectedStart *time.Time
	PlanProjectedEnd   *time.Time
}

// Vehicle is a configured vehicle.
type Vehicle struct {
	Name           string
	Title          string
	CapacityKWh    float64
	MinSoc         int
	LimitSoc       int
	Plan           *SocPlan
	RepeatingPlans int
}

// SocPlan is a one-off SoC goal with a deadline.
type SocPlan struct {
	Soc  float64   `json:"soc"`
	Time time.Time `json:"time"`
}

// Rate is a priced time slot.
type Rate struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	Value float64   `json:"value"`
}

// UnmarshalJSON accepts both the documented object form
// {"start","end","value"} and the compact [startUnix, endUnix, value] array
// returned by recent EVCC versions.
func (r *Rate) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) > 0 && b[0] == '[' {
		var arr []float64
		if err := json.Unmarshal(b, &arr); err != nil {
			return err
		}
		if len(arr) < 3 {
			return fmt.Errorf("rate array has %d elements, want 3", len(arr))
		}
		r.Start = time.Unix(int64(arr[0]), 0)
		r.End = time.Unix(int64(arr[1]), 0)
		r.Value = arr[2]
		return nil
	}
	var obj struct {
		Start time.Time `json:"start"`
		End   time.Time `json:"end"`
		Value float64   `json:"value"`
	}
	if err := json.Unmarshal(b, &obj); err != nil {
		return err
	}
	*r = Rate(obj)
	return nil
}

// Preview is a simulated plan from EVCC.
type Preview struct {
	PlanTime *time.Time `json:"planTime"`
	Duration int64      `json:"duration"` // seconds
	PowerW   float64    `json:"power"`
	Slots    []Rate     `json:"plan"`
}

// Loadpoint returns the loadpoint with a 1-based index.
func (s *State) Loadpoint(index int) (*Loadpoint, bool) {
	if index < 1 || index > len(s.Loadpoints) {
		return nil, false
	}
	return &s.Loadpoints[index-1], true
}

// ResolveVehicle picks the vehicle to plan for: the configured name, else the
// loadpoint's vehicle, else the only configured vehicle.
func (s *State) ResolveVehicle(configured string, loadpoint int) (Vehicle, bool) {
	if configured != "" {
		v, ok := s.Vehicles[configured]
		return v, ok
	}
	if lp, ok := s.Loadpoint(loadpoint); ok && lp.VehicleName != "" {
		if v, ok := s.Vehicles[lp.VehicleName]; ok {
			return v, true
		}
	}
	if len(s.Vehicles) == 1 {
		for _, v := range s.Vehicles {
			return v, true
		}
	}
	return Vehicle{}, false
}

// ---- HTTP client ----

// HTTPClient talks to a real EVCC instance.
type HTTPClient struct {
	BaseURL string
	HTTP    *http.Client
}

// NewHTTPClient returns a client for the EVCC base URL (e.g. http://host:7070).
func NewHTTPClient(baseURL string, timeout time.Duration) *HTTPClient {
	return &HTTPClient{BaseURL: strings.TrimRight(baseURL, "/"), HTTP: &http.Client{Timeout: timeout}}
}

type rawState struct {
	Version    string                 `json:"version"`
	SiteTitle  string                 `json:"siteTitle"`
	Currency   string                 `json:"currency"`
	TariffGrid *float64               `json:"tariffGrid"`
	PVPower    float64                `json:"pvPower"`
	HomePower  float64                `json:"homePower"`
	Loadpoints []rawLoadpoint         `json:"loadpoints"`
	Vehicles   map[string]rawVeh      `json:"vehicles"`
	Forecast   *struct{ Grid []Rate } `json:"forecast"`
}

type rawLoadpoint struct {
	Title              string     `json:"title"`
	Mode               string     `json:"mode"`
	Connected          bool       `json:"connected"`
	Charging           bool       `json:"charging"`
	ChargePower        float64    `json:"chargePower"`
	VehicleName        string     `json:"vehicleName"`
	VehicleTitle       string     `json:"vehicleTitle"`
	VehicleSoc         float64    `json:"vehicleSoc"`
	EffectiveLimitSoc  int        `json:"effectiveLimitSoc"`
	PlanActive         bool       `json:"planActive"`
	EffectivePlanSoc   float64    `json:"effectivePlanSoc"`
	EffectivePlanTime  *time.Time `json:"effectivePlanTime"`
	PlanProjectedStart *time.Time `json:"planProjectedStart"`
	PlanProjectedEnd   *time.Time `json:"planProjectedEnd"`
}

type rawVeh struct {
	Title          string            `json:"title"`
	Capacity       float64           `json:"capacity"`
	MinSoc         int               `json:"minSoc"`
	LimitSoc       int               `json:"limitSoc"`
	Plan           *SocPlan          `json:"plan"`
	RepeatingPlans []json.RawMessage `json:"repeatingPlans"`
}

// ParseState decodes an /api/state body, with or without the legacy
// {"result": ...} wrapper.
func ParseState(body []byte) (*State, error) {
	var wrapper struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(body, &wrapper); err == nil && len(wrapper.Result) > 0 && wrapper.Result[0] == '{' {
		body = wrapper.Result
	}
	var raw rawState
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decode evcc state: %w", err)
	}
	st := &State{
		Version: raw.Version, SiteTitle: raw.SiteTitle, Currency: raw.Currency, GridPrice: raw.TariffGrid,
		PVPowerW: raw.PVPower, HomePowerW: raw.HomePower, Vehicles: map[string]Vehicle{},
	}
	for _, lp := range raw.Loadpoints {
		st.Loadpoints = append(st.Loadpoints, Loadpoint{
			Title: lp.Title, Mode: lp.Mode, Connected: lp.Connected, Charging: lp.Charging,
			ChargePowerW: lp.ChargePower, VehicleName: lp.VehicleName, VehicleTitle: lp.VehicleTitle,
			VehicleSoc: lp.VehicleSoc, EffectiveLimitSoc: lp.EffectiveLimitSoc, PlanActive: lp.PlanActive,
			EffectivePlanSoc: lp.EffectivePlanSoc, EffectivePlanTime: nonZero(lp.EffectivePlanTime),
			PlanProjectedStart: nonZero(lp.PlanProjectedStart), PlanProjectedEnd: nonZero(lp.PlanProjectedEnd),
		})
	}
	for name, v := range raw.Vehicles {
		plan := v.Plan
		if plan != nil && (plan.Soc <= 0 || plan.Time.IsZero()) {
			plan = nil
		}
		st.Vehicles[name] = Vehicle{
			Name: name, Title: v.Title, CapacityKWh: v.Capacity, MinSoc: v.MinSoc, LimitSoc: v.LimitSoc,
			Plan: plan, RepeatingPlans: len(v.RepeatingPlans),
		}
	}
	if raw.Forecast != nil {
		st.PriceSlots = raw.Forecast.Grid
		sort.Slice(st.PriceSlots, func(i, j int) bool { return st.PriceSlots[i].Start.Before(st.PriceSlots[j].Start) })
	}
	return st, nil
}

func nonZero(t *time.Time) *time.Time {
	if t == nil || t.IsZero() || t.Unix() <= 0 {
		return nil
	}
	return t
}

// State fetches /api/state.
func (c *HTTPClient) State(ctx context.Context) (*State, error) {
	body, err := c.do(ctx, http.MethodGet, "/api/state")
	if err != nil {
		return nil, err
	}
	st, err := ParseState(body)
	if err != nil {
		return nil, err
	}
	st.RetrievedAt = time.Now()
	return st, nil
}

// PreviewSocPlan simulates a SoC plan without changing EVCC.
func (c *HTTPClient) PreviewSocPlan(ctx context.Context, loadpoint int, soc int, at time.Time) (*Preview, error) {
	path := fmt.Sprintf("/api/loadpoints/%d/plan/static/preview/soc/%d/%s", loadpoint, soc, at.UTC().Format(time.RFC3339))
	body, err := c.do(ctx, http.MethodGet, path)
	if err != nil {
		return nil, err
	}
	var p Preview
	if err := json.Unmarshal(unwrap(body), &p); err != nil {
		return nil, fmt.Errorf("decode preview: %w", err)
	}
	return &p, nil
}

// SetSocPlan creates or replaces the vehicle's one-off SoC plan.
func (c *HTTPClient) SetSocPlan(ctx context.Context, vehicle string, soc int, at time.Time) (*SocPlan, error) {
	path := fmt.Sprintf("/api/vehicles/%s/plan/soc/%d/%s", url.PathEscape(vehicle), soc, at.UTC().Format(time.RFC3339))
	body, err := c.do(ctx, http.MethodPost, path)
	if err != nil {
		return nil, err
	}
	var p SocPlan
	if err := json.Unmarshal(unwrap(body), &p); err != nil {
		return nil, fmt.Errorf("decode plan response: %w", err)
	}
	return &p, nil
}

func unwrap(body []byte) []byte {
	var wrapper struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(body, &wrapper); err == nil && len(wrapper.Result) > 0 {
		return wrapper.Result
	}
	return body
}

// APIError is a non-2xx response from EVCC.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("evcc returned %d: %s", e.Status, e.Message)
}

func (c *HTTPClient) do(ctx context.Context, method, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("evcc unreachable: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		msg := strings.TrimSpace(string(body))
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &e) == nil && e.Error != "" {
			msg = e.Error
		}
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return nil, &APIError{Status: resp.StatusCode, Message: msg}
	}
	return body, nil
}
