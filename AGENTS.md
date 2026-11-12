# Agent guide

Instructions for AI coding agents working in this repository. Read
[PLAN.md](PLAN.md) for scope (V1–V4 are done; V5 is in progress) and [AUTOMATION.md](AUTOMATION.md) for the
rules of automatic plans and notifications, and
[README.md](README.md) for configuration and deployment.

## Commands

```sh
make setup        # once: prerequisites, locked dependencies, Playwright Chromium
make dev          # app with demo data and stubs: UI http://127.0.0.1:7201, API 127.0.0.1:7202
make test         # fast: go test ./... and vitest
make verify       # everything: formatting, lint, types, unit/API/component tests, build, browser tests
make test-live    # read-only checks against the real EVCC and Home Assistant (test inputs EVCC_URL, HA_URL, HA_TOKEN)
make docker-smoke # production image build and smoke test (Docker required)
```

`make verify` must pass before any push: every commit on `main` is deployed
automatically. It needs no credentials or live services.

## Rules

- **Never commit private data**: no real addresses, IPs, iCal URLs, or keys.
  `config.yaml` uses loopback addresses; real values come from environment
  variables (`.env` locally, ignored by Git). Personal data lives in the UI/SQLite.
- **Never write to the real EVCC or Home Assistant from tests.** Live tests are
  read-only; plan writes and notifications are only exercised against the stubs.
- Every change goes through the activity log: mutations use `Service.change`, which
  writes the change and its audit event in one transaction (see `internal/service/activity.go`);
  external HTTP clients use `audit.Transport`. Never put secrets in events.
- Forecast text is structured by a model, but numbers are never computed by it:
  keep quote verification and conversions in `internal/forecast`.
- Add tests with every change: Go `testing` + `httptest` (fixed clocks, temp
  databases), Vitest + Vue Test Utils for components, Playwright for user flows
  (`web/e2e`, run against demo mode with a fixed clock and `/api/demo/reset`).
- Planning and advice logic stay pure (`internal/planner`, `internal/advice`); time handling goes through
  `internal/localtime` (Europe/Amsterdam, DST-safe). Frontend formatting uses
  `web/src/lib/format.ts`, which always formats in the planner's timezone.
- Prices are always EUR/kWh including VAT.
- Go: standard library HTTP, `modernc.org/sqlite`, schema changes as new entries
  in `migrations` (`internal/store`). Frontend: Vue 3 `<script setup>` + TypeScript,
  Chart.js components registered selectively in `web/src/lib/chart.ts`.
- Match the surrounding style; `gofmt` and Prettier are enforced.

## Diagnosing failures

- Server: `GET /healthz`, `/readyz`, `/api/connections`; logs on stdout.
- Browser tests: `web/test-results/` (screenshots, traces), `web/playwright-report/`,
  server log path printed when the tests start.
- API tests in `internal/app/app_test.go` run the production wiring in demo mode;
  copy their pattern for new endpoints.
- On TLS-inspecting networks, browser and Docker downloads may need the macOS
  keychain CAs; `make setup` and `make docker-smoke` handle this.
