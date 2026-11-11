package app

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/diogoap/evcc-planner/internal/homeassistant"
	"github.com/diogoap/evcc-planner/internal/service"
)

type socReading struct {
	Source     string    `json:"source"`
	Percent    float64   `json:"percent"`
	ObservedAt time.Time `json:"observedAt"`
	RangeKm    *float64  `json:"rangeKm"`
}

type socView struct {
	Percent    *float64     `json:"percent"`
	Known      bool         `json:"known"`
	Source     string       `json:"source"`
	Live       bool         `json:"live"`
	Stale      bool         `json:"stale"`
	ObservedAt *time.Time   `json:"observedAt"`
	RangeKm    *float64     `json:"rangeKm"`
	Other      []socReading `json:"other"`
}

func (ta *testApp) soc() socView {
	ta.t.Helper()
	var p struct {
		SoC socView `json:"soc"`
	}
	ta.json("GET", "/api/plan", nil, http.StatusOK, &p)
	return p.SoC
}

func (ta *testApp) connection(name string) (status, detail string) {
	ta.t.Helper()
	var conns []struct{ Name, Status, Detail string }
	ta.json("GET", "/api/connections", nil, http.StatusOK, &conns)
	for _, c := range conns {
		if c.Name == name {
			return c.Status, c.Detail
		}
	}
	ta.t.Fatalf("no %s connection in %+v", name, conns)
	return "", ""
}

func local(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

type failingHA struct{}

func (failingHA) SoC(context.Context) (*homeassistant.Reading, error) {
	return nil, errors.New("home assistant unreachable: connection refused")
}

func TestSoCFromHomeAssistantWhileUnplugged(t *testing.T) {
	ta := newTestApp(t, "")
	svc := ta.app.Service
	ctx := context.Background()

	// Before Home Assistant is read: EVCC's last reading, from 16:45.
	if s := ta.soc(); s.Source != "evcc" || s.Live || s.Stale || !s.ObservedAt.Equal(local("2026-11-09T16:45:00+01:00")) || len(s.Other) != 0 {
		t.Fatalf("seeded soc = %+v", s)
	}

	// Home Assistant has the unplugged car's SoC, 20 minutes old: newer, so
	// it is used, and EVCC's reading is listed with its own time.
	if err := svc.PollHomeAssistant(ctx); err != nil {
		t.Fatal(err)
	}
	s := ta.soc()
	if s.Source != "homeassistant" || *s.Percent != 38 || s.Live || s.Stale || s.RangeKm == nil || *s.RangeKm != 122 ||
		!s.ObservedAt.Equal(local("2026-11-09T18:40:00+01:00")) {
		t.Fatalf("home assistant soc = %+v", s)
	}
	if len(s.Other) != 1 || s.Other[0].Source != "evcc" || !s.Other[0].ObservedAt.Equal(local("2026-11-09T16:45:00+01:00")) {
		t.Errorf("other readings = %+v", s.Other)
	}
	if status, detail := ta.connection("Home Assistant"); status != "stub" ||
		detail != "SoC 38% from sensor.demo_leaf_battery, reported Mon 9 Nov 18:40" {
		t.Errorf("connection = %s %q", status, detail)
	}

	// Plugged in, EVCC's live reading wins: it is what EVCC charges by.
	ta.json("POST", "/api/demo/vehicle", map[string]any{"connected": true, "soc": 70}, http.StatusOK, nil)
	s = ta.soc()
	if s.Source != "evcc" || !s.Live || *s.Percent != 70 || len(s.Other) != 1 || s.Other[0].Source != "homeassistant" || s.Other[0].Percent != 70 {
		t.Fatalf("plugged-in soc = %+v", s)
	}

	// Just unplugged: EVCC's last reading (19:00) is newer than Home
	// Assistant's (18:40) until Home Assistant updates.
	ta.json("POST", "/api/demo/vehicle", map[string]any{"connected": false, "soc": 70}, http.StatusOK, nil)
	if s := ta.soc(); s.Source != "evcc" || s.Live || s.Stale || *s.Percent != 70 {
		t.Fatalf("just unplugged soc = %+v", s)
	}
	ta.at(local("2026-11-09T20:00:00+01:00"))
	if err := svc.PollHomeAssistant(ctx); err != nil {
		t.Fatal(err)
	}
	if s := ta.soc(); s.Source != "homeassistant" || !s.ObservedAt.Equal(local("2026-11-09T19:40:00+01:00")) {
		t.Fatalf("after a Home Assistant update = %+v", s)
	}

	// Home Assistant fails: the stored readings are kept, and the reading
	// turns stale after three hours without updates.
	svc.UseIntegrations(func(i *service.Integrations) { i.HA = failingHA{} })
	if err := svc.PollHomeAssistant(ctx); err == nil {
		t.Fatal("want an error")
	}
	if status, detail := ta.connection("Home Assistant"); status != "error" || !strings.Contains(detail, "connection refused") {
		t.Errorf("failing connection = %s %q", status, detail)
	}
	if s := ta.soc(); s.Source != "homeassistant" || s.Stale || *s.Percent != 70 {
		t.Fatalf("soc while Home Assistant fails = %+v", s)
	}
	ta.at(local("2026-11-09T22:41:00+01:00"))
	if s := ta.soc(); !s.Stale || s.Source != "homeassistant" {
		t.Fatalf("soc after 3 h without updates = %+v, want stale", s)
	}
}

func TestHomeAssistantNotConfigured(t *testing.T) {
	ta := newTestApp(t, "")
	ta.app.Service.UseIntegrations(func(i *service.Integrations) {
		i.HA, i.HASensors, i.HANote = nil, nil, "Set HA_TOKEN (and HA_URL) to read the SoC while the car is unplugged"
	})
	if err := ta.app.Service.PollHomeAssistant(context.Background()); err != nil {
		t.Fatal(err)
	}
	if status, detail := ta.connection("Home Assistant"); status != "not_configured" || !strings.Contains(detail, "HA_TOKEN") {
		t.Errorf("connection = %s %q", status, detail)
	}
	if s := ta.soc(); s.Source != "evcc" || *s.Percent != 38 {
		t.Errorf("soc = %+v, want EVCC's last reading", s)
	}
}

// fixedHA reports a set reading, e.g. what a lagging cloud integration says.
type fixedHA struct{ r homeassistant.Reading }

func (f *fixedHA) SoC(context.Context) (*homeassistant.Reading, error) {
	r := f.r
	return &r, nil
}

func TestEVCCWinsWhilePluggedInAndUntilHomeAssistantHasNews(t *testing.T) {
	ta := newTestApp(t, "")
	svc := ta.app.Service
	ctx := context.Background()
	reading := func(pct float64, reported, changed string) homeassistant.Reading {
		return homeassistant.Reading{Entity: "sensor.leaf_battery", Percent: pct, ObservedAt: local(reported), ChangedAt: local(changed)}
	}
	ha := &fixedHA{r: reading(74, "2026-11-09T18:55:00+01:00", "2026-11-09T18:30:00+01:00")}
	svc.UseIntegrations(func(i *service.Integrations) { i.HA = ha })

	// Charging: EVCC's live 80% wins over Home Assistant's lagging 74%.
	ta.json("POST", "/api/demo/vehicle", map[string]any{"connected": true, "soc": 80}, http.StatusOK, nil)
	if s := ta.soc(); s.Source != "evcc" || !s.Live || *s.Percent != 80 || len(s.Other) != 1 || s.Other[0].Percent != 74 {
		t.Fatalf("plugged in = %+v", s)
	}
	// Still plugged in at 19:04; unchanged readings are saved every five
	// minutes, so the saved one is from 19:00.
	ta.at(local("2026-11-09T19:04:00+01:00"))
	ta.poll()
	// Unplugged at 19:05: EVCC's reading is dated to the last poll that saw
	// the car plugged in.
	ta.at(local("2026-11-09T19:05:00+01:00"))
	ta.app.Stub.SetVehicle(false, 0)
	ta.poll()
	if r, _ := ta.app.Store.SoCReading("evcc"); r == nil || r.Percent != 80 || !r.ObservedAt.Equal(local("2026-11-09T19:04:00+01:00")) {
		t.Fatalf("evcc reading after unplugging = %+v", r)
	}

	// Home Assistant reports its old value again after unplugging: still EVCC.
	ha.r = reading(74, "2026-11-09T19:30:00+01:00", "2026-11-09T18:30:00+01:00")
	ta.at(local("2026-11-09T19:30:00+01:00"))
	if err := svc.PollHomeAssistant(ctx); err != nil {
		t.Fatal(err)
	}
	if s := ta.soc(); s.Source != "evcc" || s.Live || *s.Percent != 80 {
		t.Fatalf("after a lagging report = %+v", s)
	}
	// After a drive Home Assistant's value changes: now it is used.
	ha.r = reading(65, "2026-11-09T20:30:00+01:00", "2026-11-09T20:25:00+01:00")
	ta.at(local("2026-11-09T20:30:00+01:00"))
	if err := svc.PollHomeAssistant(ctx); err != nil {
		t.Fatal(err)
	}
	if s := ta.soc(); s.Source != "homeassistant" || *s.Percent != 65 {
		t.Fatalf("after a drive = %+v", s)
	}
}
