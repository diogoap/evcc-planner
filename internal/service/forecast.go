package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/diogoap/evcc-planner/internal/audit"
	"github.com/diogoap/evcc-planner/internal/forecast"
	"github.com/diogoap/evcc-planner/internal/localtime"
	"github.com/diogoap/evcc-planner/internal/model"
	"github.com/diogoap/evcc-planner/internal/store"
)

// Forecast source keys in the store.
const (
	SourceTibber = "tibber"
	SourceKNMI   = "knmi"
)

// Forecasts are the daily forecast integrations. Weather is nil when KNMI
// is not configured.
type Forecasts struct {
	Article   forecast.ArticleSource
	Extractor forecast.Extractor
	Weather   forecast.WeatherSource
	// Notes explain fallbacks, e.g. a missing API key.
	ExtractorNote string
	WeatherNote   string
}

// RefreshForecasts fetches each source that is due (once a day at
// the daily fetch time, retrying failures), or all of them when forced. A
// failure keeps the last valid payload.
func (s *Service) RefreshForecasts(ctx context.Context, force bool) error {
	s.forecastMu.Lock()
	defer s.forecastMu.Unlock()
	// One set of sources and settings for the whole refresh, even if they
	// are saved meanwhile.
	cs := s.state()
	var errs []error
	if err := s.refreshOutlook(ctx, cs, force); err != nil {
		errs = append(errs, fmt.Errorf("tibber outlook: %w", err))
	}
	if cs.Forecasts.Weather != nil {
		if err := s.refreshWeather(ctx, cs, force); err != nil {
			errs = append(errs, fmt.Errorf("knmi: %w", err))
		}
	}
	return errors.Join(errs...)
}

// due reports whether a source should be fetched now.
func (s *Service) due(rec *store.ForecastRecord, now time.Time, c model.ConnectionSettings) bool {
	if rec == nil {
		return true
	}
	retry := rec.LastAttemptAt == nil || now.Sub(*rec.LastAttemptAt) >= c.ForecastRetryInterval()
	if rec.LastSuccessAt == nil {
		return retry
	}
	at, _ := time.Parse("15:04", c.ForecastDailyAt)
	today := localtime.StartOfDay(now, s.loc())
	slot := localtime.OnDay(today, at.Hour(), at.Minute())
	if now.Before(slot) {
		slot = localtime.OnDay(localtime.AddDays(today, -1), at.Hour(), at.Minute())
	}
	if !rec.LastSuccessAt.Before(slot) {
		return false
	}
	return rec.LastAttemptAt.Before(slot) || retry
}

func (s *Service) refreshOutlook(ctx context.Context, cs *connState, force bool) error {
	f := cs.Forecasts
	rec, err := s.Store.Forecast(SourceTibber)
	if err != nil {
		return err
	}
	now := s.now()
	if !force && !s.due(rec, now, cs.settings) {
		return nil
	}
	fail := func(err error) error {
		s.Log.Warn("forecast fetch failed", "source", SourceTibber, "err", err)
		return errors.Join(err, s.Store.SaveForecastFailure(SourceTibber, now, err.Error()))
	}
	page, err := f.Article.Fetch(audit.WithCall(ctx, audit.Call{Summary: "Tibber price outlook page"}))
	if err != nil {
		return fail(err)
	}
	text, err := forecast.ArticleText(page)
	if err != nil {
		return fail(err)
	}
	// Unchanged articles are not sent to the model again.
	hash := forecast.TextHash(text) + ":" + f.Extractor.Name()
	if rec != nil && rec.Payload != nil && rec.ContentHash == hash {
		s.Log.Info("forecast unchanged", "source", SourceTibber)
		return s.Store.TouchForecast(SourceTibber, now)
	}
	req := forecast.ExtractRequest{Text: text, URL: f.Article.URL(), FetchedAt: now, Location: s.loc()}
	ex, usage, err := f.Extractor.Extract(audit.WithCall(ctx, audit.Call{
		Summary: "Structure the price outlook with " + cs.settings.OpenRouterModel,
		Detail:  map[string]any{"model": cs.settings.OpenRouterModel, "articleChars": len(text)},
	}), req)
	if err != nil {
		return fail(err)
	}
	outlook, err := forecast.Normalize(ex, forecast.NormalizeInput{
		Text: text, URL: f.Article.URL(), FetchedAt: now, Location: s.loc(), VATPct: cs.settings.ForecastVATPct,
		Extraction: f.Extractor.Label(),
	})
	if err != nil {
		return fail(err)
	}
	raw, err := json.Marshal(outlook)
	if err != nil {
		return err
	}
	detail := f.Extractor.Label()
	if usage.PromptTokens > 0 {
		detail += fmt.Sprintf(" · %d+%d tokens", usage.PromptTokens, usage.CompletionTokens)
	}
	s.Log.Info("forecast stored", "source", SourceTibber, "period", outlook.ValidFrom+".."+outlook.ValidTo,
		"days", len(outlook.Days), "warnings", len(outlook.Warnings), "extraction", detail)
	return s.Store.SaveForecast(SourceTibber, now, raw, hash, detail)
}

func (s *Service) refreshWeather(ctx context.Context, cs *connState, force bool) error {
	rec, err := s.Store.Forecast(SourceKNMI)
	if err != nil {
		return err
	}
	now := s.now()
	if !force && !s.due(rec, now, cs.settings) {
		return nil
	}
	w, err := cs.Forecasts.Weather.Fetch(ctx)
	if err != nil {
		s.Log.Warn("forecast fetch failed", "source", SourceKNMI, "err", err)
		return errors.Join(err, s.Store.SaveForecastFailure(SourceKNMI, now, err.Error()))
	}
	w.FetchedAt = now
	raw, err := json.Marshal(w)
	if err != nil {
		return err
	}
	s.Log.Info("forecast stored", "source", SourceKNMI, "file", w.SourceFile, "days", len(w.Days))
	return s.Store.SaveForecast(SourceKNMI, now, raw, w.SourceFile, w.Source)
}

// outlook returns the stored outlook and its record.
func (s *Service) outlook() (*forecast.Outlook, *store.ForecastRecord, error) {
	rec, err := s.Store.Forecast(SourceTibber)
	if err != nil || rec == nil || rec.Payload == nil {
		return nil, rec, err
	}
	var o forecast.Outlook
	if err := json.Unmarshal(rec.Payload, &o); err != nil {
		return nil, rec, fmt.Errorf("decode stored outlook: %w", err)
	}
	return &o, rec, nil
}

// weather returns the stored weather outlook and its record.
func (s *Service) weather() (*forecast.Weather, *store.ForecastRecord, error) {
	rec, err := s.Store.Forecast(SourceKNMI)
	if err != nil || rec == nil || rec.Payload == nil {
		return nil, rec, err
	}
	var w forecast.Weather
	if err := json.Unmarshal(rec.Payload, &w); err != nil {
		return nil, rec, fmt.Errorf("decode stored weather: %w", err)
	}
	return &w, rec, nil
}

// ForecastsView is the forecast detail shown in Settings.
type ForecastsView struct {
	Outlook *forecast.Outlook `json:"outlook"`
	Weather *forecast.Weather `json:"weather"`
}

// ForecastDetail returns the stored payloads.
func (s *Service) ForecastDetail() (*ForecastsView, error) {
	o, _, err := s.outlook()
	if err != nil {
		return nil, err
	}
	w, _, err := s.weather()
	if err != nil {
		return nil, err
	}
	return &ForecastsView{Outlook: o, Weather: w}, nil
}

// forecastConnections reports the forecast integrations.
func (s *Service) forecastConnections() ([]ConnectionView, error) {
	f := s.Clients().Forecasts
	_, trec, err := s.outlook()
	if err != nil {
		return nil, err
	}
	_, wrec, err := s.weather()
	if err != nil {
		return nil, err
	}
	status := func(c *ConnectionView, rec *store.ForecastRecord, stub bool) {
		switch {
		case rec != nil && rec.LastError != "":
			c.Status, c.Detail = "error", rec.LastError
			if rec.Payload != nil {
				c.Detail += " (keeping the last valid forecast)"
			}
		case stub:
			c.Status = "stub"
		default:
			c.Status = "ok"
		}
		if rec != nil {
			c.LastSuccessAt = rec.LastSuccessAt
		}
		if rec == nil || (rec.LastSuccessAt == nil && rec.LastError == "") {
			c.Detail += " · not fetched yet"
		}
	}

	tib := ConnectionView{Name: "Tibber outlook", Mode: s.Cfg.Tibber.Mode, Detail: "Weekly price outlook article"}
	if f.Article.Name() == "stub" {
		tib.Detail = "Demo article"
	}
	status(&tib, trec, f.Article.Name() == "stub")

	or := ConnectionView{Name: "OpenRouter", Mode: s.Cfg.OpenRouter.Mode, Detail: "Model: " + s.Conn().OpenRouterModel}
	if f.Extractor.Name() == "openrouter" {
		or.Status = "ok"
		if trec != nil && trec.Detail != "" {
			or.Detail = trec.Detail
		}
	} else {
		or.Status, or.Detail = "stub", f.Extractor.Label()
		if f.ExtractorNote != "" {
			or.Detail = f.ExtractorNote
		}
	}

	knmi := ConnectionView{Name: "KNMI", Mode: s.Cfg.KNMI.Mode, Detail: "Multi-day weather outlook"}
	switch {
	case f.Weather == nil:
		knmi.Status, knmi.Detail = "not_configured", f.WeatherNote
	default:
		if f.Weather.Name() == "stub" {
			knmi.Detail = "Demo weather"
		}
		status(&knmi, wrec, f.Weather.Name() == "stub")
	}
	return []ConnectionView{tib, or, knmi}, nil
}
