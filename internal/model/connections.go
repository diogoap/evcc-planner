package model

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// ConnectionSettings are the integration settings edited under Settings →
// Connections. Secrets stay in environment variables, as do the API base
// URLs that receive them; the Home Assistant address, which receives the
// token, is editable by decision. Keys are flat, prefixed by integration, so
// the activity log lists changed fields by name.
type ConnectionSettings struct {
	EVCCURL             string `json:"evccUrl"`
	EVCCLoadpoint       int    `json:"evccLoadpoint"` // 1-based
	EVCCVehicle         string `json:"evccVehicle"`   // empty: the loadpoint's or only vehicle
	EVCCPollIntervalSec int    `json:"evccPollIntervalSec"`
	EVCCTimeoutSec      int    `json:"evccTimeoutSec"`

	HAURL             string `json:"haUrl"`           // receives HA_TOKEN
	HASoCEntity       string `json:"haSocEntity"`     // battery level sensor (%)
	HARangeEntity     string `json:"haRangeEntity"`   // optional range sensor (km or mi)
	HAUpdatedEntity   string `json:"haUpdatedEntity"` // optional timestamp sensor: when the car last reported
	HAPollIntervalMin int    `json:"haPollIntervalMin"`
	HATimeoutSec      int    `json:"haTimeoutSec"`

	CalendarSyncIntervalMin int `json:"calendarSyncIntervalMin"`

	RoutesTrafficAware     bool `json:"routesTrafficAware"`
	RoutesCacheTTLHours    int  `json:"routesCacheTtlHours"`
	RoutesMonthlyFreeCalls int  `json:"routesMonthlyFreeCalls"` // shown next to the calls made; 0 hides it

	TibberURL string `json:"tibberUrl"`

	OpenRouterModel      string `json:"openRouterModel"` // must support strict JSON Schema output
	OpenRouterTimeoutSec int    `json:"openRouterTimeoutSec"`

	KNMIDataset string `json:"knmiDataset"`
	KNMIVersion string `json:"knmiVersion"`

	ForecastDailyAt          string  `json:"forecastDailyAt"` // local HH:MM
	ForecastRetryIntervalMin int     `json:"forecastRetryIntervalMin"`
	ForecastVATPct           float64 `json:"forecastVatPct"`

	RetentionDays int `json:"retentionDays"`
}

// DefaultConnectionSettings are the built-in values; deployment seeds (such
// as EVCC_URL) are applied on top by the config.
func DefaultConnectionSettings() ConnectionSettings {
	return ConnectionSettings{
		EVCCURL: "http://127.0.0.1:7070", EVCCLoadpoint: 1, EVCCPollIntervalSec: 60, EVCCTimeoutSec: 10,
		HAURL: "http://127.0.0.1:8123", HAPollIntervalMin: 10, HATimeoutSec: 10,
		CalendarSyncIntervalMin: 60,
		RoutesTrafficAware:      true, RoutesCacheTTLHours: 168, RoutesMonthlyFreeCalls: 5000,
		TibberURL:       "https://tibber.com/nl/stroomprijsprognose",
		OpenRouterModel: "anthropic/claude-haiku-5.5", OpenRouterTimeoutSec: 60,
		KNMIDataset: "outlook_weather_forecast", KNMIVersion: "1.0",
		ForecastDailyAt: "18:00", ForecastRetryIntervalMin: 60, ForecastVATPct: 21,
		RetentionDays: 90,
	}
}

// EntityID matches Home Assistant entity IDs: domain.object_id.
var EntityID = regexp.MustCompile(`^[a-z0-9_]+\.[a-z0-9_]+$`)

// Validate reports the first invalid field.
func (c ConnectionSettings) Validate() error {
	for _, u := range []struct{ name, v string }{
		{"EVCC address", c.EVCCURL}, {"Home Assistant address", c.HAURL}, {"Tibber outlook page", c.TibberURL},
	} {
		if err := checkURL(u.v); err != nil {
			return fmt.Errorf("%s: %w", u.name, err)
		}
	}
	for _, e := range []struct{ name, v string }{
		{"SoC sensor", c.HASoCEntity}, {"range sensor", c.HARangeEntity}, {"reading-time sensor", c.HAUpdatedEntity},
	} {
		if e.v != "" && !EntityID.MatchString(e.v) {
			return fmt.Errorf("%s must be an entity ID such as sensor.leaf_battery_level", e.name)
		}
	}
	ranges := []struct {
		name     string
		v, lo, h float64
	}{
		{"EVCC loadpoint", float64(c.EVCCLoadpoint), 1, 20},
		{"EVCC poll interval", float64(c.EVCCPollIntervalSec), 5, 3600},
		{"EVCC timeout", float64(c.EVCCTimeoutSec), 1, 120},
		{"Home Assistant poll interval", float64(c.HAPollIntervalMin), 1, 24 * 60},
		{"Home Assistant timeout", float64(c.HATimeoutSec), 1, 120},
		{"calendar sync interval", float64(c.CalendarSyncIntervalMin), 1, 24 * 60},
		{"route cache", float64(c.RoutesCacheTTLHours), 1, 24 * 90},
		{"monthly free route calls", float64(c.RoutesMonthlyFreeCalls), 0, 1_000_000},
		{"OpenRouter timeout", float64(c.OpenRouterTimeoutSec), 5, 600},
		{"forecast retry interval", float64(c.ForecastRetryIntervalMin), 1, 24 * 60},
		{"VAT", c.ForecastVATPct, 0, 50},
		{"history retention", float64(c.RetentionDays), 7, 3650},
	}
	for _, r := range ranges {
		if r.v < r.lo || r.v > r.h {
			return fmt.Errorf("%s must be between %g and %g", r.name, r.lo, r.h)
		}
	}
	switch {
	case strings.TrimSpace(c.OpenRouterModel) == "":
		return fmt.Errorf("OpenRouter model is required")
	case strings.TrimSpace(c.KNMIDataset) == "" || strings.TrimSpace(c.KNMIVersion) == "":
		return fmt.Errorf("KNMI dataset and version are required")
	}
	if _, err := time.Parse("15:04", c.ForecastDailyAt); err != nil {
		return fmt.Errorf("daily forecast time must be HH:MM")
	}
	return nil
}

// checkURL accepts http(s) addresses without login details or a query, so
// nothing secret ends up in settings or the activity log.
func checkURL(raw string) error {
	u, err := url.Parse(raw)
	switch {
	case err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "":
		return fmt.Errorf("%q is not an http(s) address", raw)
	case u.User != nil:
		return fmt.Errorf("must not contain a user name or password")
	case u.RawQuery != "" || u.Fragment != "":
		return fmt.Errorf("must not contain a query")
	}
	return nil
}

// Durations of the interval and timeout fields.
func (c ConnectionSettings) EVCCPollInterval() time.Duration { return seconds(c.EVCCPollIntervalSec) }
func (c ConnectionSettings) EVCCTimeout() time.Duration      { return seconds(c.EVCCTimeoutSec) }
func (c ConnectionSettings) HAPollInterval() time.Duration   { return minutes(c.HAPollIntervalMin) }
func (c ConnectionSettings) HATimeout() time.Duration        { return seconds(c.HATimeoutSec) }
func (c ConnectionSettings) CalendarSyncInterval() time.Duration {
	return minutes(c.CalendarSyncIntervalMin)
}
func (c ConnectionSettings) RoutesCacheTTL() time.Duration {
	return time.Duration(c.RoutesCacheTTLHours) * time.Hour
}
func (c ConnectionSettings) OpenRouterTimeout() time.Duration { return seconds(c.OpenRouterTimeoutSec) }
func (c ConnectionSettings) ForecastRetryInterval() time.Duration {
	return minutes(c.ForecastRetryIntervalMin)
}

func seconds(n int) time.Duration { return time.Duration(n) * time.Second }
func minutes(n int) time.Duration { return time.Duration(n) * time.Minute }
