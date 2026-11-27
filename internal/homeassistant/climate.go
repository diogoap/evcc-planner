package homeassistant

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strconv"
	"sync"
	"time"
)

// ClimateController reads entities (the car's climate and the outside
// temperature sensor) and turns the car's climate on, for car heating.
type ClimateController interface {
	Locator
	// SetClimate sets the target temperature and turns the climate on in
	// hvacMode: climate.set_temperature, then climate.set_hvac_mode. The
	// temperature is in Home Assistant's unit system.
	SetClimate(ctx context.Context, entityID string, temp float64, hvacMode string) error
}

// Climate is a climate entity's state.
type Climate struct {
	EntityID string
	// State is the HVAC mode now: "off", "heat_cool", "heat", ...;
	// Action what it does in it (hvac_action, when reported): "heating",
	// "cooling", "idle", "off", ...
	State   string
	Action  string
	Target  *float64
	Current *float64
	// Min, Max, and Step bound the target; zero when not reported.
	Min, Max, Step float64
	Modes          []string
}

// ParseClimate reads a climate entity's state and attributes.
func ParseClimate(e EntityState) (Climate, error) {
	c := Climate{EntityID: e.EntityID, State: e.State}
	if e.unknown() {
		return c, fmt.Errorf("%s is %s", e.EntityID, orDefault(e.State, "empty"))
	}
	c.Target, c.Current = attrNumber(e, "temperature"), attrNumber(e, "current_temperature")
	c.Action, _ = e.Attributes["hvac_action"].(string)
	for name, v := range map[string]*float64{"min_temp": &c.Min, "max_temp": &c.Max, "target_temp_step": &c.Step} {
		if n := attrNumber(e, name); n != nil {
			*v = *n
		}
	}
	if modes, ok := e.Attributes["hvac_modes"].([]any); ok {
		for _, m := range modes {
			if s, ok := m.(string); ok {
				c.Modes = append(c.Modes, s)
			}
		}
	}
	return c, nil
}

// attrNumber reads a numeric attribute, or nil.
func attrNumber(e EntityState, name string) *float64 {
	switch v := e.Attributes[name].(type) {
	case float64:
		return &v
	case int:
		f := float64(v)
		return &f
	case string:
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return &f
		}
	}
	return nil
}

// On reports whether the climate runs in any mode.
func (c Climate) On() bool { return c.State != "off" }

// HeatMode is the mode that heats: heat, else heat_cool, else auto, if the
// entity offers one.
func (c Climate) HeatMode() (string, bool) {
	for _, m := range []string{"heat", "heat_cool", "auto"} {
		if slices.Contains(c.Modes, m) {
			return m, true
		}
	}
	return "", false
}

// Clamp rounds a target temperature to the entity's step (0.5 when it
// reports none) and keeps it within its range.
func (c Climate) Clamp(t float64) float64 {
	step := c.Step
	if step <= 0 {
		step = 0.5
	}
	t = math.Round(t/step) * step
	if c.Max > c.Min {
		t = math.Min(math.Max(t, c.Min), c.Max)
	}
	return t
}

// ParseTemperature reads a temperature sensor in °C (°F is converted).
// Unknown, unavailable, non-numeric, and implausible values are errors.
func ParseTemperature(e EntityState) (float64, error) {
	if e.unknown() {
		return 0, fmt.Errorf("%s is %s", e.EntityID, orDefault(e.State, "empty"))
	}
	v, err := strconv.ParseFloat(e.State, 64)
	if err != nil {
		return 0, fmt.Errorf("%s is %q, not a number", e.EntityID, e.State)
	}
	switch u := e.Unit(); u {
	case "", "°C", "C":
	case "°F", "F":
		v = (v - 32) * 5 / 9
	default:
		return 0, fmt.Errorf("%s is in %s, not °C or °F", e.EntityID, u)
	}
	if v < -50 || v > 60 {
		return 0, fmt.Errorf("%s reads %g °C, treated as unknown", e.EntityID, v)
	}
	return v, nil
}

// SetClimate sets the climate's target temperature, then turns it on in
// hvacMode.
func (c *HTTPClient) SetClimate(ctx context.Context, entityID string, temp float64, hvacMode string) error {
	if err := c.post(ctx, "/api/services/climate/set_temperature",
		map[string]any{"entity_id": entityID, "temperature": temp}); err != nil {
		return fmt.Errorf("set the temperature of %s: %w", entityID, err)
	}
	if err := c.post(ctx, "/api/services/climate/set_hvac_mode",
		map[string]any{"entity_id": entityID, "hvac_mode": hvacMode}); err != nil {
		return fmt.Errorf("turn on %s: %w", entityID, err)
	}
	return nil
}

// ---- stub ----

// Entities the climate stub reports.
const (
	// StubClimateEntity is the demo car's climate, shaped like a real
	// car's: 16–26 °C in steps of 1, modes heat_cool and off.
	StubClimateEntity = "climate.demo_car"
	// StubOutsideEntity is the demo outside temperature sensor.
	StubOutsideEntity = "sensor.demo_outside_temperature"
)

// stubOutsideLag is how old the demo outside temperature reading is.
const stubOutsideLag = 5 * time.Minute

// ClimateCall is a SetClimate call the stub received.
type ClimateCall struct {
	Entity string
	Temp   float64
	Mode   string
}

// ClimateStub is the demo car's climate and an outside temperature sensor
// (demo mode and tests). It records SetClimate calls and keeps the state
// they set.
type ClimateStub struct {
	Now func() time.Time

	mu        sync.Mutex
	state     string
	target    float64
	outside   *float64
	outsideAt time.Time // zero: stubOutsideLag before Now
	calls     []ClimateCall
	fail      error
}

// NewClimateStub returns the stub in its reset state.
func NewClimateStub(now func() time.Time) *ClimateStub {
	s := &ClimateStub{Now: now}
	s.Reset()
	return s
}

// Reset turns the climate off at 18 °C and makes it 2 °C outside (a cold
// November morning), and forgets the recorded calls.
func (s *ClimateStub) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	two := 2.0
	s.state, s.target, s.outside, s.outsideAt, s.calls = "off", 18, &two, time.Time{}, nil
}

var stubModes = []any{"heat_cool", "off"}

// State reports the climate and the outside temperature; other entities
// are unknown.
func (s *ClimateStub) State(_ context.Context, entityID string) (*EntityState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail != nil {
		return nil, s.fail
	}
	now := s.Now()
	switch entityID {
	case StubClimateEntity:
		action := "heating"
		switch s.state {
		case "off":
			action = "off"
		case "cool":
			action = "cooling"
		}
		return &EntityState{EntityID: entityID, State: s.state, LastChanged: now, LastUpdated: now, LastReported: now,
			Attributes: map[string]any{
				"hvac_modes": stubModes, "min_temp": 16.0, "max_temp": 26.0, "target_temp_step": 1.0,
				"temperature": s.target, "current_temperature": 10.0, "hvac_action": action, "friendly_name": "Demo car climate",
			}}, nil
	case StubOutsideEntity:
		at := s.outsideAt
		if at.IsZero() {
			at = now.Add(-stubOutsideLag).Truncate(time.Minute)
		}
		state := "unavailable"
		if s.outside != nil {
			state = strconv.FormatFloat(*s.outside, 'f', -1, 64)
		}
		return &EntityState{EntityID: entityID, State: state, LastChanged: at, LastUpdated: at, LastReported: at,
			Attributes: map[string]any{
				"unit_of_measurement": "°C", "device_class": "temperature", "friendly_name": "Demo outside temperature",
			}}, nil
	}
	return nil, fmt.Errorf("%s: %w", entityID, ErrNotFound)
}

// SetClimate records the call and turns the demo climate on.
func (s *ClimateStub) SetClimate(_ context.Context, entityID string, temp float64, hvacMode string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.fail != nil:
		return s.fail
	case entityID != StubClimateEntity:
		return fmt.Errorf("%s: %w", entityID, ErrNotFound)
	case !slices.Contains(stubModes, any(hvacMode)) || hvacMode == "off":
		return &APIError{Status: 400, Message: "invalid hvac_mode " + hvacMode}
	}
	s.calls = append(s.calls, ClimateCall{Entity: entityID, Temp: temp, Mode: hvacMode})
	s.state, s.target = hvacMode, temp
	return nil
}

// SetOutside sets the outside temperature (nil: unavailable) and when it
// was reported (zero: a few minutes before now).
func (s *ClimateStub) SetOutside(c *float64, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.outside, s.outsideAt = c, at
}

// SetState sets the climate's mode, such as "heat_cool" when it was turned
// on elsewhere, or "off".
func (s *ClimateStub) SetState(mode string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = mode
}

// SetFail makes every call fail with err, or work again with nil.
func (s *ClimateStub) SetFail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fail = err
}

// Take returns the recorded calls and forgets them.
func (s *ClimateStub) Take() []ClimateCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.calls
	s.calls = nil
	return out
}

// Sensors lists the climate and the outside temperature sensor.
func (s *ClimateStub) Sensors() []Sensor {
	s.mu.Lock()
	defer s.mu.Unlock()
	outside := "unavailable"
	if s.outside != nil {
		outside = strconv.FormatFloat(*s.outside, 'f', -1, 64)
	}
	return []Sensor{
		{EntityID: StubClimateEntity, Name: "Demo car climate", State: s.state, Kind: KindClimate},
		{EntityID: StubOutsideEntity, Name: "Demo outside temperature", State: outside, Unit: "°C", Kind: KindTemperature},
	}
}
