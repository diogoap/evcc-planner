package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestPrecedenceYAMLLocalEnv(t *testing.T) {
	dir := t.TempDir()
	base := write(t, dir, "config.yaml", `
evcc:
  url: http://127.0.0.1:7070
  poll_interval: 45s
defaults:
  consumption_kwh_per_100km: 17
`)
	local := write(t, dir, "config.local.yaml", `
defaults:
  arrival_reserve_pct: 20
places:
  - name: School
    address: "School 2, Town"
`)
	envFile := write(t, dir, ".env", "# secrets\nGOOGLE_MAPS_API_KEY=\"from-file\"\nexport EVCC_URL=http://192.0.2.10:7070\n")
	cfg, err := Load(Options{Path: base, LocalPath: local, EnvFile: envFile, Getenv: env(map[string]string{
		"GOOGLE_CALENDAR_ICAL_URL": "https://cal.example/ical/a/private-s1/basic.ics",
	})})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EVCC.URL != "http://192.0.2.10:7070" {
		t.Errorf("EVCC_URL from .env not applied: %s", cfg.EVCC.URL)
	}
	if cfg.EVCC.PollInterval != 45*time.Second {
		t.Errorf("poll interval = %s, want 45s from base", cfg.EVCC.PollInterval)
	}
	if cfg.Defaults.ConsumptionKWhPer100Km != 17 || cfg.Defaults.ArrivalReservePct != 20 {
		t.Errorf("defaults merge wrong: %+v", cfg.Defaults)
	}
	if cfg.Defaults.UsableCapacityKWh != 59 {
		t.Errorf("built-in default lost: %v", cfg.Defaults.UsableCapacityKWh)
	}
	if cfg.Routes.APIKey != "from-file" {
		t.Errorf("api key from .env = %q", cfg.Routes.APIKey)
	}
	if len(cfg.Calendar.Sources) != 1 || cfg.Calendar.Sources[0].Label != "Calendar" {
		t.Errorf("calendar sources = %+v", cfg.Calendar.Sources)
	}
	if len(cfg.Places) != 1 || cfg.Location.String() != "Europe/Amsterdam" {
		t.Errorf("places/location: %+v %s", cfg.Places, cfg.Location)
	}
	if !strings.HasSuffix(cfg.DBPath(), "planner.db") {
		t.Errorf("db path = %s", cfg.DBPath())
	}

	// Real environment variables win over .env.
	cfg, _ = Load(Options{Path: base, EnvFile: envFile, Getenv: env(map[string]string{"EVCC_URL": "http://real:7070"})})
	if cfg.EVCC.URL != "http://real:7070" {
		t.Errorf("real environment must win over .env: %s", cfg.EVCC.URL)
	}
}

func TestCalendarSources(t *testing.T) {
	list := CalendarSources("https://calendar.google.com/calendar/ical/kids%40group/private-abc/basic.ics,\n" +
		" webcal://calendar.google.com/calendar/ical/me%40gmail.com/private-def/basic.ics " +
		"https://calendar.google.com/calendar/ical/kids%40group/private-abc/basic.ics")
	if len(list) != 2 || list[0].Label != "Calendar 1" || list[1].Label != "Calendar 2" || list[0].ID == list[1].ID {
		t.Fatalf("sources = %+v", list)
	}
	// Resetting Google's secret address keeps the ID (and the local edits).
	again := CalendarSources("https://calendar.google.com/calendar/ical/kids%40group/private-NEW/basic.ics")
	if again[0].ID != list[0].ID {
		t.Errorf("ID changed with the secret: %s vs %s", again[0].ID, list[0].ID)
	}
	if len(CalendarSources("  ")) != 0 {
		t.Error("blank value should give no calendars")
	}
}

func TestEnvOverridesAndDemo(t *testing.T) {
	dir := t.TempDir()
	base := write(t, dir, "config.yaml", "evcc:\n  mode: live\n")
	cfg, err := Load(Options{Path: base, Getenv: env(map[string]string{
		"EVCC_PLANNER_LISTEN": "0.0.0.0:7201", "EVCC_URL": "http://evcc:7070", "EVCC_PLANNER_DEMO": "true",
		"EVCC_PLANNER_FIXED_TIME": "2026-11-09T19:00:00+01:00", "EVCC_PLANNER_DATA_DIR": "/data",
	})})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Listen != "0.0.0.0:7201" || cfg.EVCC.URL != "http://evcc:7070" {
		t.Errorf("env overrides not applied: %+v %+v", cfg.Server, cfg.EVCC)
	}
	if cfg.EVCC.Mode != ModeStub || cfg.Calendar.Mode != ModeStub || cfg.Routes.Mode != ModeStub {
		t.Errorf("demo must force stubs: %s %s %s", cfg.EVCC.Mode, cfg.Calendar.Mode, cfg.Routes.Mode)
	}
	if cfg.Fixed == nil || cfg.DBPath() != filepath.Join("/data", "demo.db") {
		t.Errorf("fixed time %v, db %s", cfg.Fixed, cfg.DBPath())
	}
}

func TestValidation(t *testing.T) {
	cases := map[string]string{
		"bad mode":      "evcc:\n  mode: magic\n",
		"bad timezone":  "timezone: Mars/Base\n",
		"bad loadpoint": "evcc:\n  loadpoint: 0\n",
		"bad default":   "defaults:\n  charging_efficiency_pct: 120\n",
		"bad yaml":      "evcc: [\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			p := write(t, t.TempDir(), "config.yaml", content)
			if _, err := Load(Options{Path: p, Getenv: env(nil)}); err == nil {
				t.Fatal("want error")
			}
		})
	}
	if _, err := Load(Options{Path: filepath.Join(t.TempDir(), "missing.yaml"), Getenv: env(nil)}); err == nil {
		t.Fatal("missing config.yaml must fail")
	}
}

func TestReadEnvFile(t *testing.T) {
	dir := t.TempDir()
	m, err := ReadEnvFile(filepath.Join(dir, "absent"))
	if err != nil || len(m) != 0 {
		t.Fatalf("absent file: %v %v", m, err)
	}
	p := write(t, dir, ".env", "A=1\n\n# c\nB='two words'\nC=x=y\n")
	m, err = ReadEnvFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if m["A"] != "1" || m["B"] != "two words" || m["C"] != "x=y" {
		t.Errorf("parsed %v", m)
	}
	bad := write(t, dir, "bad.env", "NOEQUALS\n")
	if _, err := ReadEnvFile(bad); err == nil {
		t.Error("want error for line without =")
	}
}

func TestRepositoryConfigLoads(t *testing.T) {
	cfg, err := Load(Options{Path: "../../config.yaml", Getenv: env(nil)})
	if err != nil {
		t.Fatalf("config.yaml must load: %v", err)
	}
	if cfg.Server.Listen != "127.0.0.1:7201" || !strings.HasPrefix(cfg.EVCC.URL, "http://127.0.0.1:") {
		t.Errorf("committed config must use loopback addresses: %+v %+v", cfg.Server, cfg.EVCC)
	}
}
