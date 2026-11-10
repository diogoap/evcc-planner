package forecast

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/diogoap/evcc-planner/internal/clock"
)

var ams, _ = time.LoadLocation("Europe/Amsterdam")

var fetched = time.Date(2026, 11, 10, 18, 0, 0, 0, ams)

func pageText(t *testing.T) string {
	t.Helper()
	page, err := os.ReadFile("testdata/tibber-page.html")
	if err != nil {
		t.Fatal(err)
	}
	text, err := ArticleText(page)
	if err != nil {
		t.Fatal(err)
	}
	return text
}

func TestArticleTextCutsTheArticle(t *testing.T) {
	text := pageText(t)
	if !strings.HasPrefix(text, "Prognose voor 9 t/m 15 nov\n") {
		t.Errorf("article should start at the heading: %q", text[:40])
	}
	for _, want := range []string{"Weekgemiddelde: €0,259/kWh excl. (€0,461/kWh incl.)", "Maandag 0,6 GW", "zo'n 2% omhoog"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	for _, unwanted := range []string{"Energiecontract", "Blijf op de hoogte", "13:00", "1 t/m 7 jan"} {
		if strings.Contains(text, unwanted) {
			t.Errorf("article should not contain %q", unwanted)
		}
	}
	if _, err := ArticleText([]byte("<html><body><h1>Other page</h1></body></html>")); err == nil {
		t.Error("a page without the outlook heading must be rejected")
	}
}

func TestHeadingPeriod(t *testing.T) {
	cases := []struct {
		text     string
		at       time.Time
		from, to string
	}{
		{"Prognose voor 9 t/m 15 nov", fetched, "2026-11-09", "2026-11-15"},
		{"Prognose voor 28 okt t/m 3 nov", time.Date(2026, 10, 29, 9, 0, 0, 0, ams), "2026-10-28", "2026-11-03"},
		{"Prognose voor 30 t/m 5 jan", time.Date(2027, 1, 2, 9, 0, 0, 0, ams), "2026-12-30", "2027-01-05"},
		{"Prognose voor 29 dec t/m 4 januari", time.Date(2026, 12, 28, 9, 0, 0, 0, ams), "2026-12-29", "2027-01-04"},
	}
	for _, c := range cases {
		from, to, ok := HeadingPeriod(c.text, c.at, ams)
		if !ok || from != c.from || to != c.to {
			t.Errorf("%q: %s..%s %v, want %s..%s", c.text, from, to, ok, c.from, c.to)
		}
	}
	if _, _, ok := HeadingPeriod("Prognose voor 31 t/m 31 nov", fetched, ams); ok {
		t.Error("31 November must not parse")
	}
}

func normalize(t *testing.T, ex Extraction, text string) *Outlook {
	t.Helper()
	o, err := Normalize(ex, NormalizeInput{Text: text, URL: "https://example.test", FetchedAt: fetched, Location: ams, VATPct: 21, Extraction: "test"})
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestPatternExtractionConvertsWithProvenance(t *testing.T) {
	text := pageText(t)
	ex, _, err := Pattern{}.Extract(context.Background(), ExtractRequest{Text: text, FetchedAt: fetched, Location: ams})
	if err != nil {
		t.Fatal(err)
	}
	o := normalize(t, ex, text)
	if o.ValidFrom != "2026-11-09" || o.ValidTo != "2026-11-15" || o.PeriodSource != "heading" {
		t.Errorf("period = %s..%s (%s)", o.ValidFrom, o.ValidTo, o.PeriodSource)
	}
	if o.Weekly.Trend != "rising" || o.Weekly.Price == nil || o.Weekly.Price.EURPerKWh != 0.461 || o.Weekly.Price.Provenance != "stated" {
		t.Fatalf("weekly = %+v price %+v", o.Weekly, o.Weekly.Price)
	}
	mon, sun := o.Day("2026-11-09"), o.Day("2026-11-15")
	if mon == nil || sun == nil || mon.Level != "most_expensive" || sun.Level != "cheapest" {
		t.Fatalf("days = %+v", o.Days)
	}
	// Retail = weekly incl. + (market − weekly excl.) × 1.21.
	if math.Abs(mon.Price.EURPerKWh-(0.461+(0.342-0.259)*1.21)) > 1e-4 || mon.Price.Provenance != "converted" {
		t.Errorf("Monday = %+v", mon.Price)
	}
	if math.Abs(sun.Price.EURPerKWh-(0.461+(0.154-0.259)*1.21)) > 1e-4 {
		t.Errorf("Sunday = %+v", sun.Price)
	}
	if !strings.Contains(mon.Price.Detail, "€0.259 excl. = €0.461 incl.") || mon.Price.Quote == "" {
		t.Errorf("provenance detail = %q, quote %q", mon.Price.Detail, mon.Price.Quote)
	}
	if o.Confidence != 2 || len(o.Warnings) != 0 {
		t.Errorf("confidence %d, warnings %v", o.Confidence, o.Warnings)
	}
}

func TestNormalizeRejectsInventedValues(t *testing.T) {
	text := pageText(t)
	str := func(s string) *string { return &s }
	gw := func(v float64) *float64 { return &v }
	ex := Extraction{
		Period: ExtractedPeriod{From: str("2026-11-09"), To: str("2026-11-16")},
		Weekly: ExtractedWeekly{Trend: "sideways", WindAvgGW: gw(2.5), SolarAvgGW: gw(900), Prices: []StatedPrice{
			{Value: 0.259, Unit: "EUR/kWh", Basis: "excl", Quote: "€0,259/kWh excl."},
		}},
		Days: []ExtractedDay{
			// Invented price: the quote is not in the article.
			{Date: "2026-11-11", Prices: []StatedPrice{{Value: 0.30, Unit: "EUR/kWh", Basis: "incl", Quote: "woensdag €0,30/kWh"}}},
			// Real quote, wrong number.
			{Date: "2026-11-12", Prices: []StatedPrice{{Value: 300, Unit: "EUR/MWh", Basis: "excl", Quote: "Maandag (~€342/MWh excl.)"}}},
			// No weekly incl./excl. pair: an excl. price cannot be converted.
			{Date: "2026-11-09", Level: "most_expensive", Prices: []StatedPrice{{Value: 342, Unit: "EUR/MWh", Basis: "excl", Quote: "Maandag (~€342/MWh excl.)"}}},
			{Date: "2026-11-20", Level: "cheap"},
			{Date: "2026-11-09", Level: "cheap"},
		},
	}
	o := normalize(t, ex, text)
	if o.ValidTo != "2026-11-15" {
		t.Errorf("heading period must win over the model's: %s", o.ValidTo)
	}
	if o.Weekly.Trend != "unknown" || o.Weekly.SolarGW != nil || o.Weekly.WindGW == nil || o.Weekly.Price != nil {
		t.Errorf("weekly = %+v", o.Weekly)
	}
	if len(o.Days) != 3 {
		t.Fatalf("days = %+v", o.Days)
	}
	for _, d := range o.Days {
		if d.Price != nil {
			t.Errorf("%s: price %+v should have been dropped", d.Date, d.Price)
		}
	}
	joined := strings.Join(o.Warnings, "\n")
	for _, want := range []string{"quote not found", "does not contain that number", "not converted", "outside the report period", "duplicate", "implausible weekly solar", "model read the period"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing warning %q in:\n%s", want, joined)
		}
	}
	if o.Confidence != 1 {
		t.Errorf("confidence = %d", o.Confidence)
	}
}

func TestNormalizeNeedsAPeriod(t *testing.T) {
	if _, err := Normalize(Extraction{}, NormalizeInput{Text: "no heading", FetchedAt: fetched, Location: ams}); err == nil {
		t.Error("an outlook without a report period must be rejected")
	}
	far := "2025-01-01"
	_, err := Normalize(Extraction{Period: ExtractedPeriod{From: &far, To: &far}}, NormalizeInput{Text: "x", FetchedAt: fetched, Location: ams})
	if err == nil {
		t.Error("a period far from the fetch date must be rejected")
	}
}

func TestExtractionSchemaIsStrict(t *testing.T) {
	var walk func(path string, v any)
	walk = func(path string, v any) {
		m, ok := v.(map[string]any)
		if !ok {
			return
		}
		if m["type"] == "object" {
			props := m["properties"].(map[string]any)
			req := m["required"].([]string)
			if len(req) != len(props) || m["additionalProperties"] != false {
				t.Errorf("%s: strict objects need every property required and no extras", path)
			}
			for k, p := range props {
				walk(path+"."+k, p)
			}
		}
		if items, ok := m["items"]; ok {
			walk(path+"[]", items)
		}
	}
	walk("$", ExtractionSchema())
	if _, err := json.Marshal(ExtractionSchema()); err != nil {
		t.Fatal(err)
	}
}

func TestOpenRouterRequestAndResponse(t *testing.T) {
	var body map[string]any
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		data, _ := io.ReadAll(r.Body)
		json.Unmarshal(data, &body)
		content, _ := json.Marshal(map[string]any{
			"period": map[string]any{"from": "2026-11-09", "to": "2026-11-15"}, "issued": nil,
			"weekly": map[string]any{"summary": "Higher prices.", "trend_vs_last_week": "rising", "prices": []any{},
				"wind_avg_gw": 2.5, "wind_normal_gw": 4.2, "solar_avg_gw": 1.1, "solar_normal_gw": 1.0, "temperature_avg_c": 5.4},
			"days": []any{map[string]any{"date": "2026-11-09", "prices": []any{}, "level": "most_expensive", "wind": "low",
				"wind_gw": 0.6, "solar": "unknown", "solar_gw": nil, "note": "Calm."}},
			"supporting_text": "Hoger dan vorige week",
		})
		json.NewEncoder(w).Encode(map[string]any{
			"model":   "test/model",
			"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"content": string(content)}}},
			"usage":   map[string]any{"prompt_tokens": 1200, "completion_tokens": 300},
		})
	}))
	defer srv.Close()
	o := &OpenRouter{BaseURL: srv.URL, APIKey: "secret", Model: "test/model", HTTP: srv.Client()}
	ex, usage, err := o.Extract(context.Background(), ExtractRequest{
		Text: "Prognose voor 9 t/m 15 nov", URL: "https://tibber.com/nl/stroomprijsprognose", FetchedAt: fetched, Location: ams,
	})
	if err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer secret" || body["model"] != "test/model" {
		t.Errorf("auth %q, model %v", auth, body["model"])
	}
	// Only the article text goes to the model: no link, tools, or web plugin.
	msgs := body["messages"].([]any)
	user := msgs[len(msgs)-1].(map[string]any)["content"].(string)
	if !strings.Contains(user, "Prognose voor 9 t/m 15 nov") || strings.Contains(user, "http") {
		t.Errorf("user message = %q", user)
	}
	for _, k := range []string{"tools", "plugins"} {
		if _, ok := body[k]; ok {
			t.Errorf("request must not use %s", k)
		}
	}
	rf := body["response_format"].(map[string]any)
	js := rf["json_schema"].(map[string]any)
	if rf["type"] != "json_schema" || js["strict"] != true || body["provider"].(map[string]any)["require_parameters"] != true {
		t.Errorf("structured output not requested strictly: %v", rf)
	}
	if usage.PromptTokens != 1200 || ex.Weekly.Trend != "rising" || len(ex.Days) != 1 || *ex.Days[0].WindGW != 0.6 {
		t.Errorf("usage %+v, extraction %+v", usage, ex)
	}
}

func TestOpenRouterErrorsHideTheKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer bad" {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":{"message":"No auth credentials found","code":401}}`))
			return
		}
		w.Write([]byte(`{"choices":[{"message":{"content":"{\"unexpected\":true}"}}]}`))
	}))
	defer srv.Close()
	o := &OpenRouter{BaseURL: srv.URL, APIKey: "bad", Model: "m", HTTP: srv.Client()}
	_, _, err := o.Extract(context.Background(), ExtractRequest{FetchedAt: fetched, Location: ams})
	if err == nil || err.Error() != "openrouter: No auth credentials found" || strings.Contains(err.Error(), "bad") {
		t.Errorf("error = %v", err)
	}
	o.APIKey = "good"
	if _, _, err := o.Extract(context.Background(), ExtractRequest{FetchedAt: fetched, Location: ams}); err == nil ||
		!strings.Contains(err.Error(), "does not match the schema") {
		t.Errorf("schema mismatch error = %v", err)
	}
}

func TestParseKNMIOutlook(t *testing.T) {
	data, err := os.ReadFile("testdata/knmi-outlook.xml")
	if err != nil {
		t.Fatal(err)
	}
	w, err := ParseKNMIOutlook(data, ams)
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Days) != 7 || w.Days[0].Date != "2026-11-10" || w.Days[6].Date != "2026-11-16" {
		t.Fatalf("days = %+v", w.Days)
	}
	d := w.Days[0]
	if *d.SunshinePct != 20 || *d.PrecipChancePct != 0 || *d.TempMaxHighC != 7 || d.WindDir != "ZO" || *d.WindBft != 2 {
		t.Errorf("day 1 = %+v", d)
	}
	if fri := w.Day("2026-11-13"); *fri.WindBft != 4 || *fri.SunshinePct != 50 {
		t.Errorf("Friday = %+v", fri)
	}
	if !strings.HasPrefix(w.Summary, "Meest droog") || w.IssuedAt == nil || w.IssuedAt.Hour() != 11 {
		t.Errorf("summary %q, issued %v", w.Summary, w.IssuedAt)
	}
	if _, err := ParseKNMIOutlook([]byte("<dataroot/>"), ams); err == nil {
		t.Error("an outlook without days must be rejected")
	}
}

func TestKNMIClientDownloadsTheNewestXML(t *testing.T) {
	data, _ := os.ReadFile("testdata/knmi-outlook.xml")
	var srv *httptest.Server
	var listAuth, downloadAuth string
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/datasets/outlook_weather_forecast/versions/1.0/files":
			listAuth = r.Header.Get("Authorization")
			if r.URL.Query().Get("sorting") != "desc" || r.URL.Query().Get("orderBy") != "created" {
				t.Errorf("list query = %s", r.URL.RawQuery)
			}
			w.Write([]byte(`{"files":[{"filename":"mtv_202611101103.html","created":"2026-11-10T11:03:36+00:00"},
				{"filename":"mtv_202611101103.xml","created":"2026-11-10T11:03:37+00:00"}]}`))
		case r.URL.Path == "/datasets/outlook_weather_forecast/versions/1.0/files/mtv_202611101103.xml/url":
			w.Write([]byte(`{"temporaryDownloadUrl":"` + srv.URL + `/download/x.xml"}`))
		case r.URL.Path == "/download/x.xml":
			downloadAuth = r.Header.Get("Authorization")
			w.Write(data)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	k := &KNMI{BaseURL: srv.URL, APIKey: "key", Dataset: "outlook_weather_forecast", Version: "1.0", HTTP: srv.Client(), Location: ams}
	w, err := k.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if listAuth != "key" || downloadAuth != "" {
		t.Errorf("auth: list %q, download %q (pre-signed links take no key)", listAuth, downloadAuth)
	}
	if w.SourceFile != "mtv_202611101103.xml" || len(w.Days) != 7 || !w.IssuedAt.Equal(time.Date(2026, 11, 10, 11, 3, 37, 0, time.UTC)) {
		t.Errorf("weather = %s %d %v", w.SourceFile, len(w.Days), w.IssuedAt)
	}
}

func TestStubsFollowTheClock(t *testing.T) {
	c := clock.Fixed{T: time.Date(2026, 11, 9, 19, 0, 0, 0, ams)}
	page, _ := StubArticle{Clock: c, Location: ams}.Fetch(context.Background())
	text, err := ArticleText(page)
	if err != nil {
		t.Fatal(err)
	}
	ex, _, _ := Pattern{}.Extract(context.Background(), ExtractRequest{Text: text, FetchedAt: c.T, Location: ams})
	o, err := Normalize(ex, NormalizeInput{Text: text, FetchedAt: c.T, Location: ams, VATPct: 21})
	if err != nil {
		t.Fatal(err)
	}
	tue, thu := o.Day("2026-11-10"), o.Day("2026-11-12")
	if o.ValidFrom != "2026-11-09" || tue == nil || thu == nil || tue.Price.EURPerKWh >= thu.Price.EURPerKWh {
		t.Fatalf("demo outlook = %+v", o)
	}
	w, _ := StubKNMI{Clock: c, Location: ams}.Fetch(context.Background())
	if len(w.Days) != 7 || w.Days[0].Date != "2026-11-09" || *w.Day("2026-11-10").WindBft <= *w.Day("2026-11-12").WindBft {
		t.Errorf("demo weather = %+v", w.Days)
	}
}
