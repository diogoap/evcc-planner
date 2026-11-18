# Features

What EVCC Trip Planner does today, in short. The goal: the car has enough
charge for every trip, charged at the cheapest practical time. EVCC does the
charging; the planner tells it how much and by when.

Setup is in [README.md](README.md), and every rule for automatic plans and
notifications is in [AUTOMATION.md](AUTOMATION.md).

## Trips

- Trips come from three places: Google Calendar (read-only, through each calendar's secret iCal link, synced every hour or at once with **Sync calendar** on the Trips page), recurring trips such as school runs, and one-off trips you add.
- A trip's time is when you must arrive. The planner works out when to leave from the route and a buffer. The trip home starts at the trip's end time.
- Calendar events count as trips when they have a real place other than home. All-day events and video calls are skipped. Unclear events are marked "Needs review".
- You can turn off, edit, or reset a single trip, or one date of a recurring trip. Your edits survive calendar syncs.
- Saved places give short names to addresses (for example "School").

## Planning

- Distance and travel time come from Google Routes (with traffic, cached). Energy comes from the car's consumption.
- Consumption is set in kWh/100 km or km/kWh (both are shown), or read from a Home Assistant sensor in either unit. The sensor's latest valid value is used; zero, unavailable, or implausible readings keep the previous value.
- For each departure: the battery level needed (state of charge, SoC), with a reserve for arrival, charging losses, and time to charge.
- The car is assumed to charge whenever it is home. Trips the car can't make in time, or that need more than a full battery, are flagged.
- The SoC comes from EVCC while the car is plugged in, otherwise from Home Assistant. The app shows where each value comes from and how old it is.
- A rolling seven-day plan in Amsterdam time, with daylight saving taken into account.

## Charging plans in EVCC

- Send a plan (target SoC and deadline) to EVCC yourself. A confirmation first shows EVCC's preview, with the planned charging's average price and total cost (also for extra plans).
- Four modes: Manual (only what you send; the advice only recommends); On plug-in (sends once when you plug in, with charge ahead); Automatic (the default: keeps EVCC's single plan in line with your trips); AI mode (like Automatic, but an AI model decides, see below).
- Settings → Advice has two parts, **Automatic rules** and **AI mode**; the parts that don't apply to the chosen mode are greyed out and say why (in AI mode all of Automatic rules).
- A plan you set yourself pauses Automatic until that plan is done, or until you press Resume.
- Charge ahead (every mode but Manual): the target also covers later trips when charging now is at least €0.05/kWh cheaper (solar counts first), up to 90%. On plug-in keeps a plan in EVCC with at least that target and replaces a lower one.
- When tomorrow's prices arrive (around 14:00), plans made automatically from advice follow the new advice (replaced, or ended so the previous plan comes back), and in Automatic and AI mode a plan you chose is raised when the planner now wants more, never lowered.
- The planner never changes EVCC's limits, repeating plans, or charging mode.

## Prices and daily advice

- Every price is in €/kWh including VAT.
- Published prices come from EVCC: today, and tomorrow from about 14:00. Later days are estimated from Tibber's weekly outlook (read by AI, with every number checked against the article). Wind and sun come from KNMI's weather outlook.
- Charging has three layers: the next departure (always), charge ahead for later trips (Planning), and charge extra on top (daily advice). EVCC gets the higher target.
- Each day with published prices gets one piece of advice:
  - **Defer** when tomorrow's lowest price is clearly lower (8%) than today's lowest while the car is home: charge only for the trips, and from the sun if it's cheaper still.
  - **Charge extra** when blocks at home are much cheaper than tomorrow's lowest price. Before tomorrow's prices are published, that's the lowest the weekly outlook expects for tomorrow, or the past week's average lowest price. How much depends on the discount and on how strongly the coming days look cheaper (outlook, wind, sun). Settings → Advice has the steps, such as "30% cheaper → 80%", for each level. The target is only what those cheap blocks can charge.
  - **Charge extra from solar**: keep the car plugged in when the sun is no dearer than the reference.
- Later days show what the outlook expects ("Looks cheaper").
- On a day the trip plan charges later trips ahead (without charge extra), the advice says "Charge ahead to 80%" rather than "No plan needed".
- Hints for a trip that misses very cheap or negative prices, and for negative prices while the sun shines (use the solar or switch the panels off).
- **Create extra plan** and **Review postponement** ask before changing anything and are undone when they expire. An extra plan is ready when its cheap hours end. Applying advice automatically is optional, off by default, and never happens in Manual mode.

## AI mode

- A fourth way to plan (Settings → Planning). Like Automatic, but an AI model decides charge ahead, charge extra, charging from solar, and when to wait for cheaper prices.
- It gets the published prices (every 15-minute slot), the trips of the next 7 days (names, times, and distances; no places or addresses), the charge each day needs, Tibber's weekly outlook, KNMI's weather, EVCC's solar forecast, the past 30 days (prices, driving, energy, wind, sun), the car and charger, the SoC, and the goals. It may add a reserve when the coming days drive more than usual.
- It answers with the target for the next trip, each day's advice (how much and when), what mattered, and a short explanation. For how much to charge extra, the prompt gives a rough guide with example values (your Advice steps are not sent; in AI mode they only apply until there is an AI plan). The solar forecast shows, per hour, the surplus the car can use and its price (the grid price minus the tax not paid on own solar). The advice always shows it, with an **AI** badge, the same actions (Create extra plan, Review postponement), and the planner's own prices. Its own switch under Settings → Advice → AI mode creates those plans automatically (off by default); the Automatic rules part doesn’t apply in AI mode.
- The rules stay: every trip's own minimum is charged by the planner itself (also right when you plug in), charge ahead stays at or below its cap, a plan you choose pauses it, and windows outside the published prices are ignored. Every answer is checked; corrections are listed with the run.
- It runs when tomorrow's prices are published and when you plug in, at most 3 times a day (configurable; the last run waits for tomorrow's prices), and with **Run now** on the Overview or under Settings → Advice → AI mode. A failed run is tried again after an hour. Until the first AI plan, or when the last one is more than a day old, the automatic rules apply.
- Settings → Advice → AI mode shows the goals the AI gets (by default the planner's rules with your settings, such as EVCC's charge limit as a battery-care preference) and lets you edit them, and edits the prompt, with placeholders such as `{{energy_prices}}` and `{{trips}}` filled with the data; a default prompt is included. **Preview with today's data** shows what would be sent. Each run keeps what was sent and what came back.
- Uses OpenRouter (`OPENROUTER_API_KEY`); the model is its own setting: pick one of 19, each with its cost per run, and the chosen one with an estimate per month at your runs per day (default `~anthropic/claude-haiku-latest`), or type another. Demo mode has a stand-in that repeats the automatic rules.

## Price history

- One row per day, kept indefinitely: the lowest, average, and highest price; the km of the day's trips (trips already driven included) and the energy they need; wind (Beaufort); and sun (share of the possible sunshine).
- Saved twice a day: as the day starts, and again at 23:45 to include trips that changed during the day. Past days never change. Recording starts the day the feature is installed; earlier days are not filled in.
- A chart below the week's charts shows the prices as a band (lowest to highest) with the average as a line. Wind, sun, driving (km, as bars), and the energy the trips need can be added (all off by default). Periods: 1 week, 1 month (default), 3 months, 1 year, or everything. Your choices are remembered on the device.
- The history is meant for later comparisons, such as today's prices against past prices.

## Phone notifications

- Sent through the Home Assistant Companion app; the buttons act on the planner.
- Plug-in reminders: early (cheapest time), urgent (latest time), and too late. Buttons: Remind in 30 min, Skip trip, View plan.
- "New prices: plug in" (Automatic and AI mode) when tomorrow's prices change the plan or bring charge extra while the car is unplugged.
- "Back home: plug in" when the car gets home from the day's last trip (by the car location sensor), unplugged, and still needs charging for the next trip, charge ahead, or charge extra. It comes in quiet hours too, like urgent reminders.
- Problems: charging stopped, unplugged early, EVCC not in Smart mode, target missed. Questions about unclear calendar events. An optional "ready" message. "Plug in for the sun".
- Charge extra: "Plug in for charge extra" before its cheap hours when the car is home but unplugged (for a cheap night, just before quiet hours), and a message when an extra plan is created automatically.
- Quiet hours (23:00–07:00 by default). Each message is sent once and is cleared when you plug in.
- In AI mode, messages about charging start with "AI mode:".

## Screens

- **Overview:** a "will the car be ready?" card, the week (tiles or a list, 3–7 days), the day's advice, and the charts (SoC over prices, published prices, price history).
- In the week, a day's lowest price turns green (with a ⚡) when the plan charges at it: charge extra, or the trip plan charging later trips ahead. Its tooltip says which.
- **Day panel:** one day's trips, charging, prices, weather, and advice, with quick actions. In the week's tiles, a tap shows the day's advice in the advice card; press and hold (or right-click) opens the day, as does the date on the advice card or a tap on the chart.
- **Trips**, **Recurring trips**, **Activity**, and **Settings** pages (Settings has a tab for AI mode).
- Works on phones (bottom bar, installable as an app, pull to refresh) and in light and dark mode. Checked for contrast, keyboard focus, screen reader labels, and 44 px touch targets.

## Activity log

- Every change, every write to EVCC, every AI plan, and every call to an outside service, with before → after values. Filter it, or export it as CSV or JSON.
- Monthly use of outside services, such as Google Routes against its free allowance.
- Entries older than 90 days are deleted (you can change this). The price history is kept.
- Keys, tokens, and secret calendar links are never stored.

## Running it

- No login: the app relies on your home network for access. Secrets live only in environment variables; everything else is set in the app.
- One Docker container (Go server, web app, SQLite file). A demo mode with sample data and stand-in services needs no accounts.
