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
	"github.com/diogoap/evcc-planner/internal/homeassistant"
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

	var stub *evcc.Stub
	if cfg.EVCC.Mode == config.ModeStub {
		stub = evcc.NewStub(clk, cfg.Location)
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

	svc = &service.Service{Cfg: cfg, Store: st, Clock: clk, Calendar: syncer, Log: log, Audit: rec}
	// Integrations are rebuilt whenever connection settings are saved; the
	// demo car (stub) is kept.
	svc.Build = func(cs model.ConnectionSettings) service.Integrations {
		var i service.Integrations
		if stub != nil {
			i.EVCC = stub
		} else {
			i.EVCC = evcc.NewHTTPClient(cs.EVCCURL, cs.EVCCTimeout())
		}
		var router routes.Router
		router, i.RoutesNote = routesRouter(cfg, cs, clk, client)
		i.Routes = &routes.Resolver{
			Router: router, Cache: st, Clock: clk, Location: cfg.Location,
			TTL: cs.RoutesCacheTTL(), TrafficAware: cs.RoutesTrafficAware,
		}
		i.Forecasts = forecasts(cfg, cs, clk, client)
		i.HA, i.HASensors, i.HANote = homeAssistant(cfg, cs, stub, func() time.Time { return svc.Clock.Now() })
		return i
	}
	if cfg.Demo {
		if err := demo.Seed(st, clk.Now(), cfg.Location, cfg.Defaults.Settings()); err != nil {
			st.Close()
			return nil, fmt.Errorf("seed demo data: %w", err)
		}
	}
	if err := svc.LoadSettings(); err != nil {
		st.Close()
		return nil, err
	}
	logNotes(log, svc.Clients())
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
			if err := svc.LoadSettings(); err != nil {
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
			if err := svc.PollHomeAssistant(ctx); err != nil {
				return err
			}
			// Learn that the demo car is unplugged, so plugging it in counts.
			if err := svc.PlanOnPlugIn(ctx); err != nil {
				return err
			}
			return svc.RefreshForecasts(ctx, true)
		}
	}
	return &App{
		Cfg: cfg, Log: log, Store: st, Service: svc, Syncer: syncer, Stub: stub, Handler: srv.Handler(),
	}, nil
}

// logNotes warns at start about integrations that fall back or are off.
func logNotes(log *slog.Logger, i service.Integrations) {
	for name, note := range map[string]string{
		"google routes": i.RoutesNote, "openrouter": i.Forecasts.ExtractorNote,
		"knmi": i.Forecasts.WeatherNote, "home assistant": i.HANote,
	} {
		if note != "" {
			log.Warn(name+" not fully configured", "note", note)
		}
	}
}

// routesRouter picks Google Routes, or the labelled stub (without a key).
func routesRouter(cfg *config.Config, cs model.ConnectionSettings, clk clock.Clock,
	client func(string, time.Duration, ...func(*audit.Transport)) *http.Client) (routes.Router, string) {
	switch {
	case cfg.Routes.Mode != config.ModeGoogle:
		return routes.Stub{}, ""
	case cfg.Routes.APIKey == "":
		return routes.Stub{}, "No API key (" + cfg.Routes.APIKeyEnv + "); using stub distances"
	}
	return &routes.Google{
		BaseURL: cfg.Routes.BaseURL, APIKey: cfg.Routes.APIKey, TrafficAware: cs.RoutesTrafficAware,
		HTTP: client(service.APIGoogleRoutes, 15*time.Second), Now: clk.Now,
	}, ""
}

// forecasts builds the forecast sources for the configured modes. Without an
// OpenRouter key the labelled pattern-based stub structures the article;
// without a KNMI key there is no weather outlook.
func forecasts(cfg *config.Config, cs model.ConnectionSettings, clk clock.Clock,
	client func(string, time.Duration, ...func(*audit.Transport)) *http.Client) service.Forecasts {
	f := service.Forecasts{
		Article:   forecast.HTTPArticle{PageURL: cs.TibberURL, HTTP: client(service.APITibber, 30*time.Second)},
		Extractor: forecast.Pattern{},
	}
	if cfg.Tibber.Mode == config.ModeStub {
		f.Article = forecast.StubArticle{Clock: clk, Location: cfg.Location}
	}
	if cfg.OpenRouter.Mode == config.ModeLive {
		if cfg.OpenRouter.APIKey == "" {
			f.ExtractorNote = "No API key (" + cfg.OpenRouter.APIKeyEnv + "); using the pattern-based stub"
		} else {
			f.Extractor = &forecast.OpenRouter{
				BaseURL: cfg.OpenRouter.BaseURL, APIKey: cfg.OpenRouter.APIKey, Model: cs.OpenRouterModel,
				HTTP: client(service.APIOpenRouter, cs.OpenRouterTimeout()),
			}
		}
	}
	switch {
	case cfg.KNMI.Mode == config.ModeStub:
		f.Weather = forecast.StubKNMI{Clock: clk, Location: cfg.Location}
	case cfg.KNMI.APIKey == "":
		f.WeatherNote = "Set " + cfg.KNMI.APIKeyEnv + " to a KNMI Open Data API key"
	default:
		f.Weather = &forecast.KNMI{
			BaseURL: cfg.KNMI.BaseURL, APIKey: cfg.KNMI.APIKey, Dataset: cs.KNMIDataset, Version: cs.KNMIVersion,
			HTTP:     client(service.APIKNMI, 30*time.Second, func(t *audit.Transport) { t.Label = knmiLabel }),
			Location: cfg.Location,
		}
	}
	return f
}

// homeAssistant builds the always-on SoC source and the sensor list, or
// explains why there is none. The stub follows the demo car's battery (a
// fixed one when EVCC is live). Local Home Assistant calls are not in the
// activity log.
func homeAssistant(cfg *config.Config, cs model.ConnectionSettings, car *evcc.Stub,
	now func() time.Time) (homeassistant.Reader, homeassistant.SensorLister, string) {
	ha := cfg.HomeAssistant
	if ha.Mode == config.ModeStub {
		battery := func() (float64, float64) { return 38, 122 }
		if car != nil {
			battery = car.Battery
		}
		s := homeassistant.Stub{Now: now, Car: battery}
		return s, s, ""
	}
	if ha.Token == "" {
		return nil, nil, "Set " + ha.TokenEnv + " to read the SoC while the car is unplugged"
	}
	c := &homeassistant.HTTPClient{
		BaseURL: cs.HAURL, Token: ha.Token, HTTP: &http.Client{Timeout: cs.HATimeout()},
		SoCEntity: cs.HASoCEntity, RangeEntity: cs.HARangeEntity, ObservedAttribute: cs.HAObservedAttribute,
		Location: cfg.Location,
	}
	if cs.HASoCEntity == "" {
		return nil, c, "Choose the car's SoC sensor under Connection settings"
	}
	return c, c, ""
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

// StartJobs runs EVCC polling (restoring temporary EVCC changes and
// planning when the car is plugged in), Home Assistant SoC polling, calendar
// sync, the daily forecasts, and the daily history cleanup until ctx ends;
// wait blocks until they have stopped. Intervals follow the connection
// settings. Each job marks its work with its source for the activity log.
func (a *App) StartJobs(ctx context.Context) (wait func()) {
	var wg sync.WaitGroup
	jobCtx := audit.WithSource(ctx, model.SourceJob)
	svc := a.Service
	every(ctx, &wg, func() time.Duration { return svc.Conn().EVCCPollInterval() }, func() {
		if err := a.Service.PollEVCC(jobCtx); err != nil && ctx.Err() == nil {
			a.Log.Warn("evcc poll failed", "err", err)
			return
		}
		if err := a.Service.CheckAdjustments(jobCtx); err != nil && ctx.Err() == nil {
			a.Log.Warn("checking temporary evcc changes failed", "err", err)
		}
		if err := a.Service.PlanOnPlugIn(jobCtx); err != nil && ctx.Err() == nil {
			a.Log.Warn("plan on plug-in failed", "err", err)
		}
	})
	// Runs even while Home Assistant is not configured: a sensor can be
	// chosen later.
	every(ctx, &wg, func() time.Duration { return svc.Conn().HAPollInterval() }, func() {
		if err := a.Service.PollHomeAssistant(jobCtx); err != nil && ctx.Err() == nil {
			a.Log.Warn("home assistant poll failed", "err", err)
		}
	})
	every(ctx, &wg, fixed(forecastCheck), func() {
		if err := a.Service.RefreshForecasts(jobCtx, false); err != nil && ctx.Err() == nil {
			a.Log.Warn("forecast refresh incomplete", "err", err)
		}
		if err := a.Service.AutoApply(audit.WithSource(ctx, model.SourceAutomation)); err != nil && ctx.Err() == nil {
			a.Log.Warn("advice auto-apply failed", "err", err)
		}
	})
	calCtx := audit.WithSource(ctx, model.SourceCalendarSync)
	every(ctx, &wg, func() time.Duration { return svc.Conn().CalendarSyncInterval() }, func() {
		if err := a.Syncer.SyncAll(calCtx); err != nil && ctx.Err() == nil {
			a.Log.Warn("calendar sync incomplete", "err", err)
		}
	})
	every(ctx, &wg, fixed(24*time.Hour), func() {
		if err := a.Service.Cleanup(jobCtx); err != nil && ctx.Err() == nil {
			a.Log.Warn("history cleanup failed", "err", err)
		}
	})
	return wg.Wait
}

// Close releases the database.
func (a *App) Close() error { return a.Store.Close() }

// everyCheck is how often jobs check whether their interval has passed, so
// a changed interval applies within it.
var everyCheck = time.Second

// every runs fn now and then each time interval() has passed since the last
// run, until ctx ends.
func every(ctx context.Context, wg *sync.WaitGroup, interval func() time.Duration, fn func()) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		fn()
		last := time.Now()
		t := time.NewTicker(everyCheck)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-t.C:
				if now.Sub(last) >= interval() {
					last = now
					fn()
				}
			}
		}
	}()
}

func fixed(d time.Duration) func() time.Duration { return func() time.Duration { return d } }
