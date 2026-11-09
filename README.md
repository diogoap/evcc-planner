# EVCC Trip Planner

Have enough charge for upcoming trips by departure time. The planner combines
Google Calendar events, recurring trips (such as school runs), and manual trips;
calculates routes, energy, and the state of charge (SoC) needed at each
departure; and sends a charging plan to [EVCC](https://evcc.io), which does the
charging. See [PLAN.md](PLAN.md) for the roadmap and [UI-MOCKUP.md](UI-MOCKUP.md)
for the UI direction.

**V1 (this version):** trip UI, recurring schedules, calendar and EVCC
connections, route-based energy and SoC targets, feasibility, plan previews,
and a confirmed, manual **Send plan to EVCC**.

## Quick start

Requires Go 1.27+, Node 22.12+, and pnpm.

```sh
make setup   # check prerequisites, install locked dependencies and the test browser
make dev     # demo data + stubbed integrations; open http://127.0.0.1:7201
```

`make dev` needs no API keys. It runs the Go API on 127.0.0.1:7202 (rebuilt and
restarted when Go or config files change) and the Vite UI on 127.0.0.1:7201 with
hot reload. Ctrl+C stops both. Demo data lives in `data/demo.db`; `make dev-reset`
deletes it.

## Commands

| Command | Purpose |
| --- | --- |
| `make setup` | Check Go/Node/pnpm, install locked Go and web dependencies, install Playwright Chromium. |
| `make dev` | Local app with seeded, editable demo data and labelled stubs. |
| `make dev-live` | Local app with your `.env` (real EVCC, calendar, routes). |
| `make verify` | Go format/vet/tests, web format/lint/types/tests, production build, browser tests. Must pass before pushing. |
| `make test` | Fast Go and web unit tests only. |
| `make e2e` | Build, then run the Playwright browser tests (desktop and phone). |
| `make test-live` | Read-only checks against your real EVCC (state and, when plugged in, a plan preview). |
| `make docker-smoke` | Build the production image, start it, check health/UI/API, report image size and idle memory. |
| `make run` | Production build, run locally with your real config. |
| `make fmt` / `make lint` / `make clean` | Format, lint, remove build output. |

## Configuration

| Location | Contents | In Git |
| --- | --- | --- |
| `config.yaml` | System settings: port, timezone, EVCC URL (loopback default), routing, planning defaults. | Yes |
| `.env` | Deployment values and secrets: `EVCC_URL`, `GOOGLE_CALENDAR_ICAL_URL`, `GOOGLE_MAPS_API_KEY`. Copy `.env.example`. Real environment variables win. | No |
| `config.local.yaml` | Optional overrides of `config.yaml` values, and saved places to seed. Copy `config.local.example.yaml`. | No |
| `data/` | SQLite database with everything edited in the UI. | No |

Other environment variables: `EVCC_PLANNER_LISTEN`, `EVCC_PLANNER_DATA_DIR`,
`EVCC_PLANNER_WEB_DIR`, `EVCC_PLANNER_DEMO`.

Personal data (home address, vehicle and buffer settings, saved places,
recurring trips) is edited in the UI and stored in SQLite; config values only
seed the vehicle and buffer defaults.

### Google Calendar (secret iCal address)

1. Google Calendar → Settings → select the calendar → **Integrate calendar** →
   copy **Secret address in iCal format**.
2. Set `GOOGLE_CALENDAR_ICAL_URL` to it. For several calendars, separate the
   addresses with commas. Names are read from the feeds; resetting a Google
   secret address keeps local trip edits.

The planner syncs hourly (and on **Sync calendar now**). Timed events with a
physical location other than home become trips; all-day events, online meetings,
and events without a location are listed as "not counted". Unclear locations
(no address and not a saved place) count toward demand and show **Needs review**.

### Google Routes API

1. In your Google Cloud project, enable **Routes API** (APIs & Services → Library).
   A billing account must be linked even when staying within the free usage.
2. Create an API key and restrict it to the Routes API.
3. Optional safety net: under Quotas, cap Routes API requests per day (e.g. 200).
4. Set `GOOGLE_MAPS_API_KEY`.

The planner uses traffic-aware routing (billed as Compute Routes Pro, 5,000 free
calls per month) and caches each route per weekday and half hour for a week, so
recurring trips reuse their result. Expect a few hundred calls per month. Without
a key it falls back to labelled stub distances.

## Deployment

`compose.yaml` builds the image from this repository and runs it.

1. Create the data folder once, owned by the container's non-root user
   (uid 65532), which cannot take ownership of a root-owned folder itself:
   `sudo install -d -o 65532 -g 65532 /opt/evcc-planner/data`
   (same as `sudo mkdir -p … && sudo chown 65532:65532 …`).
2. Provide `EVCC_URL`, `GOOGLE_CALENDAR_ICAL_URL`, and `GOOGLE_MAPS_API_KEY` as
   environment variables, then `docker compose up -d --build`.
3. Point a reverse proxy at port 7201.

The host folder `/opt/evcc-planner/data` is mounted at the same path in the
container and keeps the SQLite database (`planner.db`) across restarts and
rebuilds. To back it up while running, copy `planner.db` together with its
`-wal` and `-shm` files, or use `sqlite3 /opt/evcc-planner/data/planner.db
".backup /path/to/backup.db"`. The container listens on 0.0.0.0:7201 and reports
health via `/healthz` (liveness) and `/readyz` (database ready). A
`config.local.yaml` or `.env` placed in the data folder is also read. If commits
to `main` deploy automatically, run `make verify` before pushing.

## How planning works

- **Times are arrive-by times.** Departure = arrival − travel time − departure
  buffer. The return leg leaves at the trip's end time. Everything is scheduled
  in Europe/Amsterdam, including across daylight-saving changes.
- **Energy** = round-trip km × consumption. Trips that overlap merge into one
  away period.
- **The car is assumed plugged in whenever it is home.** Each home period
  charges at wall power × efficiency, minus a charging time buffer.
- **Targets:** each departure needs its own energy plus the arrival reserve, and
  enough to cover later trips the following home periods cannot recharge.
- **SoC:** EVCC only reports SoC while the car is plugged in. The planner stores
  the last reading and shows it as "last known" with its time; trips driven since
  are subtracted.
- **Send plan to EVCC** writes the next departure's target (EVCC keeps one plan
  per vehicle) after you confirm. It never changes limits or repeating plans;
  EVCC's planner charges past the charge limit during planned slots. EVCC can
  only preview a plan while the car is plugged in.
- All prices are EVCC's published prices, including VAT.

## Diagnostics

- Logs go to stdout (request, sync, and EVCC poll lines).
- `GET /healthz`, `GET /readyz`, `GET /api/connections` (integration status, no secrets).
- Browser test failures: screenshots and traces in `web/test-results/`, HTML
  report in `web/playwright-report/`, server log path printed at the start.
- On TLS-inspecting networks, `make setup` and `make docker-smoke` retry
  downloads with the macOS keychain certificates; `make docker-smoke` also uses
  the host's npm registry.

## Layout

```text
cmd/evcc-planner   server entry point (and `healthcheck` subcommand)
cmd/devrun         `make dev` process manager
internal/app       wiring shared by the server and API tests
internal/planner   pure SoC/energy/feasibility planning
internal/trips     merges manual, recurring, and calendar trips with local edits
internal/calendar  iCal parsing, trip classification, sync, demo feed
internal/routes    Google Routes client, stub, cache
internal/evcc      EVCC client and stub
internal/service   use cases and API view models
internal/server    HTTP API and static UI
internal/store     SQLite persistence and migrations
web/               Vue 3 + TypeScript UI, Vitest and Playwright tests
```
