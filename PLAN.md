# EVCC Trip Planner

Have enough charge for upcoming trips by departure time, using the cheapest practical mix of grid energy and solar.

**Decisions.**

- Deterministic service with **native local development** and **Docker production packaging**: calculate trip demand and send SoC/departure targets to EVCC, which controls charging. V2 uses AI to structure external forecast text.
- Planner targets **always override the 10% minimum and 80% default limit**, without approval. EVCC's planner charges past the limit during planned slots (checked in EVCC source), so plan writes need no limit change; synchronize limits only if live validation shows otherwise. Physical vehicle/charger limits still apply.
- **Hourly Google Calendar sync** via each calendar's **secret iCal address** (read-only; no OAuth). Manual edits apply immediately; local EVCC status/deadline checks can run more frequently.
- **No app authentication or user accounts**; use existing network authentication. Calendar iCal URLs and external API keys remain server-side.
- Schedule in **Europe/Amsterdam**, respecting daylight-saving changes.
- Beautiful, simple, responsive **EVCC-inspired UI**: clear cards, restrained colors, useful graphs, and a rolling **seven-day plan**. Distinguish published prices, estimates, and missing data.
- **Only use/show energy prices including VAT**, in €/kWh, throughout calculations, recommendations, charts, and notifications. EVCC prices are confirmed to include VAT. Normalize other sources to the same retail basis; unclear conversions remain unknown.
- **Daily advice is advisory by default**: offer **Create extra plan** for charge-ahead advice, followed by a proposal and confirmation. Optional advice auto-apply is a separate setting, **off by default**; enabling it requires explicit target/expiry rules because qualitative indicators alone do not define a charging amount. Automatic trip/deadline plan management starts in V5. Every action is auditable from V3.

**Context (2026-11-09).** Local EVCC API (`/api/state`; LAN address kept in private config): SoC, connection, prices, solar forecast. EVCC 0.319.0; Alfen Eve Pro charger (6–16 A); Leaf configured with 62 kWh gross; 15-minute price slots; optimizer automatic control off; Home Assistant connected.

- Defaults: **59 kWh usable** and **3.68 kW** (Leaf charges single-phase at 16 A; about 3.3 kW into the battery at 90% efficiency). Validate both live.
- Vehicle SoC comes from EVCC's Leaf integration and is **only available while plugged in**; EVCC reports 0 when unplugged. Home Assistant always has it, and the planner reads it from there since V4.
- EVCC plan endpoints need no login. Each vehicle holds **one one-off SoC plan** (`POST /api/vehicles/{name}/plan/soc/{soc}/{timestamp}`) plus optional repeating plans (none configured). Previews: `GET /api/loadpoints/{id}/plan/static/preview/soc/{soc}/{timestamp}`.

Background EVCC preferences: cheap-grid charging and solar/export-price priority. Typical thresholds discussed: €0.15/€0.30 per kWh; snapshot: €0.20/€0.40. Trip requirements take priority.

**Confirmed lightweight stack.** Go standard-library HTTP server, Vue 3 + TypeScript + Vite, SQLite (`modernc.org/sqlite`), and selectively imported Chart.js. In production, one Go process serves the API and compiled UI in one Docker container; SQLite uses a persistent volume. Node is needed for frontend development/builds. Periodic jobs run inside the app.

**Configuration and execution.**

| Location | Contents / Git policy |
| --- | --- |
| `config.yaml` | Tracked system settings: port **7201**, timezone, integration modes, secret variable names, the API base URLs that receive keys, the Home Assistant address default, notification provider/connection defaults. Service addresses use `127.0.0.1` with the real ports; other non-sensitive values are allowed. Integration settings without secrets are edited in the UI (Settings → Connections) since 2026-11-11. |
| `config.local.yaml` | Optional ignored overrides of `config.yaml` values and bootstrap trip defaults/places. Personal values saved through the UI take precedence. |
| `.env` | Ignored deployment values and secrets, provided as environment variables in production: `EVCC_URL` (optional starting EVCC address, not needed once saved in the UI), `GOOGLE_CALENDAR_ICAL_URL` (one or more secret iCal addresses; calendar names come from the feeds), Google Maps (Routes API) key, OpenRouter key, KNMI key, Home Assistant token (`HA_TOKEN`; `HA_URL` only as the starting address). Server-side only. |
| `data/` | Ignored persistent SQLite database/cache: UI-managed home address, trip defaults, connection settings, recurring templates/occurrences, overrides, advice/automation choices, and notification preferences. Example config/ENV files contain safe placeholders for private values. |

- Exclude private config, ENV, and runtime data from Git and Docker build context; supply them at runtime.
- Native development is the default: the UI on **127.0.0.1:7201** (Vite) with the Go API on 127.0.0.1:7202. One command manages backend/frontend startup, reload, and shutdown. The Docker app listens on **0.0.0.0:7201**, reached by existing NGINX using IP/port. No fixed hostname required.
- Production runs as a **Portainer stack** on a Proxmox Docker VM (x86_64 → `linux/amd64`; Docker 29.3.1, Compose v5.1.1; reaches EVCC). Portainer tracks `refs/heads/main` of the public [GitHub repo](https://github.com/diogoap/evcc-planner), polls every 15 minutes, and redeploys `compose.yaml` on every new commit (same setup as finance-control). `compose.dev.yaml` runs the same image locally for `make docker-smoke`. The VM builds the image from the checked-out repo (`build.context: .`, with `pull_policy: build` to guarantee a rebuild). Real service addresses and secrets are passed through as Portainer stack environment variables, which override config files. SQLite lives in a host bind mount at `/opt/evcc-planner/data`; the service restarts `unless-stopped`. Every push to `main` deploys, so push only after `make verify` passes, and never commit private data.
- Google Routes supplies road distances/travel times through the **Routes API** with a plain API key: traffic-aware travel times for the planned departure, cached per route and time slot so usage stays far below the free monthly cap (5,000 traffic-aware calls). OpenRouter supplies AI inference. Missing integrations can use explicitly labelled stubs with the same payload contracts, including inferred Tibber/KNMI fixtures. Real Tibber articles may be fetched while inference is stubbed; preserve report dates.
- Personal/planning settings and integration settings without secrets (EVCC and Home Assistant addresses, EVCC loadpoint/vehicle, Home Assistant sensors, intervals, timeouts, route cache, model, forecast schedule, VAT, retention) are editable in the app (decided 2026-11-11), applied without a restart, and audited. Secrets stay in environment variables and the API base URLs that receive keys stay in config; the UI shows only whether each secret is set. The Home Assistant address is editable although the token is sent to it (decided 2026-11-11): with no app login, this relies on network access control, and changes are in the activity log. Connection status is visible in the UI. Notification enablement, destination selection where configured, quiet hours, and reminder preferences are UI-managed; transport URLs/credentials remain server-configured.

**UI direction.** EVCC-inspired cards, clear typography, generous spacing, restrained color, and responsive layouts. Navigation: **Overview**, **Trips**, **Recurring trips**, **Settings**, plus **Activity** (audit trail) from V3. Overview combines EVCC status, the seven-day plan, and daily advice. Use graphs for published prices, planned charging, and projected SoC where useful; represent uncertain long-range advice with labelled signals. See [text UI mockup](UI-MOCKUP.md).

**Local development and AI-agent verification.** Planned commands, to implement with the app:

| Command | Purpose |
| --- | --- |
| `make setup` | Check Go/Node prerequisites; install locked project dependencies and browser-test tooling. |
| `make dev` | Start the complete app locally with editable, seeded seven-day demo data and labelled integration stubs. No API keys required. |
| `make verify` | Run formatting/lint/type checks, unit/API/component tests, production build, and browser end-to-end tests; fail clearly on errors. |
| `make test-live` | Run separately configured live integration checks, initially EVCC state/plan previews. |
| `make docker-smoke` | Build and check the production container; Docker is optional for daily development. |

- Add tests with every feature: Go `testing` + `net/http/httptest`, Vitest + Vue Test Utils, and Playwright. Cover energy/SoC, departures/DST, recurring-trip exceptions, VAT/price precedence, daily recommendations, and UI edits.
- Verification uses fixed clocks, fixtures, isolated databases, and stubbed external services. After dependency setup, the standard checks run without credentials or live services.
- Browser tests start/stop their own app, verify real user flows and persistence, and save reports/screenshots. Provide a health/readiness endpoint, useful logs, and reliable process cleanup so agents can diagnose failures independently.
- Test UI settings persistence, separate trip/advice automation settings, advice auto-apply **off by default**, and proposal confirmation before manual EVCC writes.
- Keep exact setup/run/test commands in README and repository AGENTS.md. Measure Docker image size and idle memory during packaging validation.

**Planning rules.**

- Combine calendar, recurring, and manual trips. Support cancellations, exclusions, and location aliases. Holidays come from the calendar; no separate school-holiday import.
- Calendar trips: timed events on configured calendars with a physical location other than home. Ignore all-day events and video-link locations. Unclear events show **Needs review** and count toward demand until disabled.
- Materialize recurring trips across the seven-day window; extend it daily. Edit/disable each occurrence independently. Preserve exceptions across regeneration; template edits update future unmodified occurrences.
- Trip edits/disabled states stay local and survive calendar syncs. Recalculate demand immediately.
- Every trip time is an **arrive-by time**: the calendar event start, or the time on a manual/recurring trip (school drop-off 08:15 means at school by 08:15). Departure = arrival time minus travel time and buffer.
- Include return legs and earlier trips. Every trip, whatever its source, has a start (arrive-by) and an end time; the **return leg starts at the end time**. New manual/recurring trips default to a 15-minute stay.
- Assume the car is **plugged in whenever it is home**: every home period is a charging opportunity at achievable power.
- Configure consumption, usable capacity, and arrival reserve. Include charging losses, achievable power, and a time buffer.
- Keep the full departure SoC target. Solar changes timing; count only surplus after household use, before departure, while the car is home.
- Refresh prices around 14:00; verify forecast coverage and retry missing data.
- Respect explicit manual plans/pauses. Retain the last valid managed plan during failures; flag stale SoC and impossible deadlines.
- Store the last SoC of each source (EVCC, Home Assistant from V4) with its timestamp. While unplugged, plan with the newest and show its source and date/time; mark old readings as last known.

Example: 100 km total at 18 kWh/100 km, 59 kWh usable capacity, and a configurable 15% arrival reserve requires about **46% departure SoC**.

<details>
<summary><b>V1 — Trip UI and plan previews</b> ✅</summary>

✅ **Done** (2026-11-09).

- Connect Calendar (secret iCal) and EVCC; add manual trips and a **Recurring trips** section to create/edit/disable schedules such as school runs.
- Add UI settings for home address, trip defaults, and recurring-trip management; save personal data privately in SQLite.
- Calculate routes, targets, charging duration, and feasibility.
- UI: EVCC status (SoC, connection, charging power), seven-day agenda, and plan previews. When unplugged, show the last known SoC with a small tip that it is not current and when it was read. Trip cards show source, arrival and departure times, route, distance **(km)**, estimated energy **(kWh)**, and edit/disable controls. Add a demand/SoC timeline where useful.
- **Send plan to EVCC** is user-triggered and needs confirmation. The confirmation shows the target SoC, deadline, EVCC preview, and any existing EVCC plan it replaces. EVCC holds one plan per vehicle, so V1 sends the next departure's target. Record app-written plans; never change repeating plans or limits. Automatic plan sync stays in V5.

</details>

<details>
<summary><b>V2 — Daily forecast ingestion and seven-day advice</b> ✅</summary>

✅ **Done** (2026-11-10).

- **Tibber, once daily:** fetch the unstructured [weekly outlook](https://tibber.com/nl/stroomprijsprognose), extract the article, and call **OpenRouter** with a schema-supporting model to produce a fixed, versioned JSON payload. The article updates weekly; preserve its report period separately from fetch time.
- **Payload contract:** source URL, issue/fetch times, validity period, weekly outlook, dated daily estimates/trends, wind/solar signals, confidence, and supporting text. Usable prices must be retail €/kWh including VAT, with verified conversion provenance. Require strict JSON Schema output and server-side validation; missing values stay null, without invented daily/hourly prices.
- **KNMI, once daily:** fetch wind, sunshine/radiation, and temperature forecasts; normalize into dated weather records. Select a dataset with the required horizon and show gaps if seven-day coverage is unavailable. Use market-relevant wind/solar for price signals and local sunshine for PV.
- User supplies **OpenRouter and KNMI API keys**, stored server-side as configuration/secrets.
- **Price precedence:** published EVCC/Tibber prices for today/tomorrow take priority over weekly/weather estimates for those dates. Recalculate advice when published prices, SoC, or trips change; Tibber/OpenRouter and KNMI ingestion stay daily.
- Produce one advisory recommendation for each date in the seven-day view, considering prices, solar, current/projected SoC, trip demand, and charging opportunities:
- Keep advice qualitative, with two independent signals: **confidence 1 = low, 2 = medium, 3 = high**, and **charge indicator = little / medium / high (more)**. Indicators do not prescribe fixed kWh/SoC amounts; the user decides how much to charge.

| Recommendation | Meaning |
| --- | --- |
| **Nothing** (`none`) | Keep the scheduled plan, or keep no plan when none is needed. For weak/missing forecasts, show **No data / no suggestion** with the reason and no charge indicator. |
| **Charge extra/ahead** (`charge_ahead`) | That day's useful charging slots are cheaper than following days; show a qualitative charge indicator. Prefer published next-day prices whenever available. |
| **Defer/postpone** (`defer`) | Lower prices are expected ahead and current/projected SoC covers demand until a later feasible charging opportunity. Required departure targets remain binding. |

- UI: each day shows its recommendation, confidence level, qualitative charge indicator, rationale, wind/sun, and source freshness. Clearly label estimated prices and weak/stale/missing inputs; insufficient evidence produces no charging suggestion.
- By default, **Create extra plan** opens an editable charge-ahead proposal with the SoC/energy target, deadline, and expiry; **Review postponement** does the same for deferral. Require confirmation before EVCC writes. Track and restore app-owned temporary settings when these plans end, preserving intervening manual changes. **Dismiss** makes no change; weak/no-data advice has no plan action.
- Settings expose optional **Automatically create plans from daily advice**, off by default and independent of V5 trip automation. Auto-apply requires user-configured concrete target/expiry policies, honours trip requirements/manual pauses, and takes no action on weak/no-data advice. Its availability does not delay the manual V2 flow.

As implemented (2026-11-10):

- KNMI: the Open Data API dataset `outlook_weather_forecast` (the meteorologist's national seven-day outlook from today, no location: sunshine %, rain, temperature ranges, wind direction/Beaufort), parsed directly from its XML; no AI needed. Missing days show as gaps.
- Local sun: EVCC's own solar forecast (`forecast.solar` in its state; today and tomorrow), integrated per day and over the hours the car is home. Later days fall back to KNMI's national sunshine, labelled as such.
- Tibber: the article is cut out of the page deterministically and only that text is sent (no link or web tools); the model returns only stated values with exact quotes (strict JSON Schema), and the server rejects quotes not in the article. Excl. market prices are converted with the article's own weekly excl./incl. pair: retail = weekly incl. + (day − weekly excl.) × (1 + VAT). Unchanged articles are not sent to the model again. Without an OpenRouter key a labelled pattern-based stub reads the fixed summary lines.
- Daily price level: published day average, else the day's estimate, else the weekly average (only usable as a comparison, never as a day's own basis). Recommendations need an 8% gap; confidence follows the weaker compared basis, lowered for stale or contradicting inputs; confidence 1 offers no plan action.
- **Create extra plan** writes a one-off SoC plan (at least the next departure's target, ready before it leaves) and **Review postponement** (today only) removes EVCC's loadpoint smart cost limit (cheap-grid charging) until the cheaper day. Both record the value they replaced and restore it at expiry unless it changed in EVCC meanwhile.

</details>

<details>
<summary><b>V3 — Audit trail</b> ✅</summary>

✅ **Done** (2026-11-10).

- Record every action as an append-only audit event: time, source (UI, scheduled job, calendar sync, automation), action, subject (trip, schedule, place, setting, plan), before/after values, and outcome (success or failure with the error). Write the event in the same transaction as the change where possible.
- **App actions:** EVCC plan submissions (target, deadline, the plan it replaced, EVCC's response), manual trips added/edited/deleted, trips or dates disabled/re-enabled/edited/reset, calendar trips confirmed, recurring schedules created/edited/paused/deleted, places and settings changes, and later advice and automation decisions.
- **External API calls:** every call to a non-local service, e.g. Google Routes (route and time slot; real calls only, not cache hits), calendar feed fetches, and later OpenRouter, KNMI, and notification providers, with status, duration, and result size. Local EVCC and Home Assistant traffic is not logged per call; writes to EVCC are logged as actions.
- Never store secrets: identify calendars by name/ID, never by their secret URL, and redact keys and tokens.
- UI: an **Activity** page in the navigation, newest first, with filters by type, source, and date, and before→after details. Link trips and plans to their history. Show monthly external API usage, e.g. Google Routes calls against the free cap.
- Configurable retention (default 90 days, decided 2026-11-10), pruned daily together with past trips, per-date edits, route cache, plan records, and advice decisions; export as CSV/JSON.
- Tests: each mutation produces exactly one event with the correct change; no secret ever appears in events.

As implemented (2026-11-10):

- One `audit_events` table; a trigger rejects updates. Each change and its event commit in one SQLite transaction; input errors (400/404) are not recorded, failed EVCC writes are. Events hold only changed fields (creations only "after", deletions only "before") as readable local values.
- Source comes from the request context: API requests are UI; jobs mark theirs (scheduled job, calendar sync, automation for advice auto-apply). Restores at expiry are scheduled-job events.
- External calls are recorded by an HTTP transport on each client (Routes, calendar, Tibber, OpenRouter, KNMI): status, duration, response size; never query strings, and only the host for secret iCal addresses. Configured keys and calendar URLs, bearer tokens, and signed URLs are redacted from every stored text.
- History: an event's subject (`trip:<key>`, `schedule:<id>`, `place:<id>`, `plan`) plus a related subject (a date's edits belong to its schedule's history; plan sends to their trip's; extra plans to the plan's).
- Retention: 90 days (Settings → Connections since 2026-11-11); one daily cleanup also deletes ended manual trips, per-date edits of past dates (and of calendar events that are gone), cached routes, older plan records, advice dismissals, and ended EVCC changes. Google Routes' free allowance for the usage card: 5,000 (also in Settings → Connections).

</details>

<details>
<summary><b>V4 — Always-on SoC</b> ✅</summary>

✅ **Done** (2026-11-11).

- **Always-on SoC:** read the Leaf's SoC from Home Assistant, which has it even when the car is unplugged, using the HA token in `.env`. Fall back to EVCC, and show the source and freshness of every SoC value.

As implemented (2026-11-11):

- Home Assistant's REST API, read-only (`GET /api/states/<entity>`) with a long-lived access token. Environment: `HA_TOKEN`. The address (seeded by `HA_URL`), the SoC sensor (battery level, %) and optional range sensor (km or mi) are chosen in Settings → Connections, which suggests Home Assistant's battery and distance sensors. Polled every 10 minutes; local calls are not in the activity log, and the token is redacted like other secrets. Without a token or sensor, only EVCC is used and Connections says what is missing.
- A reading's time is when Home Assistant last received the value (`last_reported`, else `last_updated`), or the car's own time from an optional reading-time attribute (e.g. `updated_on`), never later than Home Assistant received it. Unknown, unavailable, 0%, and non-% values are rejected.
- Each source keeps its last reading. **While plugged in, EVCC's live SoC always wins** (it is what EVCC charges by; decided 2026-11-11). After unplugging, EVCC's last reading, dated to the last poll that saw the car plugged in, stands until Home Assistant has news from after it: a value that changed since (Home Assistant's `last_changed`, or the car's own reading time), or the same value reported again. A lagging cloud value reported again after unplugging never replaces it. Planned trips that left after the chosen reading are subtracted, as before.
- UI: the vehicle card shows the source and age ("From Home Assistant · 20 min ago"), the other source's last reading below it, and "last known" for readings that are not live and over three hours old. Settings → Connections shows Home Assistant's latest reading or error. Demo mode stubs Home Assistant: the demo car's SoC, 20 minutes behind, also while unplugged. `make test-live` lists the battery and range sensors with the reading each would give.

</details>

<details open>
<summary><b>V5 — Automatic plans and interaction</b> 🚧</summary>

🚧 **In progress.**

Done:

- ✅ **Foundational rule: plan on plug-in** (implemented early, 2026-11-11). When the car goes from unplugged to plugged in, send the next departure's own target to EVCC once, ready by that departure. Keep a plan already in EVCC with an equal or higher SoC, even when its ready-by time is after the next departure: someone chose that plan, so the planner respects it (decided 2026-11-11). Replace only a plan with a lower SoC than the departure needs, and send nothing when the current charge already covers the departure. The last seen connection state is stored, so a restart while plugged in does not count, and a car plugged in while the app was down does. On by default, switchable in Settings; each decision is in the activity log (source: automation). This covers only the most basic need; the rest of V5 can refine the target, e.g. adding charge when prices are cheap or for trips soon after the first one (horizon planning below).

To do:

- Update managed trip plans after hourly calendar sync, new prices, connection changes, and manual edits. Keep trip automation separate from advice auto-apply; daily advice remains manual unless its own setting is explicitly enabled.
- **Horizon planning:** while published prices (typically 24–36 hours after the ~14:00 update), the trips, and the solar forecast cover the coming day, plan for all trips in that horizon, not only the next one. For each later trip, compare charging its energy before the first departure (e.g. a cheap night block) with the cheapest feasible home window before that trip, including forecast solar surplus after household use; add it to the earlier target when that is cheaper.
- EVCC keeps one plan per vehicle, so the managed plan is one SoC target by the first departure (EVCC picks the cheapest slots before it); it may exceed the 80% limit. Re-plan as the day unfolds. Beyond the published-price horizon, keep the minimum per-trip targets; V2 estimates stay advisory.
- UI: explain a target that is higher than the next trip needs, e.g. "includes tomorrow's afternoon trips: night €0.21/kWh vs €0.32/kWh before them".
- Manual changes made in EVCC pause app control for that vehicle until explicit **Resume**. Preserve plans/settings the app does not own.
- Track temporary app-managed settings and restore their previous background values on expiry/completion/cancellation, provided the user has not subsequently changed them.
- Prefer Home Assistant Companion notifications; Telegram is an alternative.
- HA flow: planner calls HA's mobile notification service; phone buttons emit HA events, and an HA automation calls back to the planner. Uses the HA connection from V4 (address and token in `.env`); configure the notification target. HA must reach the callback address; local-only development can stub callbacks or use a configurable LAN bind for integration checks.
- Request connection before useful cheap/solar windows; escalate near the latest feasible plug-in time.
- Actions: **Remind in 30 minutes**, **Skip trip**, **View plan**. Support trip edits and clarification requests.
- Confirm connection through EVCC, cancel reminders, and flag interruptions/missed targets; readiness confirmations are optional.
- Persist snoozes/decisions and deduplicate notifications. Expose notification enablement, available destinations, early/urgent reminder timing, quiet hours/urgent handling, and snoozing in UI settings.
- UI: target, charging windows, readiness/plug-in deadline, pause/resume, and notification actions. Graph published prices and planned charging.

</details>

<details open>
<summary><b>V6 — UI polish</b></summary>

- Refine the visual design: typography scale, spacing, color tokens, dark-mode parity, icons, and empty, loading, and error states.
- Better graphs: an interactive SoC timeline with trip markers and charging windows, a price chart with planned charging and confirmed vs estimated prices, day drill-downs, and tap-friendly tooltips on phones.
- Clearer Overview hierarchy and a day detail view with quick actions.
- Phone and installed-app polish: touch targets, bottom navigation on small screens, safe areas, pull-to-refresh, splash and theme colors.
- Accessibility: contrast, focus order, screen-reader labels, reduced motion. Keep screenshot checks of key screens at desktop and phone sizes.

</details>

<details open>
<summary><b>V7 — Reliability</b></summary>

Extend the tests already built from V1; harden recovery, credential renewal, stale-data handling, and notification delivery. UI: sync/health status and recovery actions.

</details>

<details open>
<summary><b>V8 — Optimization and conversation</b></summary>

Validate EVCC optimizer control for solar/grid scheduling; its regular planner selects tariff slots and uses available solar. Optional OpenClaw interface using the same core. UI: schedule comparisons/explanations.

</details>

**Costs.** Existing hosting adds little cost. Calendar/Telegram and personal routing should fit free allowances. V2 adds daily OpenRouter extraction usage; model choice determines cost. KNMI open datasets are free.

References: [EVCC plans](https://docs.evcc.io/en/features/plans/), [limits](https://docs.evcc.io/en/features/limits/), [plan API](https://docs.evcc.io/integrations/rest-api/operations/setvehiclesocplan/), [preview API](https://docs.evcc.io/integrations/rest-api/operations/previewloadpointsocplan/), [HA notification actions](https://companion.home-assistant.io/docs/notifications/actionable-notifications/), [Maps pricing](https://developers.google.com/maps/billing-and-pricing/pricing), [OpenRouter structured outputs](https://openrouter.ai/docs/guides/features/structured-outputs), [KNMI API](https://developer.dataplatform.knmi.nl/open-data-api), [KNMI access/costs](https://developer.dataplatform.knmi.nl/faq).
