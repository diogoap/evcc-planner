// Package config loads system settings from config.yaml, the private
// config.local.yaml overlay, and environment variables (including .env).
package config

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"go.yaml.in/yaml/v3"

	"github.com/diogoap/evcc-planner/internal/model"
)

// Integration modes.
const (
	ModeLive   = "live"
	ModeStub   = "stub"
	ModeICal   = "ical"
	ModeGoogle = "google"
)

type Config struct {
	Server     ServerConfig     `yaml:"server"`
	Timezone   string           `yaml:"timezone"`
	DataDir    string           `yaml:"data_dir"`
	Demo       bool             `yaml:"demo"`
	FixedTime  string           `yaml:"fixed_time"`
	EVCC       EVCCConfig       `yaml:"evcc"`
	Calendar   CalendarConfig   `yaml:"calendar"`
	Routes     RoutesConfig     `yaml:"routes"`
	Tibber     TibberConfig     `yaml:"tibber"`
	OpenRouter OpenRouterConfig `yaml:"openrouter"`
	KNMI       KNMIConfig       `yaml:"knmi"`
	Forecast   ForecastConfig   `yaml:"forecast"`
	Retention  RetentionConfig  `yaml:"retention"`
	Defaults   Defaults         `yaml:"defaults"`
	Places     []PlaceSeed      `yaml:"places"`

	Location *time.Location `yaml:"-"`
	Fixed    *time.Time     `yaml:"-"`
}

type ServerConfig struct {
	Listen string `yaml:"listen"`
	WebDir string `yaml:"web_dir"`
}

type EVCCConfig struct {
	Mode         string        `yaml:"mode"`
	URL          string        `yaml:"url"`
	Loadpoint    int           `yaml:"loadpoint"`
	Vehicle      string        `yaml:"vehicle"`
	PollInterval time.Duration `yaml:"poll_interval"`
	Timeout      time.Duration `yaml:"timeout"`
}

// CalendarConfig names the environment variable holding one or more secret
// iCal addresses (comma or space separated). Calendar names come from the
// feeds themselves.
type CalendarConfig struct {
	Mode         string        `yaml:"mode"`
	SyncInterval time.Duration `yaml:"sync_interval"`
	URLEnv       string        `yaml:"url_env"`

	Sources []CalendarSource `yaml:"-"`
}

// CalendarSource is one iCal feed.
type CalendarSource struct {
	ID    string // stable; derived from the URL without its secret part
	Label string // shown until the feed's own name is known
	URL   string
}

type RoutesConfig struct {
	Mode         string        `yaml:"mode"`
	APIKeyEnv    string        `yaml:"api_key_env"`
	BaseURL      string        `yaml:"base_url"`
	TrafficAware bool          `yaml:"traffic_aware"`
	CacheTTL     time.Duration `yaml:"cache_ttl"`
	// MonthlyFreeCalls is the provider's free monthly allowance, shown next
	// to the calls made (0 hides it).
	MonthlyFreeCalls int    `yaml:"monthly_free_calls"`
	APIKey           string `yaml:"-"`
}

// TibberConfig locates the weekly price outlook article.
type TibberConfig struct {
	Mode string `yaml:"mode"`
	URL  string `yaml:"url"`
}

// OpenRouterConfig selects the model that structures forecast text. The
// model must support strict JSON Schema output.
type OpenRouterConfig struct {
	Mode      string        `yaml:"mode"`
	BaseURL   string        `yaml:"base_url"`
	Model     string        `yaml:"model"`
	APIKeyEnv string        `yaml:"api_key_env"`
	Timeout   time.Duration `yaml:"timeout"`
	APIKey    string        `yaml:"-"`
}

// KNMIConfig reads KNMI's multi-day outlook from the Open Data API.
type KNMIConfig struct {
	Mode      string `yaml:"mode"`
	BaseURL   string `yaml:"base_url"`
	Dataset   string `yaml:"dataset"`
	Version   string `yaml:"version"`
	APIKeyEnv string `yaml:"api_key_env"`
	APIKey    string `yaml:"-"`
}

// ForecastConfig schedules the daily forecast ingestion.
type ForecastConfig struct {
	DailyAt       string        `yaml:"daily_at"` // local HH:MM
	RetryInterval time.Duration `yaml:"retry_interval"`
	VATPct        float64       `yaml:"vat_pct"`
}

// RetentionConfig sets how long history is kept: activity log entries and
// past trips, per-date edits, plan records, and advice decisions older than
// Days are deleted daily.
type RetentionConfig struct {
	Days int `yaml:"days"`
}

// Defaults are bootstrap planning settings; UI-saved values take precedence.
// The home address is personal and only set in the UI.
type Defaults struct {
	UsableCapacityKWh      float64 `yaml:"usable_capacity_kwh"`
	ChargePowerKW          float64 `yaml:"charge_power_kw"`
	ConsumptionKWhPer100Km float64 `yaml:"consumption_kwh_per_100km"`
	ArrivalReservePct      float64 `yaml:"arrival_reserve_pct"`
	ChargingEfficiencyPct  float64 `yaml:"charging_efficiency_pct"`
	DepartureBufferMin     int     `yaml:"departure_buffer_min"`
	ChargeBufferMin        int     `yaml:"charge_buffer_min"`
	DefaultStayMin         int     `yaml:"default_stay_min"`
}

// PlaceSeed bootstraps a saved place on first start.
type PlaceSeed struct {
	Name    string `yaml:"name"`
	Address string `yaml:"address"`
}

// Settings converts the bootstrap defaults to model settings.
func (d Defaults) Settings() model.Settings {
	return model.Settings{
		UsableCapacityKWh:      d.UsableCapacityKWh,
		ChargePowerKW:          d.ChargePowerKW,
		ConsumptionKWhPer100Km: d.ConsumptionKWhPer100Km,
		ArrivalReservePct:      d.ArrivalReservePct,
		ChargingEfficiencyPct:  d.ChargingEfficiencyPct,
		DepartureBufferMin:     d.DepartureBufferMin,
		ChargeBufferMin:        d.ChargeBufferMin,
		DefaultStayMin:         d.DefaultStayMin,
	}
}

// Default returns built-in values used when config files omit a setting.
func Default() Config {
	return Config{
		Server:   ServerConfig{Listen: "127.0.0.1:7201", WebDir: "web/dist"},
		Timezone: "Europe/Amsterdam",
		DataDir:  "data",
		EVCC: EVCCConfig{
			Mode: ModeLive, URL: "http://127.0.0.1:7070", Loadpoint: 1,
			PollInterval: 30 * time.Second, Timeout: 10 * time.Second,
		},
		Calendar: CalendarConfig{Mode: ModeICal, SyncInterval: time.Hour, URLEnv: "GOOGLE_CALENDAR_ICAL_URL"},
		Routes: RoutesConfig{
			Mode: ModeGoogle, APIKeyEnv: "GOOGLE_MAPS_API_KEY",
			BaseURL: "https://routes.googleapis.com", TrafficAware: true, CacheTTL: 7 * 24 * time.Hour,
			MonthlyFreeCalls: 5000,
		},
		Tibber: TibberConfig{Mode: ModeLive, URL: "https://tibber.com/nl/stroomprijsprognose"},
		OpenRouter: OpenRouterConfig{
			Mode: ModeLive, BaseURL: "https://openrouter.ai/api/v1", Model: "anthropic/claude-haiku-5.5",
			APIKeyEnv: "OPENROUTER_API_KEY", Timeout: 60 * time.Second,
		},
		KNMI: KNMIConfig{
			Mode: ModeLive, BaseURL: "https://api.dataplatform.knmi.nl/open-data/v1",
			Dataset: "outlook_weather_forecast", Version: "1.0", APIKeyEnv: "KNMI_API_KEY",
		},
		Forecast:  ForecastConfig{DailyAt: "18:00", RetryInterval: time.Hour, VATPct: 21},
		Retention: RetentionConfig{Days: 90},
		Defaults: Defaults{
			UsableCapacityKWh: 59, ChargePowerKW: 3.68, ConsumptionKWhPer100Km: 18,
			ArrivalReservePct: 15, ChargingEfficiencyPct: 90,
			DepartureBufferMin: 15, ChargeBufferMin: 30, DefaultStayMin: 15,
		},
	}
}

// Options control where configuration is read from.
type Options struct {
	Path      string // config.yaml; required
	LocalPath string // config.local.yaml; optional
	EnvFile   string // .env; optional, never overrides real environment variables
	Getenv    func(string) string
}

// Load reads and validates configuration.
func Load(opts Options) (*Config, error) {
	getenv := opts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	if opts.EnvFile != "" {
		fileEnv, err := ReadEnvFile(opts.EnvFile)
		if err != nil {
			return nil, err
		}
		real := getenv
		getenv = func(k string) string {
			if v := real(k); v != "" {
				return v
			}
			return fileEnv[k]
		}
	}

	cfg := Default()
	if err := readYAML(opts.Path, &cfg, true); err != nil {
		return nil, err
	}
	if opts.LocalPath != "" {
		if err := readYAML(opts.LocalPath, &cfg, false); err != nil {
			return nil, err
		}
	}
	if err := cfg.applyEnv(getenv); err != nil {
		return nil, err
	}
	if err := cfg.finish(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func readYAML(path string, cfg *Config, required bool) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) && !required {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return fmt.Errorf("parse %s: %w", filepath.Base(path), err)
	}
	return nil
}

func (c *Config) applyEnv(getenv func(string) string) error {
	set := func(key string, dst *string) {
		if v := getenv(key); v != "" {
			*dst = v
		}
	}
	set("EVCC_PLANNER_LISTEN", &c.Server.Listen)
	set("EVCC_PLANNER_WEB_DIR", &c.Server.WebDir)
	set("EVCC_PLANNER_DATA_DIR", &c.DataDir)
	set("EVCC_PLANNER_FIXED_TIME", &c.FixedTime)
	set("EVCC_URL", &c.EVCC.URL)
	if v := getenv("EVCC_PLANNER_DEMO"); v != "" {
		demo, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("EVCC_PLANNER_DEMO: %w", err)
		}
		c.Demo = demo
	}
	if c.Routes.APIKeyEnv != "" {
		c.Routes.APIKey = strings.TrimSpace(getenv(c.Routes.APIKeyEnv))
	}
	if c.Calendar.URLEnv != "" {
		c.Calendar.Sources = CalendarSources(getenv(c.Calendar.URLEnv))
	}
	if c.OpenRouter.APIKeyEnv != "" {
		c.OpenRouter.APIKey = strings.TrimSpace(getenv(c.OpenRouter.APIKeyEnv))
	}
	if c.KNMI.APIKeyEnv != "" {
		c.KNMI.APIKey = strings.TrimSpace(getenv(c.KNMI.APIKeyEnv))
	}
	return nil
}

// CalendarSources splits a list of iCal URLs separated by commas or spaces.
func CalendarSources(v string) []CalendarSource {
	urls := strings.FieldsFunc(v, func(r rune) bool { return r == ',' || unicode.IsSpace(r) })
	var out []CalendarSource
	seen := map[string]bool{}
	for _, u := range urls {
		id := calendarID(u)
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, CalendarSource{ID: id, URL: u})
	}
	for i := range out {
		out[i].Label = "Calendar"
		if len(out) > 1 {
			out[i].Label = fmt.Sprintf("Calendar %d", i+1)
		}
	}
	return out
}

var secretSegment = regexp.MustCompile(`/private-[^/]+`)

// calendarID identifies a feed without its secret: Google's addresses keep
// the calendar ID when the secret is reset, so local edits survive.
func calendarID(raw string) string {
	u := raw
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i]
	}
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
	}
	u = strings.ToLower(secretSegment.ReplaceAllString(u, ""))
	sum := sha1.Sum([]byte(u))
	return hex.EncodeToString(sum[:6])
}

func (c *Config) finish() error {
	if c.Demo {
		c.EVCC.Mode, c.Calendar.Mode, c.Routes.Mode = ModeStub, ModeStub, ModeStub
		c.Tibber.Mode, c.OpenRouter.Mode, c.KNMI.Mode = ModeStub, ModeStub, ModeStub
	}
	loc, err := time.LoadLocation(c.Timezone)
	if err != nil {
		return fmt.Errorf("timezone %q: %w", c.Timezone, err)
	}
	c.Location = loc
	if c.FixedTime != "" {
		t, err := time.Parse(time.RFC3339, c.FixedTime)
		if err != nil {
			return fmt.Errorf("fixed_time must be RFC3339: %w", err)
		}
		c.Fixed = &t
	}

	switch {
	case c.Server.Listen == "":
		return errors.New("server.listen is required")
	case c.EVCC.Mode != ModeLive && c.EVCC.Mode != ModeStub:
		return fmt.Errorf("evcc.mode must be %q or %q", ModeLive, ModeStub)
	case c.Calendar.Mode != ModeICal && c.Calendar.Mode != ModeStub:
		return fmt.Errorf("calendar.mode must be %q or %q", ModeICal, ModeStub)
	case c.Routes.Mode != ModeGoogle && c.Routes.Mode != ModeStub:
		return fmt.Errorf("routes.mode must be %q or %q", ModeGoogle, ModeStub)
	case c.Tibber.Mode != ModeLive && c.Tibber.Mode != ModeStub:
		return fmt.Errorf("tibber.mode must be %q or %q", ModeLive, ModeStub)
	case c.OpenRouter.Mode != ModeLive && c.OpenRouter.Mode != ModeStub:
		return fmt.Errorf("openrouter.mode must be %q or %q", ModeLive, ModeStub)
	case c.KNMI.Mode != ModeLive && c.KNMI.Mode != ModeStub:
		return fmt.Errorf("knmi.mode must be %q or %q", ModeLive, ModeStub)
	case c.Forecast.RetryInterval < time.Minute:
		return errors.New("forecast.retry_interval must be at least 1m")
	case c.Forecast.VATPct < 0 || c.Forecast.VATPct > 50:
		return errors.New("forecast.vat_pct must be between 0 and 50")
	case c.EVCC.Loadpoint < 1:
		return errors.New("evcc.loadpoint starts at 1")
	case c.EVCC.PollInterval < time.Second:
		return errors.New("evcc.poll_interval must be at least 1s")
	case c.Calendar.SyncInterval < time.Minute:
		return errors.New("calendar.sync_interval must be at least 1m")
	case c.Retention.Days < 7:
		return errors.New("retention.days must be at least 7")
	case c.Routes.MonthlyFreeCalls < 0:
		return errors.New("routes.monthly_free_calls must not be negative")
	}
	if _, err := time.Parse("15:04", c.Forecast.DailyAt); err != nil {
		return fmt.Errorf("forecast.daily_at must be HH:MM: %w", err)
	}
	if err := c.Defaults.Settings().Validate(); err != nil {
		return fmt.Errorf("defaults: %w", err)
	}
	return nil
}

// Secrets lists configured values that must never be stored or shown: API
// keys and secret calendar addresses.
func (c *Config) Secrets() []string {
	out := []string{c.Routes.APIKey, c.OpenRouter.APIKey, c.KNMI.APIKey}
	for _, src := range c.Calendar.Sources {
		out = append(out, src.URL)
		if rest, ok := strings.CutPrefix(src.URL, "webcal://"); ok {
			out = append(out, "https://"+rest)
		}
	}
	return out
}

// DBPath is the SQLite file; demo mode uses its own database.
func (c *Config) DBPath() string {
	name := "planner.db"
	if c.Demo {
		name = "demo.db"
	}
	return filepath.Join(c.DataDir, name)
}

// ReadEnvFile parses KEY=VALUE lines. A missing file yields an empty map.
func ReadEnvFile(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read env file: %w", err)
	}
	env := map[string]string{}
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("%s:%d: expected KEY=VALUE", filepath.Base(path), i+1)
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		env[strings.TrimSpace(key)] = value
	}
	return env, nil
}
