# EVCC Trip Planner

Have enough charge for upcoming trips by departure time. The planner combines
Google Calendar events, recurring trips (such as school runs), and manual trips;
calculates routes, energy, and the state of charge (SoC) needed at each
departure; and sends a charging plan to [EVCC](https://evcc.io), which does the
charging. See [PLAN.md](PLAN.md) for the roadmap and [UI-MOCKUP.md](UI-MOCKUP.md)
for the UI direction.

**V1:** trip UI, recurring schedules, calendar and EVCC connections,
route-based energy and SoC targets, feasibility, plan previews, and a
confirmed, manual **Send plan to EVCC**.

**V2:** daily forecast ingestion (Tibber's weekly price outlook structured
with OpenRouter, KNMI's weather outlook) and one advisory recommendation per
day (charge ahead, defer, or nothing), with confirmed **Create extra plan**
and **Review postponement** actions that are undone when they expire.
Optional auto-apply, off by default.

**V3:** an **Activity** page with the append-only activity
log (every change, EVCC write, and external API call, with before → after
values), history links from trips, schedules, and the EVCC plan, monthly
external API usage, CSV/JSON export, and a daily cleanup of history older than
90 days.

**V4 (this version):** an always-on SoC. Home Assistant has the car's state
of charge also while it is unplugged; the planner reads it (optional) and
shows where every SoC value comes from and how old it is. Integration
settings without secrets (addresses, sensors, intervals, model, retention)
are edited under Settings → Connections instead of config files.

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
| `make test-live` | Read-only checks against your real EVCC (state and, when plugged in, a plan preview), Home Assistant (the SoC entity, or a list of battery sensors to pick from), and the forecast sources (Tibber page; KNMI with a key; OpenRouter only with `LIVE_OPENROUTER=1`). |
| `make docker-smoke` | Build the production image, start it, check health/UI/API, report image size and idle memory. |
| `make run` | Production build, run locally with your real config. |
| `make fmt` / `make lint` / `make clean` | Format, lint, remove build output. |

## Configuration

| Location | Contents | In Git |
| --- | --- | --- |
| `config.yaml` | System settings: port, timezone, live or stub mode per integration, the names of the secret variables, the API base URLs that receive keys, the Home Assistant address default (loopback), planning defaults. | Yes |
| `.env` | Deployment values and secrets: `GOOGLE_CALENDAR_ICAL_URL`, `GOOGLE_MAPS_API_KEY`, and optionally `OPENROUTER_API_KEY`, `KNMI_API_KEY`, `HA_TOKEN`; `EVCC_URL` and `HA_URL` only as starting addresses. Copy `.env.example`. Real environment variables win. | No |
| `config.local.yaml` | Optional overrides of `config.yaml` values, and saved places to seed. Copy `config.local.example.yaml`. | No |
| `data/` | SQLite database with everything edited in the UI, including the connection settings. | No |

Other environment variables: `EVCC_PLANNER_LISTEN`, `EVCC_PLANNER_DATA_DIR`,
`EVCC_PLANNER_WEB_DIR`, `EVCC_PLANNER_DEMO`.

Personal data (home address, vehicle and buffer settings, saved places,
recurring trips) is edited in the UI and stored in SQLite; config values only
seed the vehicle and buffer defaults.

### Connection settings (Settings → Connections)

Integration settings that hold no secret are edited in the UI, stored in the
database, and applied at once, without a restart:

- **EVCC:** address, loadpoint, vehicle, poll interval (60 s), timeout.
  `EVCC_URL` only sets the starting address, until the form is saved.
- **Home Assistant:** address, the car's SoC, range, and reading-time
  sensors, poll interval (10 min), timeout. `HA_URL` only
  sets the starting address.
- **Google Calendar:** sync interval (1 h). **Google Routes:** traffic-aware
  routes, route cache (1 week), free calls per month (5,000).
- **Daily forecasts:** Tibber outlook page, OpenRouter model and timeout, KNMI
  dataset and version, daily fetch time (18:00), retry interval (1 h), VAT
  (21%).
- **History:** how many days to keep (90).

Each field shows its default; **Reset to defaults** fills them in again.
Saving stores every value that differs from the built-in defaults, including
addresses that came from `EVCC_URL` and `HA_URL`: after one save, those
variables are no longer needed by the app (`make test-live` still reads them
from `.env`).
Fields left at their built-in values keep following them. Saving checks EVCC
and Home Assistant again and is recorded in the activity log. Changing the EVCC
address, loadpoint, or vehicle waits until an active extra plan or
postponement has ended, because each one restores EVCC's previous setting
where it was made.

Secrets stay in environment variables: the API keys, the calendar
addresses, and `HA_TOKEN`; the Connections tab shows only whether each is
set. The Google Routes, OpenRouter, and KNMI base URLs, which receive the
keys, stay in `config.yaml`. The Home Assistant address is editable although
the token is sent to it: the planner has no login, so anyone who can open it
could point the address elsewhere and receive the token. Keep the planner
reachable only by people you trust (every change is in the activity log). Keys of
the old integration settings left in a `config.local.yaml` are ignored with
a warning in the log.

### Google Calendar (secret iCal address)

1. Google Calendar → Settings → select the calendar → **Integrate calendar** →
   copy **Secret address in iCal format**.
2. Set `GOOGLE_CALENDAR_ICAL_URL` to it. For several calendars, separate the
   addresses with commas. Names are read from the feeds; resetting a Google
   secret address keeps local trip edits.

The planner syncs hourly (and on **Sync calendar now**). Timed events become
trips unless they are online meetings or at home (listed as "not counted");
all-day events are skipped. Events without a location are shown disabled until
they get one. Unclear locations (no address and not a saved place) count toward
demand and show **Needs review**.

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

### Daily advice: OpenRouter and KNMI (optional)

- **OpenRouter** structures Tibber's weekly outlook article. Create a key at
  openrouter.ai and set `OPENROUTER_API_KEY`. The model (Settings →
  Connections) must support structured outputs. The planner downloads the page itself and
  sends only the article text (about 2,000 characters; no link, tools, or web
  access). The article changes weekly and an unchanged one is not sent again,
  so cost is a fraction of a cent a week.
  Without a key a labelled pattern-based stub reads only the article's summary
  lines (report period, weekly average, dearest and cheapest day).
- **KNMI**: register at the KNMI Developer Portal, request an **Open Data
  API** key in the API Catalogue (KNMI issues a separate key per API; an EDR,
  WMS, or Notification Service key is refused with "Access to this API has
  been disallowed"), and set `KNMI_API_KEY`. KNMI's seven-day outlook is
  national (no location): it gives wind, and sunshine for the days after
  EVCC's solar forecast. Without it the advice still works on prices.
- **Local sun** comes from the solar forecast configured in EVCC (today and
  tomorrow), read from EVCC's state; nothing to set up here.

Both are fetched once a day at the daily fetch time (18:00 by default; Settings →
Connections, which also has **Refresh forecasts**); failures retry hourly and
keep the last valid forecast.

### Home Assistant: always-on SoC (optional)

EVCC only reads the car's state of charge (SoC) while it is plugged in. If
Home Assistant has the car (for example through a Nissan Leaf integration),
the planner reads the SoC from it too, so it stays current while the car is
unplugged. It only reads, through Home Assistant's REST API.

1. In Home Assistant, create a long-lived access token: your profile (bottom
   left) → **Security** → **Long-lived access tokens** → **Create token**.
   Tokens cannot be limited to reading, so consider a separate, non-admin
   user just for the planner. Set `HA_TOKEN` to it.
2. In **Settings → Connections**, enter Home Assistant's address as the
   planner reaches it, e.g. `http://192.0.2.20:8123`, and save.
3. Then choose the car's battery level sensor (in %) as the SoC sensor, e.g. `sensor.leaf_battery_level`; the field
   suggests Home Assistant's battery sensors. Optionally choose the range
   sensor (km or mi).
4. If the integration has a timestamp sensor with when the car last
   reported, choose it as the reading-time sensor (the field suggests
   timestamp sensors). The reading is then dated to the car's own time,
   which is better than the SoC sensor's: an unchanged SoC is often not
   written again when the car reports. Without it, the reading is dated to
   when Home Assistant last received the value. `make
   test-live` lists the battery, range, and timestamp sensors with the
   reading each would give.

The planner reads Home Assistant every 10 minutes (adjustable in Settings →
Connections) and keeps each source's last reading. Settings → Connections shows the
latest one or the error. Wrong tokens are refused with 401; if Home
Assistant bans IPs after failed logins (`ip_ban_enabled`), fix the token
quickly or the planner's address gets banned. Calls to Home Assistant are on
your network and are not in the activity log.

## Install on your phone

The UI can be installed as an app (PWA). It needs HTTPS, e.g. through your
reverse proxy:

- **Android (Chrome):** open the site → menu → **Install app**.
- **iPhone (Safari):** open the site → Share → **Add to Home Screen**.

It always shows live data, so there is no offline mode. The icons are generated
from one design: `cd web && pnpm icons`.

## Deployment

`compose.yaml` builds the image from this repository and runs it.

1. Create the data folder once, owned by the container's non-root user
   (uid 65532), which cannot take ownership of a root-owned folder itself:
   `sudo install -d -o 65532 -g 65532 /opt/evcc-planner/data`
   (same as `sudo mkdir -p … && sudo chown 65532:65532 …`).
2. Provide `GOOGLE_CALENDAR_ICAL_URL` and `GOOGLE_MAPS_API_KEY` (optionally
   `OPENROUTER_API_KEY`, `KNMI_API_KEY`, and `HA_TOKEN`) as environment
   variables, then `docker compose up -d --build`. Enter the EVCC and Home
   Assistant addresses and everything else in Settings → Connections
   (`EVCC_URL` and `HA_URL` can provide the starting addresses).
3. Point a reverse proxy at port 7201. To put the container on the proxy's
   existing Docker network instead, also set `EVCC_PLANNER_NETWORK` to that
   network's name and `EVCC_PLANNER_NETWORK_EXTERNAL=true`; the proxy then
   reaches it as `http://evcc-planner:7201`.

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
  away period. A trip whose distance cannot be determined (no location, no
  route, or no home address) is shown disabled with the reason until it gets a
  destination or a manual distance.
- **The car is assumed plugged in whenever it is home.** Each home period
  charges at wall power × efficiency, minus a charging time buffer.
- **Targets:** each departure needs its own energy plus the arrival reserve, and
  enough to cover later trips the following home periods cannot recharge.
- **SoC:** while the car is plugged in, EVCC's live SoC always wins (it is
  what EVCC charges by); Home Assistant's is only shown next to it. After
  unplugging, EVCC's last reading (dated to the last moment EVCC saw the car
  plugged in) stays in use until Home Assistant has news from after that: a
  value that changed since, such as after a drive, or the same value
  reported again. An older value that Home Assistant reports again (a cloud
  integration that lagged while the car charged) never replaces it. Every
  SoC shows its source and age, with the other source's last reading below
  it; a reading that is not live and more than three hours old is marked
  "last known". Planned trips that left after the reading are subtracted.
- **Send plan to EVCC** writes the next departure's target (EVCC keeps one plan
  per vehicle) after you confirm. It never changes limits or repeating plans;
  EVCC's planner charges past the charge limit during planned slots. EVCC can
  only preview a plan while the car is plugged in.
- **When the car is plugged in** (unplugged before), the planner sends the
  next departure's target to EVCC once, unless EVCC already has a plan with
  an equal or higher SoC (kept even if it is ready later than the departure:
  someone chose it) or the charge already covers the departure. It is on
  by default (Settings → Home & vehicle); later changes in EVCC are left alone
  until the car is plugged in again.
- The arrow next to **Send plan to EVCC** offers plans that cover more days
  from the next departure's day: that day's trips, plus the next day, plus
  two days. Each target is enough to drive all those trips with no charging
  in between and still arrive home with the reserve, e.g. charging on a cheap
  night for tomorrow's and the next day's trips. It is still one plan, ready
  by the next departure.
- All prices are in EUR/kWh including VAT.

## How daily advice works

- **Prices, strongest first:** EVCC's published prices (today and, after
  ~14:00, tomorrow), then a dated estimate from the Tibber outlook, then only
  its weekly average. The model reports only values the article states, each
  with its exact quote; the server checks every quote against the article and
  converts market prices excl. VAT itself, using the article's own weekly
  excl./incl. pair: retail = weekly incl. + (day − weekly excl.) × 1.21. Each
  price shows how it was obtained (Settings → Connections).
- **One recommendation per day**, comparing the day's average price with the
  later days the car is home: **Charge ahead** when it is at least 8% cheaper
  than every later day and later trips will need charge; **Defer optional
  charging** when a later day is at least 8% cheaper and the projected SoC
  covers every departure until then; otherwise nothing (keep the plan). Days
  with only a weekly average, or nothing to compare with, show **No data / no
  suggestion**.
- **Sun:** EVCC's local solar forecast for today and tomorrow, in kWh for the
  day and while the car is home (before household use); later days show KNMI's
  national sunshine (% of possible). It is shown and explained, but does not
  change the recommendation.
- **Confidence** 1 low / 2 medium / 3 high follows the weaker of the two
  compared prices (published 3, estimate 2, weekly average 1), lowered for
  stale sources or KNMI wind that contradicts an estimate. The **charge
  indicator** (little / medium / high) reflects the price gap and later need;
  it never prescribes an amount. Low-confidence and no-data advice offer no
  plan action.
- **Create extra plan** opens an editable proposal (target in % or kWh,
  ready-by, expiry) with an EVCC preview. It replaces EVCC's one plan until it
  expires: the target is at least the next departure's and ready before it
  leaves. **Review postponement** (today only) pauses EVCC's cheap-grid
  charging (its smart cost limit) until the cheaper day; trip plans keep
  charging. Nothing is written before **Confirm**; **Dismiss** changes nothing.
- At expiry (or **End now**) the previous plan or limit is restored, unless it
  was changed in EVCC in the meantime; then your change is kept.
- **Automatically create plans from daily advice** (Settings → Advice) is off by
  default and needs a SoC target per indicator, a ready-by time, and an expiry.
  It acts once a day on today's advice with enough confidence, keeps trip
  targets, and skips plans you set in EVCC.

## Activity log and history

The **Activity** page lists, newest first, everything the planner did:

- **Changes:** trips added, edited, disabled, re-enabled, reset, deleted, or
  confirmed; recurring schedules created, edited, paused, resumed, or
  deleted; places and settings; plans sent to EVCC (with the plan they
  replaced and EVCC's response); advice dismissed; extra plans and
  postponements created, ended, and restored. Each shows only the values that
  changed (before → after) and is saved in the same database transaction as
  the change. Failed EVCC writes are recorded too; rejected input is not.
- **Source:** the UI, a scheduled job (e.g. restoring an EVCC setting at
  expiry), the calendar sync, or automation (plans created from advice).
- **External API calls:** every request to Google Routes (route and time slot;
  cache hits make no call), the calendar feeds, the Tibber page, OpenRouter,
  and KNMI, with HTTP status, duration, and size. Calls to EVCC and Home
  Assistant on your network are not logged; writes to EVCC are logged as changes. The page shows
  each month's calls per service, with Google Routes against its free
  allowance (free calls per month, Settings → Connections).

Filter by type, source, and dates; **History** on a trip, schedule, or the
EVCC plan opens just its entries. **Export CSV/JSON** downloads the filtered
log. Entries are never edited. Calendars appear by name and ID; API keys,
secret iCal addresses, and signed download links are never recorded.

Once a day the planner deletes history older than the retention in Settings →
Connections (90 days by
default): activity entries, manual trips that have ended, per-date edits of
past dates, cached routes, plan records (keeping the latest), advice
dismissals, and ended EVCC changes. Schedules, places, and settings are kept;
calendar events only ever cover the next eight days.

## Diagnostics

- Logs go to stdout (request, sync, and EVCC poll lines): `docker logs evcc-planner`.
- The image has no shell. For one beside the app (same network, data at `/data`):
  `docker run --rm -it --network container:evcc-planner -v /opt/evcc-planner/data:/data busybox sh`
- `GET /healthz`, `GET /readyz`, `GET /api/connections` (integration status, no secrets),
  `GET /api/forecasts` (the stored outlook and weather payloads),
  `GET /api/activity` (the activity log, including failed external calls).
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
internal/advice    pure daily charging advice
internal/audit     activity log support: request source, redaction, auditing HTTP transport
internal/forecast  Tibber outlook extraction (OpenRouter or pattern stub), validation, KNMI outlook
internal/trips     merges manual, recurring, and calendar trips with local edits
internal/calendar  iCal parsing, trip classification, sync, demo feed
internal/routes    Google Routes client, stub, cache
internal/evcc      EVCC client and stub
internal/homeassistant  Home Assistant SoC reader and stub
internal/service   use cases and API view models
internal/server    HTTP API and static UI
internal/store     SQLite persistence and migrations
web/               Vue 3 + TypeScript UI, Vitest and Playwright tests
```
