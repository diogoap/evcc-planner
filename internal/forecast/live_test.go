//go:build live

package forecast

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/diogoap/evcc-planner/internal/config"
	"github.com/diogoap/evcc-planner/internal/model"
)

// TestLiveForecasts reads the real Tibber outlook and, with a key, KNMI's
// outlook; both are read-only. The OpenRouter extraction costs a little, so
// it only runs with LIVE_OPENROUTER=1 and a key.
func TestLiveForecasts(t *testing.T) {
	cfg, err := config.Load(config.Options{
		Path: "../../config.yaml", LocalPath: "../../config.local.yaml", EnvFile: "../../.env",
	})
	if err != nil {
		t.Fatal(err)
	}
	// The default connection settings (Settings → Connections is not read).
	conn := model.DefaultConnectionSettings()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	now := time.Now()

	page, err := HTTPArticle{PageURL: conn.TibberURL, HTTP: &http.Client{Timeout: 30 * time.Second}}.Fetch(ctx)
	if err != nil {
		t.Fatalf("tibber page: %v", err)
	}
	text, err := ArticleText(page)
	if err != nil {
		t.Fatal(err)
	}
	extractors := []Extractor{Pattern{}}
	if os.Getenv("LIVE_OPENROUTER") == "1" && cfg.OpenRouter.APIKey != "" {
		extractors = append(extractors, &OpenRouter{
			BaseURL: cfg.OpenRouter.BaseURL, APIKey: cfg.OpenRouter.APIKey, Model: conn.OpenRouterModel,
			HTTP: &http.Client{Timeout: conn.OpenRouterTimeout()},
		})
	}
	for _, x := range extractors {
		ex, usage, err := x.Extract(ctx, ExtractRequest{Text: text, URL: conn.TibberURL, FetchedAt: now, Location: cfg.Location})
		if err != nil {
			t.Fatalf("%s: %v", x.Label(), err)
		}
		o, err := Normalize(ex, NormalizeInput{Text: text, URL: conn.TibberURL, FetchedAt: now, Location: cfg.Location,
			VATPct: conn.ForecastVATPct, Extraction: x.Label()})
		if err != nil {
			t.Fatalf("%s: %v", x.Label(), err)
		}
		t.Logf("%s: report %s..%s, confidence %d, %d days, %d warnings, tokens %d+%d",
			x.Label(), o.ValidFrom, o.ValidTo, o.Confidence, len(o.Days), len(o.Warnings), usage.PromptTokens, usage.CompletionTokens)
		if o.Weekly.Price != nil {
			t.Logf("  week €%.3f/kWh incl. VAT (%s)", o.Weekly.Price.EURPerKWh, o.Weekly.Price.Provenance)
		}
		for _, d := range o.Days {
			if d.Price != nil {
				t.Logf("  %s €%.3f/kWh (%s) %s", d.Date, d.Price.EURPerKWh, d.Price.Provenance, d.Level)
			}
		}
		for _, w := range o.Warnings {
			t.Logf("  warning: %s", w)
		}
	}

	if cfg.KNMI.APIKey == "" {
		t.Log("KNMI_API_KEY not set: KNMI check skipped")
		return
	}
	k := &KNMI{BaseURL: cfg.KNMI.BaseURL, APIKey: cfg.KNMI.APIKey, Dataset: conn.KNMIDataset, Version: conn.KNMIVersion,
		HTTP: &http.Client{Timeout: 30 * time.Second}, Location: cfg.Location}
	w, err := k.Fetch(ctx)
	if err != nil {
		t.Fatalf("knmi: %v", err)
	}
	t.Logf("KNMI %s: %d days from %s, issued %v: %s", w.SourceFile, len(w.Days), w.Days[0].Date, w.IssuedAt, w.Summary)
}
