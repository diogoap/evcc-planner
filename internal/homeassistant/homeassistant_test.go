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
	r, err := ParseSoC(entity("72", nil))
	if err != nil || r.Percent != 72 || !r.ObservedAt.Equal(reported) || r.Entity != "sensor.leaf_battery" ||
		!r.ChangedAt.Equal(reported.Add(-3*time.Hour)) {
		t.Fatalf("reading = %+v %v (want the last reported time, and when the value changed)", r, err)
	}

	// Older Home Assistant versions have no last_reported.
	e := entity("72.5", nil)
	e.LastReported = time.Time{}
	if r, _ := ParseSoC(e); r.Percent != 72.5 || !r.ObservedAt.Equal(reported.Add(-time.Hour)) {
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
		if _, err := ParseSoC(entity(state, nil)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("state %q: err = %v, want %q", state, err, want)
		}
	}
	if _, err := ParseSoC(entity("40", map[string]any{"unit_of_measurement": "kWh"})); err == nil ||
		!strings.Contains(err.Error(), "in kWh, not %") {
		t.Errorf("energy sensor: err = %v", err)
	}
	if r, err := ParseSoC(entity("40", map[string]any{})); err != nil || r.Percent != 40 {
		t.Errorf("no unit = %+v %v", r, err)
	}
}

func TestReadingTimeSensor(t *testing.T) {
	ts := func(state string) EntityState {
		return EntityState{EntityID: "sensor.leaf_last_update", State: state, Attributes: map[string]any{"device_class": "timestamp"}}
	}
	carTime := reported.Add(-40 * time.Minute)
	for _, state := range []string{
		carTime.Format(time.RFC3339Nano),
		carTime.In(amsterdam).Format("2006-01-02T15:04:05"), // no zone: planner time
		carTime.In(amsterdam).Format("2006-01-02 15:04:05"),
	} {
		got, err := ParseTimestamp(ts(state), amsterdam)
		if err != nil || !got.Equal(carTime) {
			t.Errorf("%s: %v %v, want %v", state, got, err, carTime)
		}
	}
	for _, state := range []string{"unavailable", "yesterday"} {
		if _, err := ParseTimestamp(ts(state), amsterdam); err == nil || !strings.Contains(err.Error(), "sensor.leaf_last_update") {
			t.Errorf("%s: err = %v", state, err)
		}
	}

	r, _ := ParseSoC(entity("61", nil))
	r.SetCarTime(carTime)
	if !r.ObservedAt.Equal(carTime) || !r.ChangedAt.Equal(carTime) {
		t.Errorf("car time = %+v", r)
	}
	// Never later than Home Assistant received the SoC.
	r, _ = ParseSoC(entity("61", nil))
	r.SetCarTime(reported.Add(time.Hour))
	if !r.ObservedAt.Equal(reported) || !r.ChangedAt.Equal(reported) {
		t.Errorf("future car time = %+v, want %v", r, reported)
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
		case "/api/states/sensor.leaf_last_update":
			w.Write([]byte(`{"entity_id":"sensor.leaf_last_update","state":"2026-11-11T07:30:00+00:00","attributes":{"device_class":"timestamp"}}`))
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

	// The reading-time sensor dates the reading; a missing one fails it.
	c.UpdatedEntity = "sensor.leaf_last_update"
	if r, err := c.SoC(ctx); err != nil || !r.ObservedAt.Equal(reported.Add(-40*time.Minute)) {
		t.Errorf("with reading-time sensor = %+v %v", r, err)
	}
	c.UpdatedEntity = "sensor.missing"
	if _, err := c.SoC(ctx); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing reading-time sensor: err = %v", err)
	}
	c.UpdatedEntity = ""

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
		{EntityID: "sensor.leaf_last_update", State: "2026-11-11T07:30:00+00:00", Attributes: map[string]any{"device_class": "timestamp"}},
	}
	got := Candidates(states)
	var ids []string
	for _, c := range got {
		ids = append(ids, c.EntityID+":"+c.Kind)
	}
	if strings.Join(ids, ",") != "sensor.leaf_battery:battery,sensor.leaf_last_update:timestamp,sensor.leaf_range:range,sensor.phone_battery:battery,sensor.trip:range" {
		t.Errorf("candidates = %v", ids)
	}
	if got[0].Name != "Leaf battery" || got[0].State != "72" || got[0].Unit != "%" {
		t.Errorf("first = %+v", got[0])
	}
	stub, _ := Stub{Now: time.Now, Car: func() (float64, float64) { return 38, 122 }}.Sensors(context.Background())
	if len(stub) != 3 || stub[0].EntityID != StubEntity || stub[0].State != "38" || stub[1].Kind != KindTimestamp || stub[2].Kind != KindRange {
		t.Errorf("stub sensors = %+v", stub)
	}
}
