# Agent guide

Instructions for AI coding agents working in this repository. Read
[PLAN.md](PLAN.md) for scope (V1 is implemented; later versions are not) and
[README.md](README.md) for configuration and deployment.

## Commands

```sh
make setup        # once: prerequisites, locked dependencies, Playwright Chromium
make dev          # app with demo data and stubs: UI http://127.0.0.1:7201, API 127.0.0.1:7202
make test         # fast: go test ./... and vitest
make verify       # everything: formatting, lint, types, unit/API/component tests, build, browser tests
make test-live    # read-only checks against the real EVCC (needs EVCC_URL, e.g. in .env)
make docker-smoke # production image build and smoke test (Docker required)
```

`make verify` must pass before any push: every commit on `main` is deployed
automatically. It needs no credentials or live services.

## Rules

- **Never commit private data**: no real addresses, IPs, iCal URLs, or keys.
  `config.yaml` uses loopback addresses; real values come from environment
  variables (`.env` locally, ignored by Git). Personal data lives in the UI/SQLite.
- **Never write to the real EVCC from tests.** Live tests are read-only; plan
  writes are only exercised against the stub.
- Add tests with every change: Go `testing` + `httptest` (fixed clocks, temp
  databases), Vitest + Vue Test Utils for components, Playwright for user flows
  (`web/e2e`, run against demo mode with a fixed clock and `/api/demo/reset`).
- Planning logic stays pure in `internal/planner`; time handling goes through
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
