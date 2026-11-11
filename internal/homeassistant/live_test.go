//go:build live

package homeassistant

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/diogoap/evcc-planner/internal/config"
)

// TestLiveHomeAssistant lists the real Home Assistant's battery and range
// sensors (HA_URL, HA_TOKEN from the environment or .env; Settings →
// Connections is not read) and reads each
// battery sensor as the planner would. It only reads. The car's sensor is
// chosen under Settings → Connections.
func TestLiveHomeAssistant(t *testing.T) {
	cfg, err := config.Load(config.Options{
		Path: "../../config.yaml", LocalPath: "../../config.local.yaml", EnvFile: "../../.env",
	})
	if err != nil {
		t.Fatal(err)
	}
	ha := cfg.HomeAssistant
	if ha.Mode != config.ModeLive {
		t.Skip("homeassistant.mode is not live")
	}
	if ha.Token == "" {
		t.Skipf("set %s to a long-lived access token (and HA_URL), e.g. in .env", ha.TokenEnv)
	}
	conn := cfg.ConnectionDefaults()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := &HTTPClient{BaseURL: conn.HAURL, Token: ha.Token, HTTP: &http.Client{Timeout: conn.HATimeout()}, Location: cfg.Location}

	states, err := c.States(ctx)
	if err != nil {
		t.Fatalf("list %s/api/states: %v", conn.HAURL, err)
	}
	byID := map[string]EntityState{}
	for _, e := range states {
		byID[e.EntityID] = e
	}
	sensors := Candidates(states)
	for _, sn := range sensors {
		e := byID[sn.EntityID]
		line := fmt.Sprintf("%-8s %s = %s %s (%s)", sn.Kind, sn.EntityID, sn.State, sn.Unit, sn.Name)
		if sn.Kind == KindBattery {
			if r, err := ParseSoC(e, "", cfg.Location); err != nil {
				line += " · not usable: " + err.Error()
			} else {
				line += fmt.Sprintf(" · reading %.1f%% at %s (%s ago)", r.Percent,
					r.ObservedAt.In(cfg.Location).Format(time.DateTime), time.Since(r.ObservedAt).Round(time.Minute))
			}
			// Attributes that hold a time could be the car's own reading
			// time (the reading-time attribute in Settings → Connections).
			for name, v := range e.Attributes {
				if ts, err := parseTime(v, cfg.Location); err == nil {
					line += fmt.Sprintf(" · attribute %s = %s", name, ts.In(cfg.Location).Format(time.DateTime))
				}
			}
		}
		t.Log(line)
	}
	if len(sensors) == 0 {
		t.Error("no battery (%) or distance sensors found")
	}
}
