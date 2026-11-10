// Command evcc-planner serves the trip planner API and UI and runs its
// periodic jobs (EVCC polling, hourly calendar sync, daily forecasts).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // timezone data for minimal container images

	"github.com/diogoap/evcc-planner/internal/app"
	"github.com/diogoap/evcc-planner/internal/config"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck())
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "evcc-planner:", err)
		os.Exit(1)
	}
}

// healthcheck probes /healthz on the configured port; the container image
// has no curl, so Compose runs `evcc-planner healthcheck`.
func healthcheck() int {
	listen := os.Getenv("EVCC_PLANNER_LISTEN")
	if listen == "" {
		listen = "127.0.0.1:7201"
	}
	_, port, err := net.SplitHostPort(listen)
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck:", err)
		return 1
	}
	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + port + "/healthz")
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck:", err)
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "healthcheck: status", resp.Status)
		return 1
	}
	return 0
}

func run() error {
	configPath := flag.String("config", "config.yaml", "system config file")
	localPath := flag.String("local", "config.local.yaml", "private config overlay (optional)")
	envPath := flag.String("env", ".env", "secrets file (optional; real environment variables win)")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	cfg, err := config.Load(config.Options{Path: *configPath, LocalPath: *localPath, EnvFile: *envPath})
	if err != nil {
		return err
	}
	if cfg.Fixed != nil {
		log.Warn("using a fixed clock", "time", cfg.Fixed.Format(time.RFC3339))
	}
	a, err := app.New(cfg, log)
	if err != nil {
		return err
	}
	defer a.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	wait := a.StartJobs(ctx)

	httpServer := &http.Server{
		Handler: a.Handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute,
	}
	ln, err := net.Listen("tcp", cfg.Server.Listen)
	if err != nil {
		stop()
		wait()
		return err
	}
	log.Info("evcc-planner listening", "addr", "http://"+ln.Addr().String(), "demo", cfg.Demo,
		"evcc", cfg.EVCC.Mode, "calendar", cfg.Calendar.Mode, "routes", a.Service.Routes.Router.Name(),
		"outlook", a.Service.Forecasts.Article.Name()+"/"+a.Service.Forecasts.Extractor.Name(), "db", cfg.DBPath())

	errc := make(chan error, 1)
	go func() { errc <- httpServer.Serve(ln) }()
	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			stop()
			wait()
			return err
		}
	case <-ctx.Done():
		log.Info("shutting down")
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Warn("http shutdown", "err", err)
	}
	stop()
	wait()
	log.Info("stopped")
	return nil
}
