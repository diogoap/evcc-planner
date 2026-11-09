// Package app wires configuration to the planner's components. The command
// and the API tests share it so tests run the production wiring.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/diogoap/evcc-planner/internal/calendar"
	"github.com/diogoap/evcc-planner/internal/clock"
	"github.com/diogoap/evcc-planner/internal/config"
	"github.com/diogoap/evcc-planner/internal/demo"
	"github.com/diogoap/evcc-planner/internal/evcc"
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
				HTTP: &http.Client{Timeout: 15 * time.Second}, Now: clk.Now,
			}
		}
	}
	resolver := &routes.Resolver{
		Router: router, Cache: st, Clock: clk, Location: cfg.Location,
		TTL: cfg.Routes.CacheTTL, TrafficAware: cfg.Routes.TrafficAware,
	}

	var source calendar.Source = calendar.HTTPSource{HTTP: &http.Client{Timeout: 30 * time.Second}}
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

	svc := &service.Service{
		Cfg: cfg, Store: st, Clock: clk, EVCC: evccClient, Routes: resolver, Calendar: syncer,
		Log: log, RoutesNote: routesNote,
	}
	if cfg.Demo {
		if err := demo.Seed(st, clk.Now(), cfg.Location, cfg.Defaults.Settings()); err != nil {
			st.Close()
			return nil, fmt.Errorf("seed demo data: %w", err)
		}
	}
	if err := svc.Bootstrap(); err != nil {
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
			return svc.PollEVCC(ctx)
		}
	}
	return &App{
		Cfg: cfg, Log: log, Store: st, Service: svc, Syncer: syncer, Stub: stub, Handler: srv.Handler(),
	}, nil
}

// StartJobs runs EVCC polling and calendar sync until ctx ends; wait blocks
// until they have stopped.
func (a *App) StartJobs(ctx context.Context) (wait func()) {
	var wg sync.WaitGroup
	every(ctx, &wg, a.Cfg.EVCC.PollInterval, func() {
		if err := a.Service.PollEVCC(ctx); err != nil && ctx.Err() == nil {
			a.Log.Warn("evcc poll failed", "err", err)
		}
	})
	every(ctx, &wg, a.Cfg.Calendar.SyncInterval, func() {
		if err := a.Syncer.SyncAll(ctx); err != nil && ctx.Err() == nil {
			a.Log.Warn("calendar sync incomplete", "err", err)
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
