package app

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/diogoap/evcc-planner/internal/config"
	"github.com/diogoap/evcc-planner/internal/evcc"
	"github.com/diogoap/evcc-planner/internal/model"
	"github.com/diogoap/evcc-planner/internal/service"
)

type connectionSettingsView struct {
	Settings    model.ConnectionSettings `json:"settings"`
	Defaults    model.ConnectionSettings `json:"defaults"`
	Env         []service.EnvView        `json:"env"`
	Connections []struct {
		Name, Status, Detail string
	} `json:"connections"`
}

func (ta *testApp) connectionSettings() connectionSettingsView {
	ta.t.Helper()
	var v connectionSettingsView
	ta.json("GET", "/api/settings/connections", nil, http.StatusOK, &v)
	return v
}

func TestConnectionSettingsAreEditedInTheUI(t *testing.T) {
	ta := newTestApp(t, "")
	log := &eventLog{ta: ta}
	log.fresh()

	v := ta.connectionSettings()
	if v.Settings != v.Defaults || v.Settings != model.DefaultConnectionSettings() {
		t.Fatalf("unsaved settings should be the defaults: %+v", v.Settings)
	}
	envs := map[string]service.EnvView{}
	for _, e := range v.Env {
		envs[e.Env] = e
	}
	if _, ok := envs["HA_TOKEN"]; !ok || envs["HA_TOKEN"].Set || envs["GOOGLE_MAPS_API_KEY"].Set || v.Settings.HAURL != "http://127.0.0.1:8123" {
		t.Errorf("env = %+v", v.Env)
	}
	if _, ok := envs["HA_URL"]; ok {
		t.Errorf("the Home Assistant address is a setting now: %+v", v.Env)
	}

	bad := v.Settings
	bad.EVCCPollIntervalSec = 1
	ta.json("PUT", "/api/settings/connections", bad, http.StatusBadRequest, nil)
	bad = v.Settings
	bad.EVCCURL = "http://admin:secret@evcc:7070"
	ta.json("PUT", "/api/settings/connections", bad, http.StatusBadRequest, nil)
	log.none("rejected input")

	edit := v.Settings
	edit.HAPollIntervalMin, edit.HASoCEntity, edit.RetentionDays = 5, " sensor.demo_leaf_battery ", 30
	var saved connectionSettingsView
	ta.json("PUT", "/api/settings/connections", edit, http.StatusOK, &saved)
	if saved.Settings.HASoCEntity != "sensor.demo_leaf_battery" || saved.Settings.RetentionDays != 30 || len(saved.Connections) == 0 {
		t.Fatalf("saved = %+v", saved)
	}
	e := log.one("settings.update", model.SourceUI)
	if e.Summary != "Changed connection settings: Home Assistant poll interval, Home Assistant SoC sensor, history retention" {
		t.Errorf("summary = %q", e.Summary)
	}
	changed(t, e, "retentionDays", 90.0, 30.0)
	if svc := ta.app.Service; svc.Conn().RetentionDays != 30 || svc.Conn().HAPollInterval() != 5*time.Minute {
		t.Errorf("not applied: %+v", svc.Conn())
	}
	if p := ta.activity("?limit=1"); p.RetentionDays != 30 {
		t.Errorf("activity retention = %d", p.RetentionDays)
	}

	// Only changed fields are stored: the others follow the defaults they
	// are read with.
	seeded := model.DefaultConnectionSettings()
	seeded.EVCCURL = "http://192.0.2.10:7070"
	stored, err := ta.app.Store.ConnectionSettings(seeded)
	if err != nil || stored.EVCCURL != seeded.EVCCURL || stored.RetentionDays != 30 {
		t.Errorf("stored = %+v %v", stored, err)
	}

	// Kept across a restart.
	again := newTestApp(t, ta.dir)
	if again.app.Service.Conn().RetentionDays != 30 {
		t.Errorf("after restart: %+v", again.app.Service.Conn())
	}

	// A demo reset brings back the defaults, for these and the cached
	// planning settings.
	ta.json("PUT", "/api/settings", map[string]any{
		"homeAddress": "Elsewhere 1", "usableCapacityKWh": 59, "chargePowerKW": 3.68, "consumptionKWhPer100Km": 18,
		"arrivalReservePct": 25, "chargingEfficiencyPct": 90, "departureBufferMin": 15, "chargeBufferMin": 30, "defaultStayMin": 15,
	}, http.StatusOK, nil)
	ta.json("POST", "/api/demo/reset", map[string]any{}, http.StatusNoContent, nil)
	var settings model.Settings
	ta.json("GET", "/api/settings", nil, http.StatusOK, &settings)
	if ta.app.Service.Conn() != model.DefaultConnectionSettings() || settings.ArrivalReservePct != 15 || settings.HomeAddress == "Elsewhere 1" {
		t.Errorf("after reset: %+v %+v", ta.app.Service.Conn(), settings)
	}
}

func TestEVCCTargetChangeWaitsForActiveAdjustments(t *testing.T) {
	ta := newTestApp(t, "")
	now := ta.app.Service.Clock.Now()
	a, err := ta.app.Store.AddAdjustment(model.Adjustment{
		Kind: model.AdjustPostpone, Source: "ui", AdviceDate: "2026-11-09", Vehicle: evcc.StubVehicle, Loadpoint: 1,
		ExpiresAt: now.Add(12 * time.Hour), Status: model.AdjustActive, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	edit := ta.connectionSettings().Settings
	edit.EVCCLoadpoint = 2
	code, body := ta.do("PUT", "/api/settings/connections", edit)
	if code != http.StatusBadRequest || !strings.Contains(string(body), "End the active postponement first") {
		t.Fatalf("PUT = %d %s", code, body)
	}
	// Other settings can still change.
	edit = ta.connectionSettings().Settings
	edit.RetentionDays = 60
	ta.json("PUT", "/api/settings/connections", edit, http.StatusOK, nil)

	a.Status = model.AdjustRestored
	if err := ta.app.Store.UpdateAdjustment(a); err != nil {
		t.Fatal(err)
	}
	edit.EVCCLoadpoint = 2
	ta.json("PUT", "/api/settings/connections", edit, http.StatusOK, nil)
	if status := ta.app.Service.Status(); status.Charger != "" {
		t.Errorf("loadpoint 2 does not exist in the demo: %+v", status)
	}
}

func TestNewEVCCTargetIsNotAPlugIn(t *testing.T) {
	ta := newTestApp(t, "")
	ta.json("POST", "/api/demo/reset", map[string]any{}, http.StatusNoContent, nil) // learns: unplugged
	ta.tripPlans(model.TripPlansPlugIn)
	log := &eventLog{ta: ta}
	log.fresh()

	// The car is plugged in when the planner switches to it (here: names the
	// vehicle explicitly). That is not a plug-in to act on.
	ta.app.Stub.SetVehicle(true, 30)
	edit := ta.connectionSettings().Settings
	edit.EVCCVehicle = evcc.StubVehicle
	ta.json("PUT", "/api/settings/connections", edit, http.StatusOK, nil)
	if err := ta.app.Service.SyncPlan(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p := ta.evccPlan(); p != nil {
		t.Errorf("a plan was sent: %+v", p)
	}
	log.one("settings.update", model.SourceUI)

	// Unplugging and plugging in again does count.
	ta.json("POST", "/api/demo/vehicle", map[string]any{"connected": false, "soc": 30}, http.StatusOK, nil)
	ta.json("POST", "/api/demo/vehicle", map[string]any{"connected": true, "soc": 30}, http.StatusOK, nil)
	if p := ta.evccPlan(); p == nil {
		t.Error("plugging in again should send the plan")
	}
}

// blockingEVCC answers State only when released.
type blockingEVCC struct {
	evcc.Client
	release chan struct{}
}

func (b blockingEVCC) State(ctx context.Context) (*evcc.State, error) {
	<-b.release
	return &evcc.State{Version: "old"}, nil
}

func TestPollFromBeforeASaveIsDropped(t *testing.T) {
	ta := newTestApp(t, "")
	svc := ta.app.Service
	release := make(chan struct{})
	svc.UseIntegrations(func(i *service.Integrations) { i.EVCC = blockingEVCC{Client: i.EVCC, release: release} })
	done := make(chan error, 1)
	go func() { done <- svc.PollEVCC(context.Background()) }()

	edit := ta.connectionSettings().Settings
	edit.EVCCTimeoutSec = 5
	ta.json("PUT", "/api/settings/connections", edit, http.StatusOK, nil) // polls the demo EVCC again
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if v := svc.Status().Version; v != "stub" {
		t.Errorf("version = %q: the old poll overwrote the new one", v)
	}
}

func TestHomeAssistantConfiguredFromSettings(t *testing.T) {
	const token = "ha-TESTSECRET-token"
	ha := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		// The SoC sensor is not written again while its value is unchanged;
		// the reading-time sensor shows when the car last reported.
		battery := map[string]any{
			"entity_id": "sensor.leaf_battery", "state": "72", "last_changed": "2026-11-09T15:00:00Z",
			"last_updated": "2026-11-09T15:00:00Z", "last_reported": "2026-11-09T15:00:00Z",
			"attributes": map[string]any{"unit_of_measurement": "%", "device_class": "battery", "friendly_name": "Leaf battery"},
		}
		updated := map[string]any{
			"entity_id": "sensor.leaf_last_update", "state": "2026-11-09T17:40:00+00:00",
			"attributes": map[string]any{"device_class": "timestamp"},
		}
		switch r.URL.Path {
		case "/api/states":
			json.NewEncoder(w).Encode([]any{battery, updated, map[string]any{
				"entity_id": "sensor.leaf_range", "state": "190",
				"attributes": map[string]any{"unit_of_measurement": "km", "device_class": "distance"},
			}})
		case "/api/states/sensor.leaf_battery":
			json.NewEncoder(w).Encode(battery)
		case "/api/states/sensor.leaf_last_update":
			json.NewEncoder(w).Encode(updated)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ha.Close()

	dir := t.TempDir()
	yaml := "fixed_time: " + fixedNow + "\ndata_dir: " + filepath.Join(dir, "data") +
		"\nevcc:\n  mode: stub\ncalendar:\n  mode: stub\nroutes:\n  mode: stub\ntibber:\n  mode: stub\n" +
		"openrouter:\n  mode: stub\nknmi:\n  mode: stub\n"
	cfgPath := filepath.Join(dir, "config.yaml")
	os.WriteFile(cfgPath, []byte(yaml), 0o600)
	env := map[string]string{"HA_TOKEN": token}
	cfg, err := config.Load(config.Options{Path: cfgPath, Getenv: func(k string) string { return env[k] }})
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	ta := &testApp{t: t, app: a, dir: dir}

	if status, detail := ta.connection("Home Assistant"); status != "not_configured" || !strings.Contains(detail, "Choose the car's SoC sensor") {
		t.Fatalf("before = %s %q", status, detail)
	}
	// The address first, then the sensors it lists.
	edit := ta.connectionSettings().Settings
	edit.HAURL = ha.URL
	ta.json("PUT", "/api/settings/connections", edit, http.StatusOK, nil)
	var sensors []struct{ EntityID, Kind string }
	ta.json("GET", "/api/homeassistant/sensors", nil, http.StatusOK, &sensors)
	if len(sensors) != 3 || sensors[0].EntityID != "sensor.leaf_battery" || sensors[1].Kind != "timestamp" || sensors[2].Kind != "range" {
		t.Fatalf("sensors = %+v", sensors)
	}

	edit.HASoCEntity = "sensor.leaf_battery"
	var saved connectionSettingsView
	ta.json("PUT", "/api/settings/connections", edit, http.StatusOK, &saved)
	found := false
	for _, c := range saved.Connections {
		if c.Name == "Home Assistant" {
			found = c.Status == "ok" && c.Detail == "SoC 72% from sensor.leaf_battery, reported Mon 9 Nov 16:00"
		}
	}
	if !found {
		t.Errorf("connections after save = %+v", saved.Connections)
	}
	if s := ta.soc(); s.Source != "homeassistant" || *s.Percent != 72 || !s.ObservedAt.Equal(local("2026-11-09T16:00:00+01:00")) {
		t.Errorf("soc = %+v", s)
	}
	// The reading-time sensor dates the reading to when the car reported.
	edit.HAUpdatedEntity = "sensor.leaf_last_update"
	ta.json("PUT", "/api/settings/connections", edit, http.StatusOK, nil)
	if status, detail := ta.connection("Home Assistant"); status != "ok" || !strings.HasSuffix(detail, "reported Mon 9 Nov 18:40") {
		t.Errorf("with reading-time sensor = %s %q", status, detail)
	}
	if s := ta.soc(); !s.ObservedAt.Equal(local("2026-11-09T18:40:00+01:00")) {
		t.Errorf("soc with reading-time sensor = %+v", s)
	}
	raw, _ := json.Marshal(ta.activity("?limit=500"))
	// The address is a setting like EVCC's (its change is logged); the token
	// never appears.
	if strings.Contains(string(raw), token) {
		t.Errorf("the activity log contains the Home Assistant token: %s", raw)
	}
}

func TestEveryFollowsTheInterval(t *testing.T) {
	defer func(d time.Duration) { everyCheck = d }(everyCheck)
	everyCheck = 2 * time.Millisecond
	var interval atomic.Int64
	interval.Store(int64(time.Hour))
	var runs atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	every(ctx, &wg, func() time.Duration { return time.Duration(interval.Load()) }, func() { runs.Add(1) })

	time.Sleep(20 * time.Millisecond)
	if n := runs.Load(); n != 1 {
		t.Fatalf("runs = %d, want only the first run within the hour", n)
	}
	interval.Store(int64(5 * time.Millisecond))
	deadline := time.Now().Add(2 * time.Second)
	for runs.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	wg.Wait()
	if n := runs.Load(); n < 3 {
		t.Errorf("runs = %d after shortening the interval", n)
	}
}
