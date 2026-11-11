// Package homeassistant reads the car's state of charge from Home
// Assistant's REST API, which has it even while the car is unplugged (EVCC
// only reads it while plugged in). It never writes. A stub with the same
// contract serves demo mode.
package homeassistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Reader is the Home Assistant contract used by the planner.
type Reader interface {
	SoC(ctx context.Context) (*Reading, error)
}

// SensorLister lists sensors that could hold the car's SoC or range.
type SensorLister interface {
	Sensors(ctx context.Context) ([]Sensor, error)
}

// Sensor is a candidate for the SoC or range sensor.
type Sensor struct {
	EntityID string `json:"entityId"`
	Name     string `json:"name"`
	State    string `json:"state"`
	Unit     string `json:"unit"`
	Kind     string `json:"kind"` // battery or range
}

// Sensor kinds.
const (
	KindBattery = "battery"
	KindRange   = "range"
)

// Candidates picks battery level sensors (%) and distance sensors (km, mi),
// sorted by entity ID.
func Candidates(states []EntityState) []Sensor {
	var out []Sensor
	for _, e := range states {
		if !strings.HasPrefix(e.EntityID, "sensor.") {
			continue
		}
		class, _ := e.Attributes["device_class"].(string)
		name, _ := e.Attributes["friendly_name"].(string)
		sn := Sensor{EntityID: e.EntityID, Name: name, State: e.State, Unit: e.Unit()}
		switch {
		case class == "battery" && sn.Unit == "%":
			sn.Kind = KindBattery
		case class == "distance" || sn.Unit == "km" || sn.Unit == "mi":
			sn.Kind = KindRange
		default:
			continue
		}
		out = append(out, sn)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].EntityID < out[j].EntityID })
	return out
}

// Reading is the car's SoC as Home Assistant reports it.
type Reading struct {
	Entity     string
	Percent    float64
	RangeKm    *float64 // from the optional range entity
	ObservedAt time.Time
	// ChangedAt is when the value last changed, never after ObservedAt: a
	// value reported again recently can still be old news.
	ChangedAt time.Time
}

// EntityState is one entity's state from /api/states.
type EntityState struct {
	EntityID   string         `json:"entity_id"`
	State      string         `json:"state"`
	Attributes map[string]any `json:"attributes"`
	// LastReported is when the integration last wrote the state, even
	// unchanged (Home Assistant 2024.3 and later); LastUpdated when the state
	// or an attribute last changed; LastChanged when the state last changed.
	LastChanged  time.Time `json:"last_changed"`
	LastUpdated  time.Time `json:"last_updated"`
	LastReported time.Time `json:"last_reported"`
}

// Reported is when Home Assistant last received the state.
func (e EntityState) Reported() time.Time {
	for _, t := range []time.Time{e.LastReported, e.LastUpdated, e.LastChanged} {
		if !t.IsZero() {
			return t
		}
	}
	return time.Time{}
}

// Unit is the entity's unit of measurement, or "".
func (e EntityState) Unit() string {
	u, _ := e.Attributes["unit_of_measurement"].(string)
	return u
}

// unknown reports a state Home Assistant uses for missing values.
func (e EntityState) unknown() bool {
	return e.State == "" || e.State == "unknown" || e.State == "unavailable"
}

// ParseSoC turns the SoC entity into a reading. The reading's time is when
// the car reported it, from the observed attribute when one is configured
// (never later than Home Assistant received it), else when Home Assistant
// last received the state; its change time is when the value last changed
// (the car's time with the attribute). Zero counts as unknown, as in EVCC.
func ParseSoC(e EntityState, observedAttribute string, loc *time.Location) (Reading, error) {
	r := Reading{Entity: e.EntityID}
	if e.unknown() {
		return r, fmt.Errorf("%s is %s", e.EntityID, orDefault(e.State, "empty"))
	}
	v, err := strconv.ParseFloat(e.State, 64)
	if err != nil {
		return r, fmt.Errorf("%s is %q, not a number", e.EntityID, e.State)
	}
	if u := e.Unit(); u != "" && u != "%" {
		return r, fmt.Errorf("%s is in %s, not %%", e.EntityID, u)
	}
	if v <= 0 || v > 100 {
		return r, fmt.Errorf("%s reports %g%%, treated as unknown", e.EntityID, v)
	}
	r.Percent = v
	r.ObservedAt = e.Reported()
	if observedAttribute != "" {
		raw, ok := e.Attributes[observedAttribute]
		if !ok {
			return r, fmt.Errorf("%s has no attribute %s", e.EntityID, observedAttribute)
		}
		t, err := parseTime(raw, loc)
		if err != nil {
			return r, fmt.Errorf("%s attribute %s: %w", e.EntityID, observedAttribute, err)
		}
		if r.ObservedAt.IsZero() || t.Before(r.ObservedAt) {
			r.ObservedAt = t
		}
		r.ChangedAt = r.ObservedAt
	}
	if r.ObservedAt.IsZero() {
		return r, fmt.Errorf("%s has no update time", e.EntityID)
	}
	if r.ChangedAt.IsZero() {
		r.ChangedAt = e.LastChanged
	}
	if r.ChangedAt.IsZero() || r.ChangedAt.After(r.ObservedAt) {
		r.ChangedAt = r.ObservedAt
	}
	return r, nil
}

// ParseRange returns the range entity's value in km, or nil when it is
// unknown. Miles are converted.
func ParseRange(e EntityState) (*float64, error) {
	if e.unknown() {
		return nil, nil
	}
	v, err := strconv.ParseFloat(e.State, 64)
	if err != nil {
		return nil, fmt.Errorf("%s is %q, not a number", e.EntityID, e.State)
	}
	switch e.Unit() {
	case "", "km":
	case "mi":
		v *= 1.609344
	default:
		return nil, fmt.Errorf("%s is in %s, not km or mi", e.EntityID, e.Unit())
	}
	if v <= 0 {
		return nil, nil
	}
	v = math.Round(v)
	return &v, nil
}

// parseTime reads a time attribute: RFC 3339 (how Home Assistant
// serializes datetimes), or a local time without zone.
func parseTime(raw any, loc *time.Location) (time.Time, error) {
	s, ok := raw.(string)
	if !ok {
		return time.Time{}, fmt.Errorf("%v is not a time", raw)
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, nil
	}
	for _, layout := range []string{"2006-01-02T15:04:05.999999999", "2006-01-02 15:04:05.999999999", "2006-01-02 15:04"} {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("%q is not a time", s)
}

func orDefault(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// ---- HTTP client ----

// HTTPClient reads a real Home Assistant with a long-lived access token.
type HTTPClient struct {
	BaseURL string
	Token   string
	HTTP    *http.Client

	SoCEntity         string
	RangeEntity       string // optional
	ObservedAttribute string // optional: the SoC entity's attribute with the car's own reading time
	Location          *time.Location
}

// APIError is a non-2xx response from Home Assistant.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("home assistant returned %d: %s", e.Status, e.Message)
}

// ErrNotFound means the entity does not exist.
var ErrNotFound = errors.New("entity not found")

// SoC reads the SoC entity and, if configured, the range entity. A failing
// range entity only drops the range.
func (c *HTTPClient) SoC(ctx context.Context) (*Reading, error) {
	e, err := c.State(ctx, c.SoCEntity)
	if err != nil {
		return nil, err
	}
	r, err := ParseSoC(*e, c.ObservedAttribute, c.Location)
	if err != nil {
		return nil, err
	}
	if c.RangeEntity != "" {
		if re, err := c.State(ctx, c.RangeEntity); err == nil {
			r.RangeKm, _ = ParseRange(*re)
		}
	}
	return &r, nil
}

// State reads one entity.
func (c *HTTPClient) State(ctx context.Context, entityID string) (*EntityState, error) {
	body, err := c.get(ctx, "/api/states/"+url.PathEscape(entityID))
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
		return nil, fmt.Errorf("%s: %w", entityID, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	var e EntityState
	if err := json.Unmarshal(body, &e); err != nil {
		return nil, fmt.Errorf("decode %s: %w", entityID, err)
	}
	return &e, nil
}

// Sensors lists the battery and distance sensors to choose from.
func (c *HTTPClient) Sensors(ctx context.Context) ([]Sensor, error) {
	list, err := c.States(ctx)
	if err != nil {
		return nil, err
	}
	return Candidates(list), nil
}

// States lists all entities.
func (c *HTTPClient) States(ctx context.Context) ([]EntityState, error) {
	body, err := c.get(ctx, "/api/states")
	if err != nil {
		return nil, err
	}
	var list []EntityState
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("decode states: %w", err)
	}
	return list, nil
}

func (c *HTTPClient) get(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.BaseURL, "/")+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("home assistant unreachable: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		msg := strings.TrimSpace(string(body))
		var e struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(body, &e) == nil && e.Message != "" {
			msg = e.Message
		}
		if resp.StatusCode == http.StatusUnauthorized {
			msg = "access token rejected"
		}
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return nil, &APIError{Status: resp.StatusCode, Message: msg}
	}
	return body, nil
}

// ---- stub ----

// Entities the stub reports.
const (
	StubEntity      = "sensor.demo_leaf_battery"
	StubRangeEntity = "sensor.demo_leaf_range"
)

// stubLag is how far the stub's readings trail the car, like a cloud
// integration that polls the car now and then.
const stubLag = 20 * time.Minute

// Stub reports a demo car's SoC as Home Assistant would: also while the car
// is unplugged, a little behind.
type Stub struct {
	Now func() time.Time
	// Car returns the simulated battery: SoC % and range in km.
	Car func() (soc, rangeKm float64)
}

// SoC reports the demo car's battery.
func (s Stub) SoC(ctx context.Context) (*Reading, error) {
	soc, rangeKm := s.Car()
	at := s.Now().Add(-stubLag).Truncate(time.Minute)
	return &Reading{Entity: StubEntity, Percent: soc, RangeKm: &rangeKm, ObservedAt: at, ChangedAt: at}, nil
}

// Sensors lists the demo car's sensors.
func (s Stub) Sensors(ctx context.Context) ([]Sensor, error) {
	soc, rangeKm := s.Car()
	return []Sensor{
		{EntityID: StubEntity, Name: "Demo Leaf battery", State: strconv.FormatFloat(soc, 'f', -1, 64), Unit: "%", Kind: KindBattery},
		{EntityID: StubRangeEntity, Name: "Demo Leaf range", State: strconv.FormatFloat(rangeKm, 'f', -1, 64), Unit: "km", Kind: KindRange},
	}, nil
}
