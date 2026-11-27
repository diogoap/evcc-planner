# EVCC Planner

Have enough charge for upcoming trips by departure time, using the cheapest practical mix of grid energy and solar.

**Decisions.**

- Deterministic service with **native local development** and **Docker production packaging**: calculate trip demand and send SoC/departure targets to EVCC, which controls charging. V2 uses AI to structure external forecast text. The optional **AI mode** (2026-11-18) lets a model choose charge ahead, charge extra, and deferring, within rules the planner keeps.
- Planner targets **always override the 10% minimum and 80% default limit**, without approval. EVCC's planner charges past the limit during planned slots (checked in EVCC source), so plan writes need no limit change; synchronize limits only if live validation shows otherwise. Physical vehicle/charger limits still apply.
- **Hourly Google Calendar sync** via each calendar's **secret iCal address** (read-only; no OAuth). Manual edits apply immediately; local EVCC status/deadline checks can run more frequently.
- **No app authentication or user accounts**; use existing network authentication. Calendar iCal URLs and external API keys remain server-side.
- Schedule in **Europe/Amsterdam**, respecting daylight-saving changes.
- Beautiful, simple, responsive **EVCC-inspired UI**: clear cards, restrained colors, useful graphs, and a rolling **seven-day plan**. Distinguish published prices, estimates, and missing data.
- **Only use/show energy prices including VAT**, in €/kWh, throughout calculations, recommendations, charts, and notifications. EVCC prices are confirmed to include VAT. Normalize other sources to the same retail basis; unclear conversions remain unknown.
- **Daily advice is advisory by default**: offer **Create extra plan** for charge-ahead advice, followed by a proposal and confirmation. Optional advice auto-apply is a separate setting, **off by default**; enabling it requires explicit target/expiry rules because qualitative indicators alone do not define a charging amount. Automatic trip/deadline plan management starts in V5. Every action is auditable from V3.

**Context (2026-11-09).** Local EVCC API (`/api/state`; LAN address kept in private config): SoC, connection, prices, solar forecast. EVCC 0.319.0; a single-phase charger (6–16 A); the car configured with 62 kWh gross; 15-minute price slots; optimizer automatic control off; Home Assistant connected.

- Defaults: **59 kWh usable** and **3.68 kW** (the car charges single-phase at 16 A; about 3.3 kW into the battery at 90% efficiency). Validate both live.
- Vehicle SoC comes from EVCC's vehicle integration and is **only available while plugged in**; EVCC reports 0 when unplugged. Home Assistant always has it, and the planner reads it from there since V4.
- EVCC plan endpoints need no login. Each vehicle holds **one one-off SoC plan** (`POST /api/vehicles/{name}/plan/soc/{soc}/{timestamp}`) plus optional repeating plans (none configured). Previews: `GET /api/loadpoints/{id}/plan/static/preview/soc/{soc}/{timestamp}`.

Background EVCC preferences: cheap-grid charging and solar/export-price priority. Typical thresholds discussed: €0.15/€0.30 per kWh; snapshot: €0.20/€0.40. Trip requirements take priority.

**Confirmed lightweight stack.** Go standard-library HTTP server, Vue 3 + TypeScript + Vite, SQLite (`modernc.org/sqlite`), and selectively imported Chart.js. In production, one Go process serves the API and compiled UI in one Docker container; SQLite uses a persistent volume. Node is needed for frontend development/builds. Periodic jobs run inside the app.

**Configuration and execution.**

| Location | Contents / Git policy |
| --- | --- |
| `config.yaml` | Tracked system settings: port **7201**, timezone, integration modes, secret variable names, the API base URLs that receive keys, the Home Assistant address default, notification provider/connection defaults. Service addresses use `127.0.0.1` with the real ports; other non-sensitive values are allowed. Integration settings without secrets are edited in the UI (Settings → Connections) since 2026-11-11. |
| `config.local.yaml` | Optional ignored overrides of `config.yaml` values and bootstrap trip defaults/places. Personal values saved through the UI take precedence. |
| `.env` | Ignored deployment values and secrets, provided as environment variables in production: `GOOGLE_CALENDAR_ICAL_URL` (one or more secret iCal addresses; calendar names come from the feeds), Google Maps (Routes API) key, OpenRouter key, KNMI key, Home Assistant token (`HA_TOKEN`). The EVCC and Home Assistant addresses are set in the UI. Server-side only. |
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
- Trip routes (2026-11-24): calendar and manual trips may have their own route of 2 to 5 points (default home → destination → home; recurring trips keep it). The arrive-by time is for the first stop and the end time is when the last stop is left. A trip ending away keeps the car away (no charging) until a later trip brings it home. Routing uses at most two Routes API calls per trip: the drive out with stops as waypoints, and the drive back; each leg is cached on its own.
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
- Keep advice qualitative, with two independent signals: **confidence 1 = low, 2 = medium, 3 = high**, and **charge indicator = little / medium / high (more)**. Indicators do not prescribe fixed kWh/SoC amounts; the user decides how much to charge. (Replaced on 2026-11-17 by charge extra's steps and target; see Charge extra below.)

| Recommendation | Meaning |
| --- | --- |
| **Nothing** (`none`) | Keep the scheduled plan, or keep no plan when none is needed. For weak/missing forecasts, show **No data / no suggestion** with the reason and no charge indicator. |
| **Charge extra** (`charge_extra`; called charge ahead until 2026-11-17) | That day's useful charging slots are cheaper than following days; show a qualitative charge indicator. Prefer published next-day prices whenever available. |
| **Defer/postpone** (`defer`) | Lower prices are expected ahead and current/projected SoC covers demand until a later feasible charging opportunity. Required departure targets remain binding. |

- UI: each day shows its recommendation, confidence level, qualitative charge indicator, rationale, wind/sun, and source freshness. Clearly label estimated prices and weak/stale/missing inputs; insufficient evidence produces no charging suggestion.
- By default, **Create extra plan** opens an editable charge-ahead proposal with the SoC/energy target, deadline, and expiry; **Review postponement** does the same for deferral. Require confirmation before EVCC writes. Track and restore app-owned temporary settings when these plans end, preserving intervening manual changes. **Dismiss** makes no change; weak/no-data advice has no plan action.
- Settings expose optional **Automatically create plans from daily advice**, off by default and independent of V5 trip automation. Auto-apply requires user-configured concrete target/expiry policies, honours trip requirements/manual pauses, and takes no action on weak/no-data advice. Its availability does not delay the manual V2 flow.

As implemented (2026-11-10):

- KNMI: the Open Data API dataset `outlook_weather_forecast` (the meteorologist's national seven-day outlook from today, no location: sunshine %, rain, temperature ranges, wind direction/Beaufort), parsed directly from its XML; no AI needed. Missing days show as gaps.
- Local sun: EVCC's own solar forecast (`forecast.solar` in its state; today and tomorrow), integrated per day and over the hours the car is home. Later days fall back to KNMI's national sunshine, labelled as such.
- Tibber: the article is cut out of the page deterministically and only that text is sent (no link or web tools); the model returns only stated values with exact quotes (strict JSON Schema), and the server rejects quotes not in the article. Excl. market prices are converted with the article's own weekly excl./incl. pair: retail = weekly incl. + (day − weekly excl.) × (1 + VAT). Unchanged articles are not sent to the model again. Without an OpenRouter key a labelled pattern-based stub reads the fixed summary lines.
- Daily price level: published day average, else the day's estimate, else the weekly average (only usable as a comparison, never as a day's own basis). Recommendations need an 8% gap; confidence follows the weaker compared basis, lowered for stale or contradicting inputs; confidence 1 offers no plan action.
- **Create extra plan** writes a one-off SoC plan (at least the next departure's target, ready before it leaves) and **Review postponement** (today only) removes EVCC's loadpoint smart cost limit (cheap-grid charging) until the cheaper day; it is not offered when no limit is set or every price until then is published and above it. Both record the value they replaced and restore it at expiry unless it changed in EVCC meanwhile.

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

- **Always-on SoC:** read the car's SoC from Home Assistant, which has it even when the car is unplugged, using the HA token in `.env`. Fall back to EVCC, and show the source and freshness of every SoC value.

As implemented (2026-11-11):

- Home Assistant's REST API, read-only (`GET /api/states/<entity>`) with a long-lived access token. Environment: `HA_TOKEN`. The address, the SoC sensor (battery level, %) and optional range sensor (km or mi) are chosen in Settings → Connections, which suggests Home Assistant's battery and distance sensors. Polled every 10 minutes; local calls are not in the activity log, and the token is redacted like other secrets. Without a token or sensor, only EVCC is used and Connections says what is missing.
- A reading's time is when Home Assistant last received the value (`last_reported`, else `last_updated`), or, with an optional reading-time sensor (a timestamp sensor with when the car last reported), the car's own time, or the SoC's last change if later; an unchanged SoC is often not written again when the car reports, so its own times can be old. An unavailable reading-time sensor fails the reading rather than mis-dating it. Unknown, unavailable, 0%, and non-% values are rejected.
- Each source keeps its last reading. **While plugged in, EVCC's live SoC always wins** (it is what EVCC charges by; decided 2026-11-11). After unplugging, EVCC's last reading, dated to the last poll that saw the car plugged in, stands until Home Assistant has news from after it: a value that changed since (Home Assistant's `last_changed`, or the car's own reading time), or the same value reported again. A lagging cloud value reported again after unplugging never replaces it. Planned trips that left after the chosen reading are subtracted, as before.
- UI: the vehicle card shows the source and age ("From Home Assistant · 20 min ago"), the other source's last reading below it, and "last known" for readings that are not live and over three hours old. Settings → Connections shows Home Assistant's latest reading or error. Demo mode stubs Home Assistant: the demo car's SoC, 20 minutes behind, also while unplugged. `make test-live` lists the battery and range sensors with the reading each would give.

</details>

<details>
<summary><b>V5 — Automatic plans and interaction</b> ✅</summary>

✅ **Done** (2026-11-19). Every rule, with diagrams and examples: [AUTOMATION.md](AUTOMATION.md). Implemented 2026-11-12; validated live: automatic writes with the real EVCC, notification delivery and button presses with the real Home Assistant (admin token). Telegram moved to V8 (decided 2026-11-12), then dropped (2026-11-19).

- ✅ **Plan on plug-in** (2026-11-11): send the next departure's target once when the car is plugged in; keep an equal or higher plan, replace a lower one.
- ✅ **Automatic trip plans** (default mode; Manual and On plug-in remain): keep EVCC's one plan matched to the trips while plugged in, after every EVCC check, trip or setting change, and calendar sync. Write only real changes; remove the planner's plan when nothing needs it.
- ✅ **Chosen plans pause it:** a plan set in EVCC or sent by hand pauses automatic changes while it covers the next departure; a lower one is replaced. Resumes on its own when that plan finishes, or with **Resume**.
- ✅ **Extra plans from advice** share EVCC's plan: the higher SoC by the earlier time. At expiry they restore as in V2, and the automatic check takes the restored plan over.
- ✅ **Charge ahead:** within published prices, add later trips to the target when charging before the next departure is at least €0.05/kWh cheaper than at home before that trip; capped at 90%. Solar comes first: a solar kWh counts as the grid price minus the tax not paid on it (default €0.11/kWh). The UI explains a higher target.
- ✅ **Notifications** through the Home Assistant Companion app; button presses come back over Home Assistant's websocket (no YAML).
- ✅ **Plug-in reminders:** early (cheapest window), urgent (latest plug-in time), and too late; Remind in 30 min, Skip trip, View plan. Quiet hours 23:00–07:00; deduplicated and cleared on plug-in.
- ✅ **Problems and questions:** charging stopped, unplugged early, EVCC not in Smart mode (only while plugged in), missed target, Needs-review calendar events, optional ready message.
- ✅ **Charge extra from solar** (2026-11-13; called charge ahead from solar until 2026-11-17): daily advice prices today's and tomorrow's solar surplus while the car is home (slot price minus the €0.11/kWh tax not paid) and says "keep the car plugged in" when that beats the later days; defer only when a later day beats the sun. No plan: EVCC charges from the sun by itself. A **Plug in for the sun** notification goes out when the car is home and unplugged during the sun (Solar reminders, on by default). Advice summaries use full day names ("Sunday looks cheaper").
- Decisions (2026-11-12): Automatic for existing installs; replace lower chosen plans in Automatic and On plug-in; pauses end on their own; websocket for button presses; €0.05/kWh minimum saving; quiet hours 23:00–07:00; solar valued at the grid price minus €0.11/kWh tax. EVCC must be in Smart mode (it has only Off, Smart, and Fast); the planner warns but never switches the mode, since another automation sets Off while the car is away. Notification buttons need an admin token (Home Assistant only lets admins subscribe to their events).

</details>

<details>
<summary><b>V6 — UI polish</b> ✅</summary>

✅ **Done** (2026-11-13; planned 2026-11-12). Screens and layouts: [UI-MOCKUP.md](UI-MOCKUP.md). Frontend only, except that `/api/status` adds EVCC's solar forecast (already read for advice) for the chart. No planning rules change.

Found on the current screens (demo data; desktop and phone; light and dark):

- On phones the tabs run off the screen (Activity is cut off). Each row of the seven-day table is about 200 px tall because its five advice badges stack. Trip rows put distance and energy on different lines.
- The most important answer (will the car be ready, and when must it be plugged in?) is split over three cards. Next to them, the Energy now card holds little.
- The two charts cover different times (seven days vs about 27 hours), show only published prices, and have no "now", trip, or charging markers. They read their colours once, so they keep the old theme when the phone switches between light and dark.
- Light mode fails contrast: grey text is 3.7–4.1:1 (4.5 needed), green text 3.3–3.6:1, the focus ring 1.8:1 and an off toggle 1.3:1 (3 needed). Dark mode passes.
- Dialogs don't keep focus inside or return it on close. Small buttons and toggles are under 44 px. Loading is plain "Loading…" text. About 150 inline styles bypass the design tokens.

Scope:

- **Design tokens:** one type scale (system font), spacing, radius, colour, and chart tokens in `styles.css`, with light-mode contrast fixed. Shared classes replace inline styles. The hand-drawn icon set grows (navigation, plug, battery, calendar, info, warning, close, refresh) and replaces text glyphs such as ⓘ and ‹ ›.
- **Overview, ready check first:** one main card answers "will the car be ready?". It shows the SoC with range and source, a bar with the target, the next departure, the plug-in-by time or charging progress, what the planner does (mode, pause and **Resume**, the charge-ahead reason, active extra plans with **End now**), and the price and solar power now on one line. Below it: a seven-day strip (each day's charge needed and advice; on wide screens also km, kWh, and price), the most relevant day's advice, and the chart.
- **One chart, one time axis:** SoC on top and prices below, sharing the seven-day axis and one readout that follows a tap or drag. SoC: the projected line, the level each departure needs, the arrival reserve, away periods shaded with trip names, charging windows, and a "now" line. Prices: published slots solid, the planner's expected charging shaded, EVCC's solar forecast, and later days' estimates dashed and labelled; days without data say so. On phones the readout sits above the chart (a finger would cover a tooltip) and a compact legend switches series on and off. Screen readers get a short text summary. No new chart library: a small plugin draws the shading and the "now" line.
- **Day panel:** tapping a day in the strip or the chart opens a panel over the Overview: a bottom sheet on phones, a side panel on desktop. The back gesture or Escape closes it; arrows (or a swipe on phones) move between days. It shows the day's chart zoomed in, its trips with on/off and edit, the charge needed and expected charging slots, prices (published or estimated), sun and wind, and the advice with its actions. Quick actions: add a trip that day, send a plan that covers through that day, open the day in Trips.
- **Phones and the installed app:** bottom navigation with icons and labels below 640 px wide. Content clears the notch and home indicator (safe areas). Dialogs become bottom sheets. Touch targets are at least 44 px. Pull to refresh in the installed app (browsers keep their own), and "Updated hh:mm" with a refresh button everywhere. Splash and theme colours match light and dark; `pnpm icons` also generates iOS launch images for current iPhone sizes.
- **States:** placeholders shaped like the content while loading; empty states that say what to do next (no trips that day → **Add trip**); errors shown in the card they affect, with **Retry**, keeping the last data visible and dated. V7 adds health status and recovery actions.
- **Other pages:** Trips rows keep one layout on phones (time and title, distance and energy aligned, actions in one row) and day headers stay visible while scrolling. Settings sub-tabs fit on phones, with a Save bar while changes are unsaved. Activity filters fold away on phones.
- **Accessibility:** contrast as above in both themes; visible focus in a logical order; focus kept inside, and returned from, dialogs and the day panel; labels on icon-only buttons; full spoken labels for days and charts; every animation respects reduced motion.
- **Checks:** a Playwright test visits the key screens (Overview, day panel, Trips, Recurring, each Settings tab, Activity, the send dialog) at desktop and phone sizes, in light and dark. It checks for sideways scrolling, touch targets under 44 px on phones, and serious or critical findings from an axe accessibility scan (`@axe-core/playwright`, dev only), and saves a screenshot of each for review. No pixel comparison.
- **Order**, each step passing `make verify` and deploying on its own: tokens, contrast, icons, and the checks first, so later steps are guarded; then navigation and the phone layout; the Overview and chart; the day panel; other pages and states.
- ✅ **Step 1, foundations** (2026-11-12): tokens for type, spacing, radius, colour, and charts in `styles.css`. Light mode now passes contrast (grey text at least 4.6:1, green text 4.7:1, focus ring and switches 3:1). Shared classes replaced the inline styles, and lint blocks new ones (`vue/no-static-inline-styles`). New icons replace the text glyphs. Charts redraw when light/dark changes. Dialogs keep focus inside and return it on close. On touch screens every control is at least 44 px. `web/e2e/screens.spec.ts` checks 11 screens at both sizes in both themes.
- ✅ **Step 2, phones and the installed app** (2026-11-13): below 640 px the navigation is a bar at the bottom (Overview, Trips, Recurring, Activity, Settings; Settings moved last on wide screens too), and the top bar shows the app's name, "EVCC Planner", in full. Content clears the notch and home indicator. Dialogs open as sheets from the bottom and stop the page behind from scrolling. "Updated hh:mm" with a refresh button sits in the top bar; the installed app also refreshes on a pull down from the top. `pnpm icons` generates iPhone launch screens for 11 screen sizes in light and dark (22 images, about 0.9 MB).
- ✅ **Step 3, the Overview** (2026-11-13): the ready card replaces the vehicle, energy, next-departure, and control cards. It leads with one line ("Plug in by 06:36", "Charging: 42% by 07:48", "Ready for tomorrow 07:48", "Can't reach …"), then the departure, the planner's mode and EVCC's plan, and the price and solar power now. The seven-day table became a strip of day tiles; tapping one shows its advice (the day panel comes in step 4). The two charts became one: SoC over prices from the current hour to the end of the week, with a readout that follows the pointer or finger. `/api/status` now includes EVCC's solar forecast. As built: the SoC panel shades the time away from home but not charging, because its line charges as soon as the car is home while the price panel shows the planner's expected (cheapest) slots, and two charging shadings would contradict each other; weekly averages are dotted and estimates dashed, as in the badges.
- Revised after review (2026-11-13): the ready card and the advice have the same height side by side. The card shows only the SoC and range (each source and its age moved to the tooltip and Settings → Connections; old readings still say "last known"). Badges at the top: unplugged, plugged in, or charging, and Home, Away, or the zone from Home Assistant's car location sensor (Settings → Notifications), which is now read with every Home Assistant poll and shown in `/api/status`. The headline names the departure and its state with an icon (green check ready, bolt charging, plug waiting, red cross not enough charge yet) and a "needs N%" badge, without a repeated departure line. "Energy now" is a labelled row; the buttons share one style. A **Published prices** card below the chart shows the quarter-hour prices in detail, like EVCC's: the axis zoomed to their range, the cheapest and dearest slot labelled, expected charging shaded, and the average and range below; the send and proposal dialogs use the same chart. Published prices are violet in both charts (estimates stay amber), and the price card shades time away from home like the SoC chart. Demo prices follow a smooth daily curve with quarter-hour jitter. The week switches between **columns** (a tile per day, with icons for distance, energy, the SoC target, and the price) and a **list** with columns for the day, driving, energy, plan (the charge and the first departure's target), price (average, and the lowest slot on published days), weather (KNMI's sunshine and wind every day, plus EVCC's solar kWh where it has one), and the advice with any calendar trips to review ("1 trip to review"); the choice is remembered on the device. The advice card shows price, sun (source: EVCC or KNMI), wind, and temperature as small panels, prices with two decimals and without "incl. VAT" (every price in the app includes it), and confidence and the charge indicator as badges.
- ✅ **Step 4, the day panel** (2026-11-13): tapping a day (tile, list row, or the chart) opens it over the Overview: a sheet from the bottom on phones, a panel at the right on wide screens. It shows the day's figures, its chart (that day only, three-hour ticks), its trips with on/off, edit, and history, what each departure needs and the planner's expected charging windows, and the advice with its price, sun, wind, and temperature. Quick actions: add a trip that day, send the plan covering through that day, open the day in Trips. The day is in the address (`?day=`), so the back gesture closes the panel; arrows or a sideways swipe move between days and Escape closes it. Dialogs opened from it (add or edit a trip, send a plan, a proposal) stack on top: Escape closes only the top one.
- ✅ **Step 5, other pages and states** (2026-11-13): Trips rows keep one layout on every screen (time and title, distance and energy on the right, details, tags, actions), day headers stay in view while scrolling, and empty days offer **Add trip**. Settings sections fit on phones as a grid; every form shows a Save bar with **Discard** while changes are unsaved, and switching section, leaving the page, or closing the tab asks first. Activity filters fold behind a **Filters** button on phones. While a page loads it shows placeholders shaped like its content; empty pages say what to do next (no recurring trips, no matching activity); errors come with **Retry**. A failed refresh keeps the last data on screen: the top bar shows "Not updated" with its time and Retry.
- Decisions (2026-11-12): the Overview leads with the ready check; day detail is a panel over the Overview, not its own page; layout and accessibility checks without pixel comparison; system font. Defaults: follow the system's light/dark setting (no switch); keep the hand-drawn icons (no icon library); no new chart dependency.

</details>

<details>
<summary><b>Charge extra</b> ✅</summary>

✅ **Done** (2026-11-17). Every rule, with a diagram: [AUTOMATION.md](AUTOMATION.md#charge-extra-daily-advice).

- Three layers: the next departure (always), charge ahead for later trips (unchanged, €0.05/kWh minimum saving kept), and **charge extra**: the daily advice's extra charge, renamed from "charge ahead" so that name means only the trip feature.
- Only days with published prices get a recommendation; later days show the outlook ("Looks cheaper"). **Defer** only on published prices: tomorrow's lowest at least 8% below today's lowest at home (with or without the sun, which still charges when cheaper than tomorrow's lowest).
- **Charge extra** compares the day's blocks at home (until the plan's deadline; solar at the slot price minus the tax not paid on it) with a reference: tomorrow's lowest published price, else the strongest of the lowest the weekly outlook expects for tomorrow (its estimate, then a full week of price history, then the outlook's weekly average; outlook values are daily averages, scaled by the day's own lowest-to-average ratio, 0.3–1) and the past 7 days' average lowest price from the price history (at least 3 complete days). An **indication of cheaper days ahead** (little/medium/high, from outlook estimates, wind, and sun) picks a level of three steps (% cheaper → target SoC, each level on/off; defaults little 20→50, 30→80, 50→100; medium 30→40, 45→60, 60→80; high off). The target is capped at what the qualifying blocks can charge. Blocks at or below €0 count for every step.
- Charge extra from solar when the sun is no dearer than the reference (no plan). Hints for trips that miss very cheap or negative blocks, and for negative prices with sun (exporting costs money: use it or switch the panels off).
- Decisions (2026-11-17): levels by the indication of cheaper days ahead; the reference uses lowest prices; defer only on published prices; the charge-ahead margin stays €0.05. Settings → Advice replaces the per-indicator targets with the levels; auto-apply needs no policy: its plan has the advice's target, is ready when the cheap hours end (or by the next departure), and ends then (the Ready by and Expiry settings were removed). **Plug in for charge extra** and **Extra plan created** notifications (Charge extra reminders, on by default). Demo mode can seed a past week of prices for tests (`POST /api/demo/history`); real installs start their history empty.
- Decisions (2026-11-17, after a cheap Wednesday night got no advice on Tuesday): the weekly outlook is a reference before the price history fills (confidence 2, 1 when stale), as the original order asked (published → outlook → past 7 days); defer compares with today's lowest while the car is home all day, not only until the next departure; the plug-in reminder also looks at tomorrow's advice and, when the cheap hours start in quiet hours, comes just before them (like the early trip reminder).
- Decisions (2026-11-17): a **Back home** reminder when the car location sensor turns home after the day's last trip (planned or not), unplugged, while the next trip, charge ahead, or charge extra needs charging; urgent, so it may come in quiet hours; once per arrival; the other non-urgent plug-in reminders wait 30 minutes after it. Setting: Back home reminders (on by default).

</details>

<details>
<summary><b>Consumption from Home Assistant</b> ✅</summary>

✅ **Done** (2026-11-17).

- The consumption is entered in kWh/100 km or km/kWh (Settings → Home & vehicle; stored in kWh/100 km, both shown read-only).
- Optional Home Assistant sensor with the car's consumption, in km/kWh or kWh/100 km (asked; taken from the sensor's unit when it has one). Read with the Home Assistant poll and right after saving. Its last valid value (a positive number within 5 to 60 kWh/100 km) is saved with its time and replaces the set value everywhere in the plan; zero, unknown, unavailable, implausible, or failed readings keep the previous value. Without a valid value yet, the set value is used. The setting shows what the plan uses in both units, its age, and why a reading was not used.

</details>

<details>
<summary><b>Price history</b> ✅</summary>

✅ **Done** (2026-11-17). What the app does is now also listed in short in [FEATURES.md](FEATURES.md), which will replace this plan.

- One row per day, kept indefinitely (not pruned with the activity log): EVCC's lowest, average, and highest published price; the km of the day's enabled trips (also those already driven) and their energy; KNMI's wind (Beaufort) and sunshine (%). Saved twice a day (decided 2026-11-17): as the day starts (prices, wind, and sun are known by then; a start later that day catches up, after the first EVCC check) and again at 23:45 for trips changed during the day; a once-a-minute check sees when a save is due. The evening save keeps the first save's prices if EVCC no longer lists the day's earlier slots (not yet checked live). Past days never change. Recording starts on install; past days are not filled in (decided 2026-11-17). Not an action, so not in the activity log (like SoC readings).
- A **Price history** card below the week's charts (the Overview, or the Charts page on phones; decided 2026-11-17): the prices as a band from lowest to highest with the average line, wind and sun on request (driving km as bars and the trips' energy too, added 2026-11-17, off by default), a dashed line at today; reloaded hourly. Period: 1 week, 1 month (default), 3 months, 1 year, or all; remembered on the device. `GET /api/history?from=&to=`.
- Later: compare today's prices against past prices and how they fluctuate.

</details>

<details>
<summary><b>AI mode</b> ✅</summary>

✅ **Done** (2026-11-18). Every rule, with a diagram: [AUTOMATION.md](AUTOMATION.md#ai-mode).

- A fourth trip plan mode, **AI mode**: like Automatic, but an AI model (through OpenRouter, strict JSON Schema output) decides charge ahead (the next departure's target, with later trips and a reserve, e.g. when the coming days drive more than usual) and each day's advice: charge extra (target and window), charge extra from solar (window), defer (and today whether to pause cheap-grid charging), or nothing, with confidence, reasons, and the inputs that mattered.
- Charge extra's target is the AI's: the default prompt gives a rough guide with example values; the steps from Settings → Advice are not sent (decided 2026-11-18). The solar forecast shows each hour's usable surplus and its price (the grid price minus the tax not paid on own solar), computed by the planner.
- Data: published prices (every slot), the next 7 days' trips (names, times, km, kWh, SoC; no places or addresses; names added 2026-11-18 so the AI's explanations name the trips), each day's need, Tibber's outlook, KNMI's weather, EVCC's solar forecast, the past 30 days of the price history, the car, the state, and the goals. The prompt is editable under Settings → Advice → AI mode with placeholders (`{{energy_prices}}`, `{{trips}}`, …) filled when it is sent; a default prompt is included; a fixed system message sets the answer's format.
- Foundational rules stay the planner's: every departure's own target (written at plug-in before the AI is asked), the charge-ahead cap, pauses for chosen plans, windows within the published prices, postponing today only. `internal/aiplan` checks every answer and lists its corrections; prices and energy shown are the planner's own. Extra plans and postponements are created automatically only with AI mode's own switch (off by default), on the AI's advice; Advice → Automatic rules is greyed out in AI mode (decided 2026-11-18).
- Runs after new prices (or when no AI plan applies), after a plug-in, and by hand (Run now on the Overview and Settings → Advice → AI mode); at most 3 a day (configurable, 0–10; runs by hand always allowed); a failed run retries after an hour. Without a current AI plan (none yet, older than 30 hours, made for an earlier departure, or no key) the automatic rules apply.
- Notifications as in Automatic; the titles of those about charging end with "(🤖 AI mode)" (until 2026-11-19 they started with "AI mode:"). Each run (`ai.run`) is in the activity log and keeps the prompt as sent and the answer; Settings → Connections has an AI mode row while it is on.
- After a 15:03 run moved 80% from charge ahead to charge extra and the plan check removed the plan (decided 2026-11-18): AI mode's auto-create switch follows the Automatic rules' until changed there; after a new AI plan, auto-apply runs before the plan check; the AI is told whether charge extra becomes a plan by itself, and to use charge ahead for the trips' energy when it doesn't.
- Goals (decided 2026-11-18): a Goals field under Settings → Advice → AI mode shows and edits what `{{goals}}` holds; empty follows the generated rules (with the current settings and EVCC's charge limit), and the default text is stored as empty.
- Settings reorganized (decided 2026-11-18): Planning, then Advice with two sub-tabs, Automatic rules and AI mode (the saved mode's opens first); parts that don't apply to the saved (or, in Planning, chosen) mode are greyed out with the reason and keep their values; the AI tab is greyed out outside AI mode, with Preview still usable; household use and the solar tax moved to Home & vehicle (Solar at home). On plug-in now sends the charge-ahead target (when on), keeping a plan in EVCC with at least that target and replacing a lower one (an extra plan it replaces ends). Acting on advice automatically never happens in Manual mode.
- New prices (decided 2026-11-18): once per publication, plans made automatically from advice are replaced or ended to follow the new advice, chosen plans and extra plans you created are raised (never lowered) when the planner wants more (Automatic and AI mode), auto-apply runs at once, and a **New prices: plug in** notification goes out while the car is unplugged (Plan changes after new prices, on by default). AI mode keeps the day's last run for tomorrow's prices until an AI plan has seen them.
- Decisions (2026-11-18): AI mode has its own switch to create plans from the AI's advice (with minimum confidence and also postpone), instead of the advice auto-apply setting; the modes are listed AI mode, Automatic, On plug-in, Manual; the automatic rules are the fallback; the AI may raise charge ahead up to the existing cap; the run limit counts every run but never blocks one by hand; its own model setting (default `~anthropic/claude-haiku-latest`, decided 2026-11-18; earlier empty used the forecast's model); demo mode uses a stand-in that repeats the automatic rules.

</details>

<details>
<summary><b>V7 — Reliability</b> ✅</summary>

Extend the tests already built from V1; harden recovery, credential renewal, stale-data handling, and notification delivery. UI: sync/health status and recovery actions.

✅ **Done** (2026-11-19). Every rule on old data: [AUTOMATION.md](AUTOMATION.md#when-data-is-too-old).

Planned 2026-11-19. When something breaks (a service down, a key rejected, data too old, a notification that didn't arrive), the app notices, says so on the Overview, and recovers by itself or offers a button to fix it.

Found in the current code:

- A reminder that fails to send is never sent again: `reminders.send` skips every stored key, failed ones included, so a short Home Assistant outage at reminder time loses the reminder. The button listener already reconnects by itself.
- Problems show only under Settings → Connections; the Overview says nothing when Home Assistant, the calendar, or a forecast fails (only "Can't reach EVCC" and "Not updated" exist). Google Routes reports "ok" whenever a key is set, whatever its calls return.
- A rejected key is explained for KNMI only ("missing, mistyped, or expired"); Home Assistant notices a 401 but shows it as a plain error; OpenRouter, Google Routes, and a reset calendar address get no specific message.
- Each source handles old data its own way: the SoC says "last known", old forecasts lower the confidence, AI plans stop after 30 hours, and AI mode waits while EVCC data is older than three checks. Prices, the solar forecast, the calendar, the weather, and the car's location have no common rule.
- `/readyz` only pings the database, so stalled background checks go unnoticed.
- Tests cover the normal paths well; few cover failures in the middle of a flow.

Scope:

- **Notifications that arrive:** a failed send is tried again after 2, 5, and 15 more minutes, keeping quiet hours (urgent ones as set), and given up once it no longer matters: the trip left, the car was plugged in, or a newer reminder replaced it. The activity log records the first failure and the final outcome, not every try; Settings → Connections shows the last failure.
- **Health on the Overview:** a line at the top of the Overview, shown only while something needs attention, such as "Home Assistant unreachable since 10:12 · SoC last known 09:58", linking to Settings → Connections. In the app only, never a phone notification. Google Routes reports its real state from its recent calls.
- **Rejected keys:** an answer meaning "not allowed" (401/403, or a calendar address that no longer exists) from Google Routes, OpenRouter, KNMI, Home Assistant, or a calendar feed gives the status **key rejected** with what to do: "Renew `OPENROUTER_API_KEY` in the deployment's environment and redeploy". Keys stay in environment variables only.
- **Recovery actions:** in Settings → Connections, **Check now** per service, **Sync now** for calendars, and **Refresh** for forecasts; **Send plan again** where EVCC's plan differs from the planner's. Each failure kind has a short "what to do" text. Actions go through the activity log.
- **Stale data:** one rule per source: how old its data may be, what the planner does when it is older, and where the age is shown. For example: with EVCC data older than three checks, no automatic writes to EVCC and the ready card says so; prices older than their last slot count as missing; a calendar not synced for 3 hours shows on the health line.
- **Readiness:** `/readyz` also fails when the background checks haven't run for three intervals, and the health line says when they stopped. Portainer then shows the container as unhealthy (Docker doesn't restart unhealthy containers by itself).
- **Tests for failures:** demo stubs that fail, time out, or reject the key on request (a demo-only endpoint, like `POST /api/demo/history`), so browser tests can show the health line, the key-rejected status, and each recovery button. Go tests for EVCC dropping during a plan write, a revoked Home Assistant token, a restart during an active extra plan, a failed send followed by a retry, and an outage across a daylight-saving change.
- **Order**, each step passing `make verify` and deploying on its own: (1) notifications that arrive; (2) the failing demo stubs and the health line on the Overview, with Google Routes' real state; (3) rejected keys; (4) recovery actions; (5) stale-data rules; (6) readiness. Every step brings its own tests, and new screens or states go into `web/e2e/screens.spec.ts`.
- ✅ **Step 1, notifications that arrive** (2026-11-19): a send that reaches no phone becomes **retrying**, which counts as open, so the existing rules end it when it's no longer needed (recorded as "Stopped sending …: no longer needed"). It is tried again after 2, 5, and 15 more minutes and given up after the fourth failed try, past its latest time, in quiet hours it may not come in, or when no check could try it within 15 minutes of its time. Information-only messages are done once a retry arrives; test notifications are not tried again. Settings → Notifications shows "Trying again" with the reason and next time; Settings → Connections shows the last failure until a send works.
- ✅ **Step 2, the health line and demo outages** (2026-11-19): `/api/status` lists the connections with an error, and the Overview shows them in a warning line at the top: what went wrong, when it last worked, and a link to Settings → Connections (Settings opens a section from `?tab=`). Google Routes reports its last lookup (failed until one works again; kept in memory, so a restart starts at "ok"). An error now wins over "Stub" for EVCC, and a failing calendar says when it last synced. Demo mode can take EVCC, Home Assistant (and with it the demo phone), the calendar feed, or route lookups down: `GET`/`PUT /api/demo/faults`, a Demo outages card in Settings → Connections, and a reset brings them back.
- ✅ **Step 3, rejected keys** (2026-11-19): a refusal (package `rejected`) comes back from Google Routes (401, 403, or 400 "API key not valid"), OpenRouter (401 only: 402 is credit, 403 moderation), KNMI (401, 403, or the gateway's words for a missing or wrong key), Home Assistant (401, and the websocket's auth_invalid), and calendar feeds (401, 403, 404, 410: a reset secret address). Its text has a fixed form ("API key rejected (HTTP 401): …"), so a refusal saved as text (calendar syncs, forecasts, AI runs) is recognised too. Such a connection gets the status **Rejected** with a fix ("Set a new OPENROUTER_API_KEY in the planner's environment and restart it."), shown in Settings → Connections and on the health line; the OpenRouter row takes the refusal from the outlook's error, while the outlook's own row stays an error. Demo outages can make Home Assistant, the calendar, and route lookups refuse their key. Settings now loads the demo outages also when opened directly, before the app knows it is the demo.
- ✅ **Step 4, recovery actions** (2026-11-19): each connection lists what can be done now, as a button on its row in Settings → Connections (replacing the three buttons above the table): **Check now** (EVCC, Home Assistant, and Google Routes, which looks one route up without the cache), **Sync now** (calendars), **Refresh** (forecasts), **Send test** (phone notifications, once there is a phone), **Run now** (AI mode), and **Send plan again** (EVCC, while the planner's last change of its plan failed; cleared once a change works, the plan is in line, the car is unplugged, or the mode keeps no plan). `POST /api/connections/action` does only what the connection offers; checks, syncs, and refreshes are recorded as `connection.check` with their outcome, the others record themselves. Every error has a short "what to do" line, also on the health line.
- ✅ **Step 5, data that is too old** (2026-11-19): one rule per source, in a table in AUTOMATION.md. EVCC's "three checks" rule, copied in five places, is one helper (`evccFresh`); too old EVCC data now also counts as unreachable in `/api/status`, so the ready card says "no answer since 10:12" and Send plan is off. EVCC and Home Assistant without an answer for three checks, calendars without a sync for three syncs, and forecasts without a fetch for two days show as an error on their connection and the health line, with what to do. The SoC (3 h), the outlook's and weather's contents (8 days, 36 h), and AI plans (30 h) keep their limits.
- ✅ **Step 6, readiness** (2026-11-19): each background job records when it starts a run; one that hasn't for three of its intervals, and at least 10 minutes (an AI run inside the EVCC check can take a while), counts as stopped. `/readyz` then answers 503 ("background checks stopped: The EVCC check last ran at 10:12"), the image's health check now probes `/readyz` instead of `/healthz`, and the health line says to restart the planner. Docker doesn't restart an unhealthy container by itself; Portainer shows it.
- ✅ **Failure tests** (2026-11-19): besides those of each step (a refused plan write and Send plan again, a revoked Home Assistant token, a failed send and its retries), demo outages can make a service slow: each call waits (2 s in the demo) and then times out (`errSlow` wraps `context.DeadlineExceeded`), and a caller's earlier deadline ends it sooner. A restart on the same database keeps an active extra plan, which still restores the earlier plan when it ends. Across a daylight-saving change, ages count real time, not the clock face: at the second 02:00 in autumn, an answer from 01:58 CEST is an hour old; at 03:00 CEST in spring, one from 01:58 CET is two minutes old.
- Decisions (2026-11-19): notification retries and the Overview health line come first; keys stay in environment variables only, and the app explains which one to renew; problems show in the app only, never as phone notifications; database backups are not part of the app (handled on the host, as the README describes).

</details>

<details>
<summary><b>Car heating</b> ✅ (2026-11-27)</summary>

Before a departure from home, when it is cold outside, turn on the car's climate in Home Assistant, by itself or after a confirmation on the phone. UI: Settings → Heating, and a Heating row on the Overview. Every rule: [AUTOMATION.md](AUTOMATION.md#car-heating).

- **Decisions (2026-11-27):** only departures from home; the outside temperature comes from a Home Assistant sensor (the car's own temperature isn't enough), else the weather forecast's lowest for the day; asking first is set separately for plugged in and unplugged; the question and the "Car heating on" message come in quiet hours too, and both are cleared after 30 minutes; once per departure; the planner never turns the climate off (the car's timer does), and leaves one that is already on alone; failures show in the app only.
- **Built:** `climate.set_temperature` then `climate.set_hvac_mode` through the existing Home Assistant client (`internal/homeassistant/climate.go`, with a demo climate stub); a per-minute job apart from the EVCC check; one decision per departure in a new `heating_runs` table; a pure `decideHeating`; the **Heat now** button; `heating.on` and `heating.skip` in the activity log. Tests: the decision table, API tests for each outcome, the live test only reads.

</details>

<details open>
<summary><b>V8 — Optimization and explanations</b></summary>

Check whether EVCC's optimizer should take over solar and grid scheduling, and show next to each plan what other ways to charge would cost. UI: plan comparisons.

Planned 2026-11-19. Today EVCC's regular planner picks the cheap price slots for the planner's plan and charges from solar by itself. V8 first finds out whether EVCC's optimizer does better, then makes the plan easier to judge: what it costs, what the alternatives would cost, and why it was chosen.

Found in the current code:

- The planner already estimates which slots EVCC will charge in and what each costs, solar included (`ChargeSlot` in `internal/planner/ahead.go`); charge ahead and the early reminder use it.
- The ready card lists the trips a higher target covers and, in AI mode, the model's reason. No screen compares the plan with another way to charge.
- EVCC has an optimizer; its automatic control is off here (see Context, 2026-11-09). The planner neither reads nor sets it.

Scope:

- **EVCC optimizer check** (a written finding, no code): against the real EVCC, read-only, answer:
  - what the optimizer controls (only the car, or a home battery and other loads too) and what it uses (prices, solar forecast, the car's plan);
  - with automatic control on, whether it follows the car's one-off plan, changes it, or ignores it, and whether planned charging still goes past the 80% limit;
  - whether its schedule can be read through the API;
  - on a few real days, how its schedule compares with the regular planner's for the same target: cost, solar used, and target met.

  The finding ends in a decision: keep the optimizer off (as now); turn it on and keep sending plans; or let it schedule and have the planner only set targets. Automatic control is switched on in EVCC by hand; the planner never switches it, and tests never write to the real EVCC. Any follow-up becomes its own step.
- **Plan comparisons:** on the Overview, below the plan, a short table of ways to reach the next departure, all computed by the planner with prices including VAT and solar valued as for charge ahead:
  - **The plan:** the slots EVCC is expected to use for the target (as today).
  - **Charge now:** full power from now (or from when the car is home) until the target, as without a plan.
  - **Without the extra:** when charge ahead or charge extra raised the target, the next departure's own target now and the later trips charged at home just before them, so the saving shows.

  For each: when it charges, kWh from the grid and from solar, the average price per kWh, and the total in euros. A row that can't be priced (prices not published yet) says so instead of guessing. On top, one "why" line, e.g. "Cheapest before Thursday 07:48: €0.21/kWh, €3.40 less than charging now." In AI mode the rows are the same, for the model's target. A pure function in `internal/planner` and a field in the plan response.
- **The optimizer in the comparison:** only if the check finds its schedule readable and worth showing, add it as a row.
- **Order**, each step passing `make verify` and deploying on its own: (1) the optimizer check, written up here; (2) plan comparisons in the API, with Go tests; (3) the comparison on the Overview, with Vitest, a Playwright flow on demo data, and the new view in `web/e2e/screens.spec.ts`; (4) the optimizer row, if step 1 says so.
- Decisions (2026-11-19): Telegram notifications (moved here from V5) and the optional OpenClaw chat interface are dropped. Notifications stay with the Home Assistant Companion app.

</details>

**Costs.** Existing hosting adds little cost. Calendar and personal routing should fit free allowances. V2 adds daily OpenRouter extraction usage; model choice determines cost. AI mode adds up to three runs a day (about 3,000 prompt and 1,000 answer tokens each). KNMI open datasets are free.

References: [EVCC plans](https://docs.evcc.io/en/features/plans/), [limits](https://docs.evcc.io/en/features/limits/), [plan API](https://docs.evcc.io/integrations/rest-api/operations/setvehiclesocplan/), [preview API](https://docs.evcc.io/integrations/rest-api/operations/previewloadpointsocplan/), [HA notification actions](https://companion.home-assistant.io/docs/notifications/actionable-notifications/), [Maps pricing](https://developers.google.com/maps/billing-and-pricing/pricing), [OpenRouter structured outputs](https://openrouter.ai/docs/guides/features/structured-outputs), [KNMI API](https://developer.dataplatform.knmi.nl/open-data-api), [KNMI access/costs](https://developer.dataplatform.knmi.nl/faq).
