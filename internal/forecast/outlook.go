// Package forecast ingests daily forecast inputs for charging advice: the
// Tibber weekly price outlook (article text structured by a language model
// into a fixed, validated payload) and KNMI's multi-day weather outlook.
//
// The model only reports values the article states, each with the exact
// quote; the server verifies every quote against the article and does all
// unit and VAT conversions itself, so stored prices carry their provenance.
package forecast

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/diogoap/evcc-planner/internal/localtime"
)

// OutlookSchemaVersion identifies the stored outlook payload contract.
const OutlookSchemaVersion = "tibber-outlook/1"

// Outlook is the validated weekly price outlook. Prices are retail EUR/kWh
// including VAT; values the article does not state stay nil.
type Outlook struct {
	SchemaVersion  string     `json:"schemaVersion"`
	SourceURL      string     `json:"sourceUrl"`
	FetchedAt      time.Time  `json:"fetchedAt"`
	IssuedAt       *time.Time `json:"issuedAt"` // publication time, when the article states it
	ValidFrom      string     `json:"validFrom"`
	ValidTo        string     `json:"validTo"`
	PeriodSource   string     `json:"periodSource"` // heading (parsed from the article) or model
	Extraction     string     `json:"extraction"`   // how the text was structured
	Weekly         Weekly     `json:"weekly"`
	Days           []Day      `json:"days"`
	Confidence     int        `json:"confidence"` // 1 low, 2 medium, 3 high: how much the article quantifies
	SupportingText string     `json:"supportingText"`
	Warnings       []string   `json:"warnings"`
	TextHash       string     `json:"textHash"`
}

// Weekly is the outlook for the whole report period.
type Weekly struct {
	Summary       string   `json:"summary"`
	Trend         string   `json:"trend"` // vs last week: rising, falling, stable, mixed, unknown
	Price         *Price   `json:"price"` // weekly average
	WindGW        *float64 `json:"windGW"`
	WindNormalGW  *float64 `json:"windNormalGW"`
	SolarGW       *float64 `json:"solarGW"`
	SolarNormalGW *float64 `json:"solarNormalGW"`
	TempC         *float64 `json:"tempC"`
}

// Day is one dated estimate or trend.
type Day struct {
	Date    string   `json:"date"`
	Price   *Price   `json:"price"`
	Level   string   `json:"level"` // cheapest, cheap, average, expensive, most_expensive, unknown
	Wind    string   `json:"wind"`  // low, normal, high, unknown (market wind)
	WindGW  *float64 `json:"windGW"`
	Solar   string   `json:"solar"`
	SolarGW *float64 `json:"solarGW"`
	Note    string   `json:"note"`
}

// Price is a retail price in EUR/kWh including VAT, with how it was obtained.
type Price struct {
	EURPerKWh  float64 `json:"eurPerKWh"`
	Provenance string  `json:"provenance"` // stated or converted
	Detail     string  `json:"detail"`
	Quote      string  `json:"quote"`
}

// Day returns the outlook for a date, if the article covers it.
func (o *Outlook) Day(date string) *Day {
	for i := range o.Days {
		if o.Days[i].Date == date {
			return &o.Days[i]
		}
	}
	return nil
}

// Covers reports whether the date is in the report period.
func (o *Outlook) Covers(date string) bool { return date >= o.ValidFrom && date <= o.ValidTo }

// ---- model output ----

// Extraction is what the model (or the pattern extractor) returns: stated
// facts only, with quotes. It is validated and converted by Normalize.
type Extraction struct {
	Period         ExtractedPeriod `json:"period"`
	Issued         *string         `json:"issued"`
	Weekly         ExtractedWeekly `json:"weekly"`
	Days           []ExtractedDay  `json:"days"`
	SupportingText string          `json:"supporting_text"`
}

// ExtractedPeriod is the report period as ISO dates.
type ExtractedPeriod struct {
	From *string `json:"from"`
	To   *string `json:"to"`
}

// ExtractedWeekly holds week-level statements.
type ExtractedWeekly struct {
	Summary         string        `json:"summary"`
	Trend           string        `json:"trend_vs_last_week"`
	Prices          []StatedPrice `json:"prices"`
	WindAvgGW       *float64      `json:"wind_avg_gw"`
	WindNormalGW    *float64      `json:"wind_normal_gw"`
	SolarAvgGW      *float64      `json:"solar_avg_gw"`
	SolarNormalGW   *float64      `json:"solar_normal_gw"`
	TemperatureAvgC *float64      `json:"temperature_avg_c"`
}

// ExtractedDay holds statements about one date.
type ExtractedDay struct {
	Date    string        `json:"date"`
	Prices  []StatedPrice `json:"prices"`
	Level   string        `json:"level"`
	Wind    string        `json:"wind"`
	WindGW  *float64      `json:"wind_gw"`
	Solar   string        `json:"solar"`
	SolarGW *float64      `json:"solar_gw"`
	Note    string        `json:"note"`
}

// StatedPrice is a price exactly as the article states it.
type StatedPrice struct {
	Value float64 `json:"value"`
	Unit  string  `json:"unit"`  // EUR/kWh, EUR/MWh, ct/kWh
	Basis string  `json:"basis"` // incl (taxes and VAT), excl, unknown
	Quote string  `json:"quote"`
}

var (
	levels = []string{"cheapest", "cheap", "average", "expensive", "most_expensive", "unknown"}
	winds  = []string{"low", "normal", "high", "unknown"}
	trends = []string{"rising", "falling", "stable", "mixed", "unknown"}
	units  = []string{"EUR/kWh", "EUR/MWh", "ct/kWh"}
	bases  = []string{"incl", "excl", "unknown"}
)

func strEnum(values []string, desc string) map[string]any {
	return map[string]any{"type": "string", "enum": values, "description": desc}
}

func nullNumber(desc string) map[string]any {
	return map[string]any{"type": []string{"number", "null"}, "description": desc}
}

func object(props map[string]any) map[string]any {
	required := make([]string, 0, len(props))
	for k := range props {
		required = append(required, k)
	}
	sort.Strings(required)
	return map[string]any{"type": "object", "additionalProperties": false, "properties": props, "required": required}
}

// ExtractionSchema is the strict JSON Schema the model must follow.
func ExtractionSchema() map[string]any {
	price := object(map[string]any{
		"value": map[string]any{"type": "number", "description": "The number exactly as written (decimal comma becomes a dot)."},
		"unit":  strEnum(units, "Unit as written."),
		"basis": strEnum(bases, "incl = including taxes/VAT, excl = excluding, unknown if not stated."),
		"quote": map[string]any{"type": "string", "description": "Exact text fragment from the article that states this value."},
	})
	prices := map[string]any{"type": "array", "items": price}
	nullDate := map[string]any{"type": []string{"string", "null"}, "description": "YYYY-MM-DD, or null if not stated."}
	return object(map[string]any{
		"period": object(map[string]any{"from": nullDate, "to": nullDate}),
		"issued": map[string]any{
			"type": []string{"string", "null"}, "description": "Publication date/time if the article states it (ISO 8601), else null.",
		},
		"weekly": object(map[string]any{
			"summary":            map[string]any{"type": "string", "description": "One-sentence summary of the outlook, in English."},
			"trend_vs_last_week": strEnum(trends, "Price direction compared with last week, as stated."),
			"prices":             prices,
			"wind_avg_gw":        nullNumber("Stated average wind generation for the week (GW)."),
			"wind_normal_gw":     nullNumber("Stated normal/seasonal wind generation (GW)."),
			"solar_avg_gw":       nullNumber("Stated average solar generation for the week (GW)."),
			"solar_normal_gw":    nullNumber("Stated normal/seasonal solar generation (GW)."),
			"temperature_avg_c":  nullNumber("Stated average temperature (°C)."),
		}),
		"days": map[string]any{"type": "array", "items": object(map[string]any{
			"date":     map[string]any{"type": "string", "description": "YYYY-MM-DD of a day the article mentions."},
			"prices":   prices,
			"level":    strEnum(levels, "Stated relative price level of the day."),
			"wind":     strEnum(winds, "Stated wind generation level of the day."),
			"wind_gw":  nullNumber("Stated wind generation for the day (GW)."),
			"solar":    strEnum(winds, "Stated solar generation level of the day."),
			"solar_gw": nullNumber("Stated solar generation for the day (GW)."),
			"note":     map[string]any{"type": "string", "description": "Short English paraphrase of what the article says about this day."},
		})},
		"supporting_text": map[string]any{"type": "string", "description": "Up to three short quotes from the article that support the outlook."},
	})
}

// ExtractionPrompt is the system prompt for the model.
const ExtractionPrompt = `You extract facts from a Dutch weekly electricity price outlook article.
Rules:
- Report only values the article states. Never calculate, convert, average, or estimate numbers.
- Every price needs the exact quote from the article that contains it; copy the number as written (decimal comma becomes a dot in "value").
- If the article does not state something, use null, "unknown", or an empty list.
- Dates are YYYY-MM-DD. The article omits the year: use the year that puts the report period closest to the fetch date.
- Weekday names refer to dates inside the report period.
- Write summary and notes in English.`

// ---- normalization ----

// NormalizeInput is the context Normalize needs besides the extraction.
type NormalizeInput struct {
	Text       string
	URL        string
	FetchedAt  time.Time
	Location   *time.Location
	VATPct     float64
	Extraction string // label stored with the payload
}

// Normalize validates an extraction against the article text and converts
// prices to retail EUR/kWh including VAT. It rejects payloads without a
// plausible report period; other problems drop the value with a warning.
func Normalize(ex Extraction, in NormalizeInput) (*Outlook, error) {
	o := &Outlook{
		SchemaVersion: OutlookSchemaVersion, SourceURL: in.URL, FetchedAt: in.FetchedAt, Extraction: in.Extraction,
		Days: []Day{}, Warnings: []string{}, TextHash: TextHash(in.Text),
	}
	fetchDate := localtime.Date(in.FetchedAt, in.Location)

	// Report period: the article heading is authoritative; the model's
	// reading is only used when the heading cannot be parsed.
	if from, to, ok := HeadingPeriod(in.Text, in.FetchedAt, in.Location); ok {
		o.ValidFrom, o.ValidTo, o.PeriodSource = from, to, "heading"
		if ex.Period.From != nil && ex.Period.To != nil && (*ex.Period.From != from || *ex.Period.To != to) {
			o.warn("model read the period as %s to %s; using the article heading", *ex.Period.From, *ex.Period.To)
		}
	} else if ex.Period.From != nil && ex.Period.To != nil {
		o.ValidFrom, o.ValidTo, o.PeriodSource = *ex.Period.From, *ex.Period.To, "model"
	}
	if err := checkPeriod(o.ValidFrom, o.ValidTo, fetchDate); err != nil {
		return nil, err
	}

	if ex.Issued != nil && strings.TrimSpace(*ex.Issued) != "" {
		if t, ok := parseIssued(*ex.Issued, in.Location); ok && !t.After(in.FetchedAt.Add(24*time.Hour)) {
			o.IssuedAt = &t
		} else {
			o.warn("ignored publication time %q", *ex.Issued)
		}
	}

	text := normalizeText(in.Text)
	o.Weekly = Weekly{
		Summary: clip(ex.Weekly.Summary, 300), Trend: enum(ex.Weekly.Trend, trends),
		WindGW: o.gw(ex.Weekly.WindAvgGW, "weekly wind"), WindNormalGW: o.gw(ex.Weekly.WindNormalGW, "normal wind"),
		SolarGW: o.gw(ex.Weekly.SolarAvgGW, "weekly solar"), SolarNormalGW: o.gw(ex.Weekly.SolarNormalGW, "normal solar"),
		TempC: o.bounded(ex.Weekly.TemperatureAvgC, -30, 45, "temperature"),
	}
	var weeklyIncl, weeklyExcl *StatedPrice
	for _, p := range o.verified(ex.Weekly.Prices, text, "weekly") {
		switch p.Basis {
		case "incl":
			if weeklyIncl == nil {
				weeklyIncl = &p
			}
		case "excl":
			if weeklyExcl == nil {
				weeklyExcl = &p
			}
		}
	}
	conv := converter{vat: in.VATPct}
	if weeklyIncl != nil {
		o.Weekly.Price = o.retail(&Price{
			EURPerKWh: perKWh(*weeklyIncl), Provenance: "stated", Detail: "Weekly average as stated, incl. taxes and VAT",
			Quote: weeklyIncl.Quote,
		}, "weekly average")
		if weeklyExcl != nil {
			conv.incl, conv.excl, conv.ok = perKWh(*weeklyIncl), perKWh(*weeklyExcl), true
		}
	}

	seen := map[string]bool{}
	for _, d := range ex.Days {
		if _, err := time.Parse(localtime.DateLayout, d.Date); err != nil || d.Date < o.ValidFrom || d.Date > o.ValidTo {
			o.warn("dropped day %q outside the report period", d.Date)
			continue
		}
		if seen[d.Date] {
			o.warn("dropped duplicate day %s", d.Date)
			continue
		}
		seen[d.Date] = true
		day := Day{
			Date: d.Date, Level: enum(d.Level, levels), Wind: enum(d.Wind, winds), Solar: enum(d.Solar, winds),
			WindGW: o.gw(d.WindGW, d.Date+" wind"), SolarGW: o.gw(d.SolarGW, d.Date+" solar"), Note: clip(d.Note, 240),
		}
		day.Price = o.dayPrice(o.verified(d.Prices, text, d.Date), conv, d.Date)
		o.Days = append(o.Days, day)
	}
	sort.Slice(o.Days, func(i, j int) bool { return o.Days[i].Date < o.Days[j].Date })
	o.SupportingText = clip(ex.SupportingText, 600)

	priced := 0
	for _, d := range o.Days {
		if d.Price != nil {
			priced++
		}
	}
	switch {
	case o.Weekly.Price != nil && priced >= 3:
		o.Confidence = 3
	case o.Weekly.Price != nil || priced > 0:
		o.Confidence = 2
	default:
		o.Confidence = 1
	}
	return o, nil
}

type converter struct {
	ok         bool
	incl, excl float64 // weekly averages, EUR/kWh
	vat        float64
}

// dayPrice prefers a stated incl. price; an excl. market price is converted
// with the article's own weekly incl./excl. pair: retail prices differ from
// market prices by fixed per-kWh taxes and fees plus VAT, so a market price
// difference becomes a retail difference times (1 + VAT).
func (o *Outlook) dayPrice(prices []StatedPrice, conv converter, date string) *Price {
	for _, p := range prices {
		if p.Basis == "incl" {
			return o.retail(&Price{EURPerKWh: perKWh(p), Provenance: "stated", Detail: "Stated incl. taxes and VAT", Quote: p.Quote}, date)
		}
	}
	for _, p := range prices {
		if p.Basis != "excl" {
			continue
		}
		if !conv.ok {
			o.warn("%s: %s excl. not converted: the article states no weekly incl./excl. pair", date, fmtStated(p))
			return nil
		}
		factor := 1 + conv.vat/100
		v := conv.incl + (perKWh(p)-conv.excl)*factor
		return o.retail(&Price{
			EURPerKWh: v, Provenance: "converted", Quote: p.Quote,
			Detail: fmt.Sprintf("From %s excl.: weekly average €%.3f excl. = €%.3f incl.; the difference × %.2f (VAT)",
				fmtStated(p), conv.excl, conv.incl, factor),
		}, date)
	}
	return nil
}

// retail rejects implausible retail prices.
func (o *Outlook) retail(p *Price, what string) *Price {
	if p.EURPerKWh < -0.5 || p.EURPerKWh > 2 || math.IsNaN(p.EURPerKWh) {
		o.warn("%s: dropped implausible retail price €%.3f/kWh", what, p.EURPerKWh)
		return nil
	}
	p.EURPerKWh = math.Round(p.EURPerKWh*10000) / 10000
	return p
}

// verified keeps prices whose quote appears in the article and contains the
// value.
func (o *Outlook) verified(prices []StatedPrice, text, what string) []StatedPrice {
	var out []StatedPrice
	for _, p := range prices {
		p.Unit, p.Basis = enum(p.Unit, units), enum(p.Basis, bases)
		q := normalizeText(p.Quote)
		switch {
		case p.Unit == "unknown":
			o.warn("%s: dropped price with unknown unit", what)
		case q == "" || !strings.Contains(text, q):
			o.warn("%s: dropped %s: quote not found in the article", what, fmtStated(p))
		case !quoteHasNumber(q, p.Value):
			o.warn("%s: dropped %s: the quote does not contain that number", what, fmtStated(p))
		default:
			p.Quote = clip(strings.TrimSpace(p.Quote), 200)
			out = append(out, p)
		}
	}
	return out
}

func (o *Outlook) gw(v *float64, what string) *float64 { return o.bounded(v, 0, 60, what+" (GW)") }

func (o *Outlook) bounded(v *float64, lo, hi float64, what string) *float64 {
	if v == nil {
		return nil
	}
	if *v < lo || *v > hi || math.IsNaN(*v) {
		o.warn("dropped implausible %s %g", what, *v)
		return nil
	}
	x := *v
	return &x
}

func (o *Outlook) warn(format string, args ...any) {
	o.Warnings = append(o.Warnings, fmt.Sprintf(format, args...))
}

func checkPeriod(from, to, fetchDate string) error {
	f, err1 := time.Parse(localtime.DateLayout, from)
	t, err2 := time.Parse(localtime.DateLayout, to)
	fd, _ := time.Parse(localtime.DateLayout, fetchDate)
	switch {
	case from == "" || to == "":
		return errors.New("the article states no report period")
	case err1 != nil || err2 != nil:
		return fmt.Errorf("invalid report period %q to %q", from, to)
	case t.Before(f) || t.Sub(f) > 14*24*time.Hour:
		return fmt.Errorf("implausible report period %s to %s", from, to)
	case t.Before(fd.AddDate(0, 0, -14)) || f.After(fd.AddDate(0, 0, 14)):
		return fmt.Errorf("report period %s to %s is far from the fetch date %s", from, to, fetchDate)
	}
	return nil
}

func parseIssued(s string, loc *time.Location) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, true
	}
	for _, layout := range []string{"2006-01-02T15:04", "2006-01-02 15:04", localtime.DateLayout} {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func perKWh(p StatedPrice) float64 {
	switch p.Unit {
	case "EUR/MWh":
		return p.Value / 1000
	case "ct/kWh":
		return p.Value / 100
	}
	return p.Value
}

func fmtStated(p StatedPrice) string {
	switch p.Unit {
	case "EUR/MWh":
		return fmt.Sprintf("€%g/MWh", p.Value)
	case "ct/kWh":
		return fmt.Sprintf("%g ct/kWh", p.Value)
	}
	return fmt.Sprintf("€%g/kWh", p.Value)
}

var numberRe = regexp.MustCompile(`-?\d+(?:[.,]\d+)?`)

func quoteHasNumber(quote string, v float64) bool {
	for _, m := range numberRe.FindAllString(quote, -1) {
		if f, err := strconv.ParseFloat(strings.ReplaceAll(m, ",", "."), 64); err == nil && math.Abs(f-v) < 1e-9+1e-6*math.Abs(v) {
			return true
		}
	}
	return false
}

// normalizeText makes quotes comparable to the article: lower case, single
// spaces, plain apostrophes and spaces.
func normalizeText(s string) string {
	s = strings.NewReplacer(" ", " ", "’", "'", "‘", "'", " ", " ").Replace(s)
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

// TextHash identifies article text so unchanged articles skip the model.
func TextHash(s string) string {
	sum := sha256.Sum256([]byte(normalizeText(s)))
	return hex.EncodeToString(sum[:8])
}

func enum(v string, allowed []string) string {
	v = strings.TrimSpace(v)
	for _, a := range allowed {
		if v == a {
			return v
		}
	}
	return "unknown"
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:n-1])) + "…"
}
