//go:build !windows

// Command devrun is the `make dev` process manager: it builds and runs the Go
// backend (restarting it when Go or config files change) and the Vite dev
// server with hot reload, and stops both cleanly on Ctrl+C.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	uiAddr  = "127.0.0.1:7201"
	apiAddr = "127.0.0.1:7202"
	binary  = "bin/evcc-planner-dev"
)

func main() {
	demo := flag.Bool("demo", true, "use seeded demo data and stubbed integrations")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if _, err := os.Stat("web/node_modules"); err != nil {
		fmt.Fprintln(os.Stderr, "web dependencies missing; run `make setup` first")
		os.Exit(1)
	}

	env := append(os.Environ(), "EVCC_PLANNER_LISTEN="+apiAddr, "EVCC_PLANNER_WEB_DIR=web/dist")
	if *demo {
		env = append(env, "EVCC_PLANNER_DEMO=true")
	}

	var wg sync.WaitGroup
	// Run Vite directly (not via pnpm) so stopping it prints no lifecycle error.
	vite := exec.Command("node_modules/.bin/vite")
	vite.Dir = "web"
	web := start(ctx, "web", vite, nil)
	wg.Add(1)
	go func() { defer wg.Done(); web.Wait() }()

	var api *exec.Cmd
	restart := func() {
		if api != nil {
			terminate(api)
			api = nil
		}
		logf("api", "building backend")
		build := exec.Command("go", "build", "-o", binary, "./cmd/evcc-planner")
		build.Stdout, build.Stderr = os.Stdout, os.Stderr
		if err := build.Run(); err != nil {
			logf("api", "build failed; fix the error and save to retry")
			return
		}
		api = start(ctx, "api", exec.Command(binary), env)
		go waitReady(ctx, *demo)
	}
	restart()

	last := snapshot()
	tick := time.NewTicker(700 * time.Millisecond)
	defer tick.Stop()
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case <-tick.C:
			if now := snapshot(); now != last {
				last = now
				logf("api", "change detected; restarting")
				restart()
			}
		}
	}

	logf("dev", "stopping")
	if api != nil {
		terminate(api)
	}
	terminate(web)
	wg.Wait()
}

// snapshot fingerprints Go sources and config files by path, size, and mtime.
func snapshot() string {
	var b strings.Builder
	for _, root := range []string{"cmd", "internal"} {
		filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			if info, err := d.Info(); err == nil {
				fmt.Fprintf(&b, "%s:%d:%d;", path, info.Size(), info.ModTime().UnixNano())
			}
			return nil
		})
	}
	for _, f := range []string{"go.mod", "go.sum", "config.yaml", "config.local.yaml", ".env"} {
		if info, err := os.Stat(f); err == nil {
			fmt.Fprintf(&b, "%s:%d:%d;", f, info.Size(), info.ModTime().UnixNano())
		}
	}
	return b.String()
}

// start runs cmd in its own process group with prefixed output.
func start(ctx context.Context, name string, cmd *exec.Cmd, env []string) *exec.Cmd {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if env != nil {
		cmd.Env = env
	}
	out, _ := cmd.StdoutPipe()
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		logf(name, "failed to start: %v", err)
		return cmd
	}
	go prefix(name, out)
	return cmd
}

func prefix(name string, r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		fmt.Printf("[%s] %s\n", name, sc.Text())
	}
}

// terminate stops the whole process group: SIGTERM, then SIGKILL after 5s.
func terminate(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pgid := cmd.Process.Pid
	syscall.Kill(-pgid, syscall.SIGTERM)
	done := make(chan struct{})
	go func() { cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		syscall.Kill(-pgid, syscall.SIGKILL)
		<-done
	}
}

func waitReady(ctx context.Context, demo bool) {
	client := http.Client{Timeout: time.Second}
	for i := 0; i < 60 && ctx.Err() == nil; i++ {
		if resp, err := client.Get("http://" + apiAddr + "/readyz"); err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				mode := "demo data, stubbed integrations"
				if !demo {
					mode = "your .env"
				}
				logf("dev", "ready: open http://%s (%s)", uiAddr, mode)
				return
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func logf(name, format string, args ...any) {
	fmt.Printf("[%s] %s\n", name, fmt.Sprintf(format, args...))
}
