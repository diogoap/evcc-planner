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

	"github.com/diogoap/evcc-planner/internal/aiplan"
	"github.com/diogoap/evcc-planner/internal/audit"
	"github.com/diogoap/evcc-planner/internal/calendar"
	"github.com/diogoap/evcc-planner/internal/clock"
	"github.com/diogoap/evcc-planner/internal/config"
	"github.com/diogoap/evcc-planner/internal/demo"
	"github.com/diogoap/evcc-planner/internal/evcc"
	"github.com/diogoap/evcc-planner/internal/forecast"
	"github.com/diogoap/evcc-planner/internal/homeassistant"
	"github.com/diogoap/evcc-planner/internal/model"
	"github.com/diogoap/evcc-planner/internal/openrouter"
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
	phone := &homeassistant.NotifyStub{}
	// The demo car's climate and an outside temperature sensor; their
	// clock follows the service's.
	climate := homeassistant.NewClimateStub(func() time.Time { return svc.Clock.Now() })
	// Demo services can be made to fail on request (Settings → Connections).
	var faults *demo.Faults
	if cfg.Demo {
		faults = &demo.Faults{Phone: phone, Climate: climate}
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
	if faults != nil {
		source = faults.Calendar(source)
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
		if faults != nil {
			i.EVCC, router = faults.EVCC(i.EVCC), faults.Routes(router)
		}
		i.Routes = &routes.Resolver{
			Router: router, Cache: st, Clock: clk, Location: cfg.Location,
			TTL: cs.RoutesCacheTTL(), TrafficAware: cs.RoutesTrafficAware,
		}
		i.Forecasts = forecasts(cfg, cs, clk, client)
		i.AI, i.AINote = aiDecider(cfg, cs, client)
		i.HA, i.HASensors, i.HANote = homeAssistant(cfg, cs, stub, climate, func() time.Time { return svc.Clock.Now() })
		if faults != nil && i.HA != nil {
			i.HA = faults.HomeAssistant(i.HA)
		}
		i.Notify, i.Actions, i.NotifyNote = notifier(cfg, cs, phone)
		i.Climate, i.ClimateNote = climateController(cfg, cs, climate)
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
	srv := &server.Server{Svc: svc, Log: log, Demo: stub, Faults: faults}
	if cfg.Demo && cfg.HomeAssistant.Mode == config.ModeStub {
		srv.Climate = climate
	}
	if cfg.Demo {
		srv.Reset = func(ctx context.Context) error {
			faults.Clear()
			climate.Reset()
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
			if err := svc.SyncPlan(ctx); err != nil {
				return err
			}
			if err := svc.CheckNotifications(ctx); err != nil {
				return err
			}
			if err := svc.RefreshForecasts(ctx, true); err != nil {
				return err
			}
			// The Overview's climate badge follows the reset demo car.
			if err := svc.CheckHeating(ctx); err != nil {
				return err
			}
			return svc.RecordHistory(ctx)
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

// aiDecider builds AI mode's decision maker: a model through OpenRouter, or
// the labelled stand-in in stub mode. Without a key there is none, and AI
// mode keeps the automatic rules. A week's plan takes longer than an
// article, so it gets at least two minutes.
func aiDecider(cfg *config.Config, cs model.ConnectionSettings,
	client func(string, time.Duration, ...func(*audit.Transport)) *http.Client) (aiplan.Decider, string) {
	switch {
	case cfg.OpenRouter.Mode == config.ModeStub:
		return aiplan.Stub{}, ""
	case cfg.OpenRouter.APIKey == "":
		return nil, "Set " + cfg.OpenRouter.APIKeyEnv + " to an OpenRouter API key to use AI mode; until then the automatic rules apply"
	}
	return &aiplan.OpenRouter{Client: &openrouter.Client{
		BaseURL: cfg.OpenRouter.BaseURL, APIKey: cfg.OpenRouter.APIKey,
		HTTP: client(service.APIOpenRouter, max(cs.OpenRouterTimeout(), 2*time.Minute)),
	}}, ""
}

// homeAssistant builds the always-on SoC source and the sensor list, or
// explains why there is none. The stub follows the demo car's battery (a
// fixed one when EVCC is live). Local Home Assistant calls are not in the
// activity log.
func homeAssistant(cfg *config.Config, cs model.ConnectionSettings, car *evcc.Stub, climate *homeassistant.ClimateStub,
	now func() time.Time) (homeassistant.Reader, homeassistant.SensorLister, string) {
	ha := cfg.HomeAssistant
	if ha.Mode == config.ModeStub {
		battery := func() (float64, float64) { return 38, 122 }
		if car != nil {
			battery = car.Battery
		}
		s := homeassistant.Stub{Now: now, Car: battery, Climate: climate}
		return s, s, ""
	}
	if ha.Token == "" {
		return nil, nil, "Set " + ha.TokenEnv + " to read the SoC while the car is unplugged"
	}
	c := &homeassistant.HTTPClient{
		BaseURL: cs.HAURL, Token: ha.Token, HTTP: &http.Client{Timeout: cs.HATimeout()},
		SoCEntity: cs.HASoCEntity, RangeEntity: cs.HARangeEntity, UpdatedEntity: cs.HAUpdatedEntity,
		Location: cfg.Location,
	}
	if cs.HASoCEntity == "" {
		return nil, c, "Choose the car's SoC sensor under Connection settings"
	}
	return c, c, ""
}

// notifier sends phone notifications through Home Assistant, or explains
// why it cannot. The stub records them and has no button listener (demo
// mode presses its buttons in the UI).
func notifier(cfg *config.Config, cs model.ConnectionSettings, phone *homeassistant.NotifyStub) (
	homeassistant.Notifier, homeassistant.ActionListener, string) {
	ha := cfg.HomeAssistant
	if ha.Mode == config.ModeStub {
		return phone, nil, ""
	}
	if ha.Token == "" {
		return nil, nil, "Set " + ha.TokenEnv + " to send notifications through Home Assistant"
	}
	c := &homeassistant.HTTPClient{BaseURL: cs.HAURL, Token: ha.Token, HTTP: &http.Client{Timeout: cs.HATimeout()}}
	return c, c, ""
}

// climateController reads and turns on the car's climate for car heating,
// or explains why it cannot. The stub is the demo car's climate.
func climateController(cfg *config.Config, cs model.ConnectionSettings, climate *homeassistant.ClimateStub) (
	homeassistant.ClimateController, string) {
	ha := cfg.HomeAssistant
	if ha.Mode == config.ModeStub {
		return climate, ""
	}
	if ha.Token == "" {
		return nil, "Set " + ha.TokenEnv + " to turn on the car's heating through Home Assistant"
	}
	return &homeassistant.HTTPClient{BaseURL: cs.HAURL, Token: ha.Token, HTTP: &http.Client{Timeout: cs.HATimeout()}}, ""
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

// historyCheck is how often the day's history checks whether a save is due.
const historyCheck = time.Minute

// heatingCheck is how often car heating checks whether a departure's time
// has come.
const heatingCheck = time.Minute

// StartJobs runs EVCC polling (restoring temporary EVCC changes, planning
// when the car is plugged in, and AI mode's runs), Home Assistant SoC polling, calendar
// sync, the daily forecasts and day history, and the daily history cleanup
// until ctx ends; wait blocks until they have stopped. Intervals follow the
// connection settings. Each job marks its work with its source for the
// activity log.
func (a *App) StartJobs(ctx context.Context) (wait func()) {
	var wg sync.WaitGroup
	jobCtx := audit.WithSource(ctx, model.SourceJob)
	svc := a.Service
	a.every(ctx, &wg, "the EVCC check", func() time.Duration { return svc.Conn().EVCCPollInterval() }, func() {
		if err := a.Service.PollEVCC(jobCtx); err != nil && ctx.Err() == nil {
			a.Log.Warn("evcc poll failed", "err", err)
			return
		}
		if err := a.Service.CheckAdjustments(jobCtx); err != nil && ctx.Err() == nil {
			a.Log.Warn("checking temporary evcc changes failed", "err", err)
		}
		if err := a.Service.SyncPlan(jobCtx); err != nil && ctx.Err() == nil {
			a.Log.Warn("automatic plan check failed", "err", err)
		}
		// AI mode: a new AI plan after new prices or a plug-in, applied at once.
		if err := a.Service.CheckAI(jobCtx); err != nil && ctx.Err() == nil {
			a.Log.Warn("AI plan check failed", "err", err)
		}
		// New prices: plans from advice follow the new advice.
		if err := a.Service.ReviewNewPrices(jobCtx); err != nil && ctx.Err() == nil {
			a.Log.Warn("reviewing plans after new prices failed", "err", err)
		}
		if err := a.Service.CheckNotifications(jobCtx); err != nil && ctx.Err() == nil {
			a.Log.Warn("notification check failed", "err", err)
		}
	})
	// Changes to trips and settings run the automatic plan check once
	// they settle.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case <-svc.Kicked():
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(kickDelay):
			}
			select {
			case <-svc.Kicked():
			default:
			}
			if err := a.Service.SyncPlan(jobCtx); err != nil && ctx.Err() == nil {
				a.Log.Warn("automatic plan check failed", "err", err)
			}
		}
	}()
	// Runs even while Home Assistant is not configured: a sensor can be
	// chosen later.
	wg.Add(1)
	go func() {
		defer wg.Done()
		svc.ListenNotificationActions(jobCtx)
	}()
	a.every(ctx, &wg, "the Home Assistant check", func() time.Duration { return svc.Conn().HAPollInterval() }, func() {
		if err := a.Service.PollHomeAssistant(jobCtx); err != nil && ctx.Err() == nil {
			a.Log.Warn("home assistant poll failed", "err", err)
		}
	})
	a.every(ctx, &wg, "the forecast check", fixed(forecastCheck), func() {
		if err := a.Service.RefreshForecasts(jobCtx, false); err != nil && ctx.Err() == nil {
			a.Log.Warn("forecast refresh incomplete", "err", err)
		}
		if err := a.Service.AutoApply(audit.WithSource(ctx, model.SourceAutomation)); err != nil && ctx.Err() == nil {
			a.Log.Warn("advice auto-apply failed", "err", err)
		}
	})
	// Car heating decides once its time before a departure has come, and
	// clears its messages after half an hour; apart from the EVCC check, so
	// it also works while EVCC is unreachable.
	a.every(ctx, &wg, "the car heating check", fixed(heatingCheck), func() {
		if err := a.Service.CheckHeating(jobCtx); err != nil && ctx.Err() == nil {
			a.Log.Warn("car heating check failed", "err", err)
		}
	})
	// Saves only as the day starts and late in the evening (RecordHistory).
	a.every(ctx, &wg, "the day's history", fixed(historyCheck), func() {
		if err := a.Service.RecordHistory(jobCtx); err != nil && ctx.Err() == nil {
			a.Log.Warn("saving the day's history failed", "err", err)
		}
	})
	calCtx := audit.WithSource(ctx, model.SourceCalendarSync)
	a.every(ctx, &wg, "the calendar sync", func() time.Duration { return svc.Conn().CalendarSyncInterval() }, func() {
		if err := a.Syncer.SyncAll(calCtx); err != nil && ctx.Err() == nil {
			a.Log.Warn("calendar sync incomplete", "err", err)
		}
		svc.Kick()
	})
	a.every(ctx, &wg, "the daily cleanup", fixed(24*time.Hour), func() {
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

// kickDelay lets a burst of edits settle before the automatic plan check.
var kickDelay = 3 * time.Second

// every runs fn now and then each time interval() has passed since the last
// run, until ctx ends.
func (a *App) every(ctx context.Context, wg *sync.WaitGroup, job string, interval func() time.Duration, fn func()) {
	run := func() {
		// /readyz and the health line notice a job that stops running.
		a.Service.JobStarted(job, interval())
		fn()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		run()
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
					run()
				}
			}
		}
	}()
}

func fixed(d time.Duration) func() time.Duration { return func() time.Duration { return d } }
