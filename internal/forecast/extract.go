package forecast

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/diogoap/evcc-planner/internal/localtime"
	"github.com/diogoap/evcc-planner/internal/openrouter"
)

// ExtractRequest is an article to structure.
type ExtractRequest struct {
	Text      string
	URL       string
	FetchedAt time.Time
	Location  *time.Location
}

// Usage reports model tokens for cost tracking.
type Usage = openrouter.Usage

// Extractor structures article text into an Extraction.
type Extractor interface {
	Name() string  // openrouter or pattern
	Label() string // stored with the payload and shown in the UI
	Extract(ctx context.Context, req ExtractRequest) (Extraction, Usage, error)
}

// ---- pattern extractor (stub inference) ----

// Pattern is the labelled stand-in used without an OpenRouter key: it reads
// only the article's fixed summary lines (report period, weekly average,
// dearest and cheapest day) with regular expressions, so report dates and
// stated prices are kept and nothing is invented.
type Pattern struct{}

// Name identifies the extractor.
func (Pattern) Name() string { return "pattern" }

// Label describes the extractor.
func (Pattern) Label() string { return "Pattern-based stub (OpenRouter not configured)" }

var (
	weeklyRe  = regexp.MustCompile(`(?i)weekgemiddelde:?\s*€\s?([\d.,]+)\s*/\s*kwh\s*excl\.?\s*\(\s*€\s?([\d.,]+)\s*/\s*kwh\s*incl\.?\s*\)`)
	extremeRe = regexp.MustCompile(`(?i)(duurste|goedkoopste) dag:?\s*(maandag|dinsdag|woensdag|donderdag|vrijdag|zaterdag|zondag)\s*\(\s*~?\s*€\s?(-?[\d.,]+)\s*/\s*mwh\s*excl\.?\s*\)`)
	trendRe   = regexp.MustCompile(`(?i)prijzen:\s*(hoger|lager|gelijk|stabiel)`)
	weekdays  = map[string]int{"maandag": 1, "dinsdag": 2, "woensdag": 3, "donderdag": 4, "vrijdag": 5, "zaterdag": 6, "zondag": 7}
)

// Extract reads the fixed lines.
func (Pattern) Extract(ctx context.Context, req ExtractRequest) (Extraction, Usage, error) {
	ex := Extraction{Weekly: ExtractedWeekly{Trend: "unknown", Prices: []StatedPrice{}}, Days: []ExtractedDay{}}
	from, to, ok := HeadingPeriod(req.Text, req.FetchedAt, req.Location)
	if ok {
		ex.Period = ExtractedPeriod{From: &from, To: &to}
	}
	lines := strings.Split(req.Text, "\n")
	if len(lines) > 1 {
		ex.Weekly.Summary = "Article intro (not translated): " + lines[1]
	}
	if m := trendRe.FindStringSubmatch(req.Text); m != nil {
		ex.Weekly.Trend = map[string]string{"hoger": "rising", "lager": "falling", "gelijk": "stable", "stabiel": "stable"}[strings.ToLower(m[1])]
	}
	if m := weeklyRe.FindStringSubmatch(req.Text); m != nil {
		excl, _ := dutchNumber(m[1])
		incl, _ := dutchNumber(m[2])
		ex.Weekly.Prices = append(ex.Weekly.Prices,
			StatedPrice{Value: excl, Unit: "EUR/kWh", Basis: "excl", Quote: m[0]},
			StatedPrice{Value: incl, Unit: "EUR/kWh", Basis: "incl", Quote: m[0]})
		ex.SupportingText = m[0]
	}
	if ok {
		start, _ := time.ParseInLocation(localtime.DateLayout, from, req.Location)
		for _, m := range extremeRe.FindAllStringSubmatch(req.Text, -1) {
			v, err := dutchNumber(m[3])
			if err != nil {
				continue
			}
			date := localtime.AddDays(start, (weekdays[strings.ToLower(m[2])]-localtime.ISOWeekday(start)+7)%7)
			level := "cheapest"
			if strings.EqualFold(m[1], "duurste") {
				level = "most_expensive"
			}
			ex.Days = append(ex.Days, ExtractedDay{
				Date: date.Format(localtime.DateLayout), Level: level, Wind: "unknown", Solar: "unknown",
				Prices: []StatedPrice{{Value: v, Unit: "EUR/MWh", Basis: "excl", Quote: m[0]}},
				Note:   fmt.Sprintf("Stated as the %s day of the week.", strings.ReplaceAll(level, "_", " ")),
			})
			ex.SupportingText = strings.TrimSpace(ex.SupportingText + " " + m[0])
		}
	}
	return ex, Usage{}, nil
}

func dutchNumber(s string) (float64, error) {
	s = strings.TrimRight(s, ".,")
	return strconv.ParseFloat(strings.ReplaceAll(s, ",", "."), 64)
}

// ---- OpenRouter ----

// maxOutputTokens caps the answer (a full extraction is about 1,000 tokens).
const maxOutputTokens = 4000

// OpenRouter structures the article with a chat completions model through
// OpenRouter, with strict JSON Schema output.
type OpenRouter struct {
	BaseURL string
	APIKey  string
	Model   string
	HTTP    *http.Client
}

// Name identifies the extractor.
func (*OpenRouter) Name() string { return "openrouter" }

// Label describes the extractor.
func (o *OpenRouter) Label() string { return "OpenRouter " + o.Model }

// Extract asks the model for the stated facts. It sends only the article
// text cut out of the page (no URL, tools, or web access), which keeps the
// request small and works with any model.
func (o *OpenRouter) Extract(ctx context.Context, req ExtractRequest) (Extraction, Usage, error) {
	user := fmt.Sprintf("Fetch date: %s (Europe/Amsterdam).\n\nArticle:\n<<<\n%s\n>>>",
		localtime.Date(req.FetchedAt, req.Location), req.Text)
	c := &openrouter.Client{BaseURL: o.BaseURL, APIKey: o.APIKey, HTTP: o.HTTP}
	content, usage, err := c.Complete(ctx, openrouter.Request{
		Model: o.Model, System: ExtractionPrompt, User: user, SchemaName: "tibber_outlook", Schema: ExtractionSchema(),
		MaxTokens: maxOutputTokens, Where: "Settings → Connections",
	})
	if err != nil {
		return Extraction{}, usage, err
	}
	var ex Extraction
	dec := json.NewDecoder(strings.NewReader(content))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&ex); err != nil {
		return Extraction{}, usage, fmt.Errorf("model output does not match the schema: %w", err)
	}
	return ex, usage, nil
}
