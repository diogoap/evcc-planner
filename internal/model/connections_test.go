package model

import (
	"strings"
	"testing"
	"time"
)

func TestConnectionSettingsValidate(t *testing.T) {
	d := DefaultConnectionSettings()
	if err := d.Validate(); err != nil {
		t.Fatalf("defaults must be valid: %v", err)
	}
	if d.EVCCPollInterval() != time.Minute || d.HAPollInterval() != 10*time.Minute || d.RoutesCacheTTL() != 168*time.Hour {
		t.Errorf("durations = %v %v %v", d.EVCCPollInterval(), d.HAPollInterval(), d.RoutesCacheTTL())
	}
	cases := map[string]struct {
		edit func(*ConnectionSettings)
		want string
	}{
		"evcc scheme":     {func(c *ConnectionSettings) { c.EVCCURL = "evcc.local:7070" }, "not an http(s) address"},
		"evcc password":   {func(c *ConnectionSettings) { c.EVCCURL = "http://user:pw@evcc:7070" }, "user name or password"},
		"ha address":      {func(c *ConnectionSettings) { c.HAURL = "ha.local" }, "Home Assistant address"},
		"tibber query":    {func(c *ConnectionSettings) { c.TibberURL = "https://tibber.com/x?token=1" }, "must not contain a query"},
		"entity":          {func(c *ConnectionSettings) { c.HASoCEntity = "Leaf battery" }, "SoC sensor must be an entity ID"},
		"range entity":    {func(c *ConnectionSettings) { c.HARangeEntity = "leaf_range" }, "range sensor must be an entity ID"},
		"updated entity":  {func(c *ConnectionSettings) { c.HAUpdatedEntity = "updated_on" }, "reading-time sensor must be an entity ID"},
		"loadpoint":       {func(c *ConnectionSettings) { c.EVCCLoadpoint = 0 }, "EVCC loadpoint must be between 1 and 20"},
		"evcc poll":       {func(c *ConnectionSettings) { c.EVCCPollIntervalSec = 1 }, "EVCC poll interval"},
		"ha poll":         {func(c *ConnectionSettings) { c.HAPollIntervalMin = 0 }, "Home Assistant poll interval"},
		"calendar":        {func(c *ConnectionSettings) { c.CalendarSyncIntervalMin = 0 }, "calendar sync interval"},
		"free calls":      {func(c *ConnectionSettings) { c.RoutesMonthlyFreeCalls = -1 }, "monthly free route calls"},
		"vat":             {func(c *ConnectionSettings) { c.ForecastVATPct = 90 }, "VAT must be between 0 and 50"},
		"retention":       {func(c *ConnectionSettings) { c.RetentionDays = 3 }, "history retention must be between 7"},
		"daily time":      {func(c *ConnectionSettings) { c.ForecastDailyAt = "noon" }, "HH:MM"},
		"model":           {func(c *ConnectionSettings) { c.OpenRouterModel = " " }, "model is required"},
		"knmi dataset":    {func(c *ConnectionSettings) { c.KNMIDataset = "" }, "KNMI dataset and version"},
		"openrouter time": {func(c *ConnectionSettings) { c.OpenRouterTimeoutSec = 0 }, "OpenRouter timeout"},
	}
	for name, c := range cases {
		v := DefaultConnectionSettings()
		c.edit(&v)
		if err := v.Validate(); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", name, err, c.want)
		}
	}
	ok := DefaultConnectionSettings()
	ok.EVCCURL, ok.HASoCEntity, ok.HARangeEntity = "https://evcc.example:7070/", "sensor.leaf_battery_level", "sensor.leaf_range"
	if err := ok.Validate(); err != nil {
		t.Errorf("valid settings rejected: %v", err)
	}
}
