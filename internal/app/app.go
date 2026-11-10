// Package app wires configuration to the planner's components. The command
// and the API tests share it so tests run the production wiring.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/diogoap/evcc-planner/internal/audit"
	"github.com/diogoap/evcc-planner/internal/calendar"
	"github.com/diogoap/evcc-planner/internal/clock"
	"github.com/diogoap/evcc-planner/internal/config"
	"github.com/diogoap/evcc-planner/internal/demo"
	"github.com/diogoap/evcc-planner/internal/evcc"
	"github.com/diogoap/evcc-planner/internal/forecast"
	"github.com/diogoap/evcc-planner/internal/model"
	"github.com/diogoap/evcc-planner/internal/routes"
	"github.com/diogoap/evcc-planner/internal/server"
	"github.com/diogoap/evcc-planner/internal/service"
	"github.com/diogoap/evcc-planner/internal/store"
)

// App is a wired planner instance.
type App struct {
	Cfg     *config.Config
	Log     *slog.Logger
	Store   *store.Store
	Service *service.Service
	Syncer  *calendar.Syncer
	Stub    *evcc.Stub // non-nil when EVCC is stubbed
	Handler http.Handler
}

// New opens the database, builds integrations for the configured modes, and
// seeds demo data or bootstrap places.
func New(cfg *config.Config, log *slog.Logger) (*App, error) {
	var clk clock.Clock = clock.Real{}
	if cfg.Fixed != nil {
		clk = clock.Fixed{T: *cfg.Fixed}
	}
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		return nil, err
	}
	// Every external call is recorded in the activity log. The recorder's
	// clock follows the service's, which tests may replace.
	var svc *service.Service
	rec := &audit.Recorder{
		Sink: st, Redactor: audit.NewRedactor(cfg.Secrets()...), Log: log,
		Now: func() time.Time { return svc.Clock.Now() },
	}
	client := func(service string, timeout time.Duration, opts ...func(*audit.Transport)) *http.Client {
		t := &audit.Transport{Service: service, Recorder: rec}
		for _, o := range opts {
			o(t)
		}
		return &http.Client{Timeout: timeout, Transport: t}
	}

	var evccClient evcc.Client
	var stub *evcc.Stub
	if cfg.EVCC.Mode == config.ModeStub {
		stub = evcc.NewStub(clk, cfg.Location)
		evccClient = stub
	} else {
		evccClient = evcc.NewHTTPClient(cfg.EVCC.URL, cfg.EVCC.Timeout)
	}

	var router routes.Router = routes.Stub{}
	routesNote := ""
	if cfg.Routes.Mode == config.ModeGoogle {
		if cfg.Routes.APIKey == "" {
			routesNote = "No API key (" + cfg.Routes.APIKeyEnv + "); using stub distances"
			log.Warn("google routes api key missing; using stub distances", "env", cfg.Routes.APIKeyEnv)
		} else {
			router = &routes.Google{
				BaseURL: cfg.Routes.BaseURL, APIKey: cfg.Routes.APIKey, TrafficAware: cfg.Routes.TrafficAware,
				HTTP: client(service.APIGoogleRoutes, 15*time.Second), Now: clk.Now,
			}
		}
	}
	resolver := &routes.Resolver{
		Router: router, Cache: st, Clock: clk, Location: cfg.Location,
		TTL: cfg.Routes.CacheTTL, TrafficAware: cfg.Routes.TrafficAware,
	}

	var source calendar.Source = calendar.HTTPSource{
		HTTP: client(service.APICalendar, 30*time.Second, func(t *audit.Transport) { t.HideURL = true }),
	}
	calendars := cfg.Calendar.Sources
	if cfg.Calendar.Mode == config.ModeStub {
		source = calendar.StubSource{Clock: clk, Location: cfg.Location}
		if len(calendars) == 0 {
			calendars = calendar.StubCalendars
		}
	}
	syncer := &calendar.Syncer{
		Store: st, Source: source, Calendars: calendars, Clock: clk, Location: cfg.Location, Log: log,
	}

	svc = &service.Service{
		Cfg: cfg, Store: st, Clock: clk, EVCC: evccClient, Routes: resolver, Calendar: syncer,
		Log: log, Audit: rec, RoutesNote: routesNote, Forecasts: forecasts(cfg, clk, log, client),
	}
	if cfg.Demo {
		if err := demo.Seed(st, clk.Now(), cfg.Location, cfg.Defaults.Settings()); err != nil {
			st.Close()
			return nil, fmt.Errorf("seed demo data: %w", err)
		}
	}
	if err := svc.Bootstrap(audit.WithSource(context.Background(), model.SourceJob)); err != nil {
		st.Close()
		return nil, err
	}
	srv := &server.Server{Svc: svc, Log: log, Demo: stub}
	if cfg.Demo {
		srv.Reset = func(ctx context.Context) error {
			if err := st.Reset(); err != nil {
				return err
			}
			if err := demo.Seed(st, clk.Now(), cfg.Location, cfg.Defaults.Settings()); err != nil {
				return err
			}
			if stub != nil {
				stub.Reset()
			}
			if err := syncer.SyncAll(ctx); err != nil {
				return err
			}
			if err := svc.PollEVCC(ctx); err != nil {
				return err
			}
			return svc.RefreshForecasts(ctx, true)
		}
	}
	return &App{
		Cfg: cfg, Log: log, Store: st, Service: svc, Syncer: syncer, Stub: stub, Handler: srv.Handler(),
	}, nil
}

// forecasts builds the forecast sources for the configured modes. Without an
// OpenRouter key the labelled pattern-based stub structures the article;
// without a KNMI key there is no weather outlook.
func forecasts(cfg *config.Config, clk clock.Clock, log *slog.Logger,
	client func(string, time.Duration, ...func(*audit.Transport)) *http.Client) service.Forecasts {
	f := service.Forecasts{
		Article:   forecast.HTTPArticle{PageURL: cfg.Tibber.URL, HTTP: client(service.APITibber, 30*time.Second)},
		Extractor: forecast.Pattern{},
	}
	if cfg.Tibber.Mode == config.ModeStub {
		f.Article = forecast.StubArticle{Clock: clk, Location: cfg.Location}
	}
	if cfg.OpenRouter.Mode == config.ModeLive {
		if cfg.OpenRouter.APIKey == "" {
			f.ExtractorNote = "No API key (" + cfg.OpenRouter.APIKeyEnv + "); using the pattern-based stub"
			log.Warn("openrouter api key missing; using the pattern-based outlook extraction", "env", cfg.OpenRouter.APIKeyEnv)
		} else {
			f.Extractor = &forecast.OpenRouter{
				BaseURL: cfg.OpenRouter.BaseURL, APIKey: cfg.OpenRouter.APIKey, Model: cfg.OpenRouter.Model,
				HTTP: client(service.APIOpenRouter, cfg.OpenRouter.Timeout),
			}
		}
	}
	switch {
	case cfg.KNMI.Mode == config.ModeStub:
		f.Weather = forecast.StubKNMI{Clock: clk, Location: cfg.Location}
	case cfg.KNMI.APIKey == "":
		f.WeatherNote = "Set " + cfg.KNMI.APIKeyEnv + " to a KNMI Open Data API key"
		log.Warn("knmi api key missing; no weather outlook", "env", cfg.KNMI.APIKeyEnv)
	default:
		f.Weather = &forecast.KNMI{
			BaseURL: cfg.KNMI.BaseURL, APIKey: cfg.KNMI.APIKey, Dataset: cfg.KNMI.Dataset, Version: cfg.KNMI.Version,
			HTTP:     client(service.APIKNMI, 30*time.Second, func(t *audit.Transport) { t.Label = knmiLabel }),
			Location: cfg.Location,
		}
	}
	return f
}

// knmiLabel names the three requests of a KNMI fetch.
func knmiLabel(r *http.Request) string {
	switch {
	case strings.HasSuffix(r.URL.Path, "/files"):
		return "KNMI: list the latest outlook files"
	case strings.HasSuffix(r.URL.Path, "/url"):
		return "KNMI: get the outlook's download link"
	}
	return "KNMI: download the weather outlook"
}

// forecastCheck is how often the daily forecast schedule and advice
// auto-apply are checked.
const forecastCheck = 15 * time.Minute

// StartJobs runs EVCC polling (with restoring temporary EVCC changes),
// calendar sync, the daily forecasts, and the daily history cleanup until
// ctx ends; wait blocks until they have stopped. Each job marks its work
// with its source for the activity log.
func (a *App) StartJobs(ctx context.Context) (wait func()) {
	var wg sync.WaitGroup
	jobCtx := audit.WithSource(ctx, model.SourceJob)
	every(ctx, &wg, a.Cfg.EVCC.PollInterval, func() {
		if err := a.Service.PollEVCC(jobCtx); err != nil && ctx.Err() == nil {
			a.Log.Warn("evcc poll failed", "err", err)
			return
		}
		if err := a.Service.CheckAdjustments(jobCtx); err != nil && ctx.Err() == nil {
			a.Log.Warn("checking temporary evcc changes failed", "err", err)
		}
	})
	every(ctx, &wg, forecastCheck, func() {
		if err := a.Service.RefreshForecasts(jobCtx, false); err != nil && ctx.Err() == nil {
			a.Log.Warn("forecast refresh incomplete", "err", err)
		}
		if err := a.Service.AutoApply(audit.WithSource(ctx, model.SourceAutomation)); err != nil && ctx.Err() == nil {
			a.Log.Warn("advice auto-apply failed", "err", err)
		}
	})
	calCtx := audit.WithSource(ctx, model.SourceCalendarSync)
	every(ctx, &wg, a.Cfg.Calendar.SyncInterval, func() {
		if err := a.Syncer.SyncAll(calCtx); err != nil && ctx.Err() == nil {
			a.Log.Warn("calendar sync incomplete", "err", err)
		}
	})
	every(ctx, &wg, 24*time.Hour, func() {
		if err := a.Service.Cleanup(jobCtx); err != nil && ctx.Err() == nil {
			a.Log.Warn("history cleanup failed", "err", err)
		}
	})
	return wg.Wait
}

// Close releases the database.
func (a *App) Close() error { return a.Store.Close() }

// every runs fn now and then at each interval until ctx ends.
func every(ctx context.Context, wg *sync.WaitGroup, interval time.Duration, fn func()) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		fn()
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				fn()
			}
		}
	}()
}
