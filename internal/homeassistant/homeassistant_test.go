package homeassistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var amsterdam, _ = time.LoadLocation("Europe/Amsterdam")

var reported = time.Date(2026, 11, 11, 8, 10, 0, 0, time.UTC)

func entity(state string, attrs map[string]any) EntityState {
	if attrs == nil {
		attrs = map[string]any{"unit_of_measurement": "%", "device_class": "battery"}
	}
	return EntityState{
		EntityID: "sensor.leaf_battery", State: state, Attributes: attrs,
		LastChanged: reported.Add(-3 * time.Hour), LastUpdated: reported.Add(-time.Hour), LastReported: reported,
	}
}

func TestParseSoC(t *testing.T) {
	r, err := ParseSoC(entity("72", nil), "", amsterdam)
	if err != nil || r.Percent != 72 || !r.ObservedAt.Equal(reported) || r.Entity != "sensor.leaf_battery" ||
		!r.ChangedAt.Equal(reported.Add(-3*time.Hour)) {
		t.Fatalf("reading = %+v %v (want the last reported time, and when the value changed)", r, err)
	}

	// Older Home Assistant versions have no last_reported.
	e := entity("72.5", nil)
	e.LastReported = time.Time{}
	if r, _ := ParseSoC(e, "", amsterdam); r.Percent != 72.5 || !r.ObservedAt.Equal(reported.Add(-time.Hour)) {
		t.Fatalf("without last_reported = %+v, want last_updated", r)
	}

	for state, want := range map[string]string{
		"unavailable": "sensor.leaf_battery is unavailable",
		"unknown":     "sensor.leaf_battery is unknown",
		"":            "sensor.leaf_battery is empty",
		"full":        `"full", not a number`,
		"0":           "reports 0%, treated as unknown",
		"101":         "reports 101%",
	} {
		if _, err := ParseSoC(entity(state, nil), "", amsterdam); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("state %q: err = %v, want %q", state, err, want)
		}
	}
	if _, err := ParseSoC(entity("40", map[string]any{"unit_of_measurement": "kWh"}), "", amsterdam); err == nil ||
		!strings.Contains(err.Error(), "in kWh, not %") {
		t.Errorf("energy sensor: err = %v", err)
	}
	if r, err := ParseSoC(entity("40", map[string]any{}), "", amsterdam); err != nil || r.Percent != 40 {
		t.Errorf("no unit = %+v %v", r, err)
	}
}

func TestParseSoCObservedAttribute(t *testing.T) {
	attrs := func(v any) map[string]any { return map[string]any{"unit_of_measurement": "%", "updated_on": v} }
	carTime := reported.Add(-40 * time.Minute)
	for _, raw := range []any{
		carTime.Format(time.RFC3339Nano),
		carTime.In(amsterdam).Format("2006-01-02T15:04:05"), // no zone: planner time
		carTime.In(amsterdam).Format("2006-01-02 15:04:05"),
	} {
		r, err := ParseSoC(entity("61", attrs(raw)), "updated_on", amsterdam)
		if err != nil || !r.ObservedAt.Equal(carTime) || !r.ChangedAt.Equal(carTime) {
			t.Errorf("%v: observed = %v %v, want %v", raw, r.ObservedAt, err, carTime)
		}
	}
	// Never later than Home Assistant received it.
	if r, _ := ParseSoC(entity("61", attrs(reported.Add(time.Hour).Format(time.RFC3339))), "updated_on", amsterdam); !r.ObservedAt.Equal(reported) {
		t.Errorf("future car time = %v, want %v", r.ObservedAt, reported)
	}
	if _, err := ParseSoC(entity("61", nil), "updated_on", amsterdam); err == nil || !strings.Contains(err.Error(), "no attribute updated_on") {
		t.Errorf("missing attribute: err = %v", err)
	}
	if _, err := ParseSoC(entity("61", attrs(12)), "updated_on", amsterdam); err == nil || !strings.Contains(err.Error(), "not a time") {
		t.Errorf("number attribute: err = %v", err)
	}
}

func TestParseRange(t *testing.T) {
	km := func(state, unit string) (*float64, error) {
		return ParseRange(EntityState{EntityID: "sensor.leaf_range", State: state, Attributes: map[string]any{"unit_of_measurement": unit}})
	}
	if v, err := km("181.4", "km"); err != nil || v == nil || *v != 181 {
		t.Errorf("km = %v %v", v, err)
	}
	if v, err := km("100", "mi"); err != nil || v == nil || *v != 161 {
		t.Errorf("miles = %v %v", v, err)
	}
	if v, err := km("unavailable", "km"); err != nil || v != nil {
		t.Errorf("unavailable = %v %v", v, err)
	}
	if v, err := km("0", "km"); err != nil || v != nil {
		t.Errorf("zero = %v %v, want unknown", v, err)
	}
	if _, err := km("5", "h"); err == nil {
		t.Error("hours accepted as a range")
	}
}

func TestHTTPClient(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.Method != http.MethodGet {
			t.Errorf("method %s: the client must only read", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer secret-token" {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte("401: Unauthorized"))
			return
		}
		switch r.URL.Path {
		case "/api/states/sensor.leaf_battery":
			json.NewEncoder(w).Encode(entity("72", nil))
		case "/api/states/sensor.leaf_range":
			w.Write([]byte(`{"entity_id":"sensor.leaf_range","state":"190","attributes":{"unit_of_measurement":"km"}}`))
		case "/api/states/sensor.missing":
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"message":"Entity not found."}`))
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()
	c := &HTTPClient{
		BaseURL: srv.URL + "/", Token: "secret-token", HTTP: srv.Client(),
		SoCEntity: "sensor.leaf_battery", RangeEntity: "sensor.leaf_range", Location: amsterdam,
	}
	ctx := context.Background()
	r, err := c.SoC(ctx)
	if err != nil || r.Percent != 72 || r.RangeKm == nil || *r.RangeKm != 190 || !r.ObservedAt.Equal(reported) {
		t.Fatalf("reading = %+v %v", r, err)
	}
	if strings.Join(paths, ",") != "/api/states/sensor.leaf_battery,/api/states/sensor.leaf_range" {
		t.Errorf("paths = %v", paths)
	}

	// A broken range entity only drops the range.
	c.RangeEntity = "sensor.missing"
	if r, err := c.SoC(ctx); err != nil || r.RangeKm != nil {
		t.Errorf("missing range entity = %+v %v", r, err)
	}

	c.SoCEntity = "sensor.missing"
	if _, err := c.SoC(ctx); !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "sensor.missing") {
		t.Errorf("missing entity: err = %v", err)
	}
	c.Token = "wrong"
	_, err = c.SoC(ctx)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 401 || !strings.Contains(err.Error(), "access token rejected") {
		t.Errorf("wrong token: err = %v", err)
	}
	if strings.Contains(err.Error(), "wrong") {
		t.Errorf("error contains the token: %v", err)
	}
}

func TestStubTrailsTheCar(t *testing.T) {
	now := time.Date(2026, 11, 9, 18, 0, 30, 0, time.UTC)
	s := Stub{Now: func() time.Time { return now }, Car: func() (float64, float64) { return 38, 122 }}
	r, err := s.SoC(context.Background())
	if err != nil || r.Percent != 38 || r.RangeKm == nil || *r.RangeKm != 122 ||
		!r.ObservedAt.Equal(time.Date(2026, 11, 9, 17, 40, 0, 0, time.UTC)) {
		t.Fatalf("stub reading = %+v %v", r, err)
	}
}

func TestCandidates(t *testing.T) {
	states := []EntityState{
		{EntityID: "sensor.phone_battery", State: "80", Attributes: map[string]any{"device_class": "battery", "unit_of_measurement": "%", "friendly_name": "Phone"}},
		{EntityID: "sensor.leaf_range", State: "190", Attributes: map[string]any{"device_class": "distance", "unit_of_measurement": "km"}},
		{EntityID: "sensor.leaf_battery", State: "72", Attributes: map[string]any{"device_class": "battery", "unit_of_measurement": "%", "friendly_name": "Leaf battery"}},
		{EntityID: "sensor.leaf_battery_kwh", State: "40", Attributes: map[string]any{"device_class": "energy_storage", "unit_of_measurement": "kWh"}},
		{EntityID: "binary_sensor.leaf_plugged", State: "on", Attributes: map[string]any{"device_class": "plug"}},
		{EntityID: "sensor.trip", State: "12", Attributes: map[string]any{"unit_of_measurement": "mi"}},
	}
	got := Candidates(states)
	var ids []string
	for _, c := range got {
		ids = append(ids, c.EntityID+":"+c.Kind)
	}
	if strings.Join(ids, ",") != "sensor.leaf_battery:battery,sensor.leaf_range:range,sensor.phone_battery:battery,sensor.trip:range" {
		t.Errorf("candidates = %v", ids)
	}
	if got[0].Name != "Leaf battery" || got[0].State != "72" || got[0].Unit != "%" {
		t.Errorf("first = %+v", got[0])
	}
	stub, _ := Stub{Now: time.Now, Car: func() (float64, float64) { return 38, 122 }}.Sensors(context.Background())
	if len(stub) != 2 || stub[0].EntityID != StubEntity || stub[0].State != "38" || stub[1].Kind != KindRange {
		t.Errorf("stub sensors = %+v", stub)
	}
}
