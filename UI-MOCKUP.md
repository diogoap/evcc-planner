# EVCC Trip Planner — text UI mockup

Illustrative demo data only, set on Monday evening, 9 November 2026. EVCC-inspired: spacious cards, rounded corners, clear typography, muted surfaces, and restrained green/amber accents. Layouts stack on phones, with bottom navigation (V6); confidence and status always have text labels. All energy prices include VAT.

**Overview — the default screen (V6 layout, phone).** The first card answers "will the car be ready?"; everything else supports it.

```text
EVCC Trip Planner                DEMO   Updated 19:00 ⟳

NISSAN LEAF                         [Home] [Unplugged]
38%   122 km
[###########--------|-----------------]

(x) Plug in by 06:36 for tomorrow 07:48     [needs 42%]
School drop-off, at School by 08:15 · then Office visit
2.3 kWh to charge, about 42 min

Trip plans    Automatic: sends 42% when plugged in
EVCC plan     None
Energy now    EUR 0.38/kWh incl. VAT · solar 0.0 kW
[Send plan to EVCC v]  [View trips]  [Plan history]

THIS WEEK                          [columns|list] [+ Add trip]
 Mon   Tue   Wed   Thu   Fri   Sat   Sun
  9    10    11    12    13    14    15
 done  +8%  +21%  +10%  +8%  +12%   -
 defer ahead  -    keep   -   -  !   -
(defer, charge ahead, keep plan, no data; ! = to review)

MONDAY'S ADVICE
Defer optional charging · confidence 3/3
Tue looks cheaper and your charge covers the trips until then.
[Review postponement] [Dismiss]  Why?

SOC AND PRICES                                 incl. VAT
Tue 10, 03:00 · SoC 42% · EUR 0.19/kWh published   <- readout
[SoC:    projected line, ^ level each departure needs,
         -- arrival reserve, shaded away periods with
         trip names, charging windows, | now; above
         it, each day's driving distance and energy
         (icons and values, numbers only where narrow) ]
[Prices: published slots solid, later days' estimates
         dashed, expected charging shaded, solar forecast;
         above it, each day's KNMI sunshine and wind     ]
  Mon    Tue    Wed    Thu    Fri    Sat    Sun

PUBLISHED PRICES                               incl. VAT
Now · EUR 0.37/kWh
[quarter-hour prices, the axis zoomed to their range;
 the cheapest and dearest slot labelled; expected
 charging shaded; | now]
Average EUR 0.23 · lowest EUR 0.13 (Tue 13:15) · highest EUR 0.40 (Mon 19:30)

-------------------------------------------------------
 Overview   Trips   Recurring   Activity   Settings
```

The headline names the next departure and its state, with an icon: a green check for **Ready for tomorrow 07:48**, a bolt for **Charging for …**, a plug for **Plugged in for …** (waiting for its slots), and a red cross while the charge is not enough yet (**Plug in by 06:36 for …**, **Not enough time to charge for …**). The badge beside it says what the departure needs. The badges at the top show where Home Assistant's car location sensor puts the car (Home, Away, or a zone) and whether it is unplugged, plugged in, or charging. A paused automatic plan shows why, with **Resume**; a charge-ahead target says which later trips it includes and the prices compared; active extra plans show their expiry and **End now**. The card shows only the SoC and range; each source's reading and age is in the SoC's tooltip and under Settings → Connections, and an old reading says **last known**, never current. On desktop the main card and the advice sit side by side, and the week strip also shows km, kWh, and price per day. Tap a day (strip or chart) to open its panel. Confidence **1 low / 2 medium / 3 high** is separate from the **little / medium / high** charge indicator. Weak/no-data advice shows its reason and no plan action. Deferred charging has **Review postponement**; keeping a plan needs no action. Long-range days use qualitative signals unless numerical estimates are actually available.

**Day panel — one day in detail (V6).** A bottom sheet on phones and a side panel on desktop, over the Overview. The back gesture or Escape closes it; arrows (or a swipe on phones) move between days.

```text
<  Tuesday 10 November  >                           [x]
100 km · 18.0 kWh · +8% charge
First departure 07:48, needs 42%
[the chart for this day only, hourly ticks]

TRIPS
[On ]  08:15  School drop-off           12 km   2.2 kWh
              Leave 07:48 · until 08:20
[On ]  09:30  Office visit              76 km  13.7 kWh
              Leave 08:20 · until 12:00
[On ]  15:00  School pickup             12 km   2.2 kWh
[Off]  17:30  Optional evening visit    35 km   6.3 kWh

CHARGING
Expected 00:00-00:45 at about EUR 0.17/kWh
(the planner's estimate; EVCC picks the slots)

PRICES AND WEATHER
Lowest EUR 0.14 at 14:00 · published
Sun 7.6 kWh while home · wind 5 Bft

ADVICE
Charge ahead · confidence 2/3 · little
[Create extra plan] [Dismiss]  Why?

[+ Add trip] [Send plan through Tue] [Open in Trips]
```

Trip rows keep the same on/off and edit controls as on the Trips page. Days without published prices show their estimate, labelled, or **No data**.

**Trips — edit individual dated journeys.**

```text
Trips                [Next 7 days] [All sources] [+ Add trip]

TUESDAY 10 NOVEMBER
[On ] 08:15  School drop-off       12 km    2.2 kWh
      Leave 07:50 · Recurring: School drop-off
      [Edit] [Disable this date]

[On ] 09:30  Office visit          76 km   13.7 kWh
      Calendar: Family            [Edit] [Disable]

[On ] 15:00  School pickup         12 km    2.2 kWh
      Recurring: School pickup     [Edit] [Disable this date]

[Off] 17:30  Optional evening visit 20 km   3.6 kWh
      Manual: disabled             [Edit] [Re-enable]

EDIT TRIP
Arrive by [date/time]       Destination [saved place/address]
Until     [time] (return leaves then)
Distance  [Calculated value / Manual override]
Enabled   [On]
[Cancel] [Save this occurrence]
```

Row times are arrive-by times (calendar event start, or the time entered for a manual/recurring trip); each row also shows the computed departure. The return leg leaves at the trip's end time. Calendar events with unclear car use show **Needs review** and count toward demand until disabled. Disabled rows are muted and excluded from demand. Edits survive refreshes and recurring-trip regeneration. Editing an occurrence changes that date; **Edit recurring schedule** is a separate action. Distances and energy recalculate immediately; displayed totals use unrounded values.

**Recurring trips — manage templates and their dated occurrences.**

```text
Recurring trips                            [+ Add schedule]

School drop-off                  [Enabled]
Mon–Fri, 08:15–08:20  Home -> School -> Home
[Edit schedule] [View occurrences] [Pause schedule]

School pickup                    [Enabled]
Mon–Fri, 15:00–15:15  Home -> School -> Home
[Edit schedule] [View occurrences] [Pause schedule]

Upcoming occurrences
Tue 10 [On]    Wed 11 [On]    Thu 12 [Off: skipped]
Fri 13 [On]    Mon 16 appears as the window advances
```

Materialize the rolling seven-day window and extend it daily. Preserve per-date changes; template edits affect future unmodified occurrences. Offer weekday and start/end-time selection, saved places, and date exceptions (holidays normally come from the calendar). The return leg leaves at the end time.

**Settings — personal choices in the UI; system connections in config.**

```text
Settings
Home & vehicle | Planning | Places | Advice | Notifications | Connections

HOME & VEHICLE [V1]
Home address          [Your address / saved location]
Usable capacity       [59] kWh
Charging power        [3.68] kW
Consumption           [18] kWh/100 km
Arrival reserve       [15] %
Charging efficiency   [90] %
Departure buffer      [15] minutes
[Save settings]

ADVICE [V2]
Daily advice                              [On]
Automatically create plans from advice    [OFF]
Manual mode: review a proposal and confirm each change.
Automatic mode requires configured targets and expiry rules.

PLANNING [V5]                       (rules: AUTOMATION.md)
Trip plans            [Manual / On plug-in / Automatic]
Charge ahead for later trips       [On]
Minimum saving        [0.05] €/kWh
Highest target when charging ahead [90] %
Household use during the day       [0.4] kW
Tax not paid on own solar          [0.11] €/kWh
Automatic plans: Paused — plan chosen in EVCC Tue 20:14 (80% by Wed 07:00)  [Resume]

NOTIFICATIONS [V5]
Notifications          [Off]
Phones                 [x] notify.mobile_app_phone   (from Home Assistant)
Link address           [address your phone uses to open the planner]
Car location sensor    [optional]
Early reminders        [On]  [30] minutes before the cheapest window
Urgent reminder        [30] minutes before the latest plug-in time
Quiet hours            [23:00] to [07:00]
Urgent in quiet hours  [Send]
Snooze                 [30] minutes
Charging problems [On]   Questions about calendar events [On]   Ready messages [Off]
[Save]  [Send test notification]
Recent notifications   Plug in by 10:20 · sent 09:50 · snoozed until 10:20

CONNECTIONS — configured on the server
EVCC              [Stub: demo feed]  [Check connection]
Google Calendar   [iCal: demo]       [Last sync / calendars]
Google Routes     [Stub]
OpenRouter        [Stub]             Model: configured model
KNMI              [Stub]
Home Assistant    [Not configured]
```

Home, trip defaults, recurrence rules, and preferences save to private local storage, excluded from Git. Private config can seed initial values. System URLs, providers/models, calendar IDs, and notification transport are file-configurable. Credentials remain in `.env`; the UI shows connection status without exposing secrets. Notification destinations can be selected once the provider is configured.

**Send plan to EVCC — manual, confirmed trip plan [V1].**

```text
Send plan to EVCC — Tuesday 07:50 departure
Target SoC             46%   (covers 100 km + 15% reserve)
Ready by               Tue 10 Nov, 07:50
Current EVCC plan      None   (or: 60% by Wed 08:00 — will be replaced)

[EVCC preview: charging windows and projected SoC]
EVCC keeps one plan per vehicle. Limits and repeating plans stay unchanged.

[Cancel]                         [Confirm and send]
```

Opening this dialog only reads EVCC (preview). Automatic sync of trip plans starts in V5.

**Create extra plan — an explicit, reviewable action [V2].**

```text
Create extra plan — Tuesday
Charge indicator: High       Confidence: 3/3

Target SoC or energy   [Choose % or kWh]
Ready by               [date/time]
Expires                 [date/time]

[EVCC preview: charging windows and trip readiness]
Previous background settings restore when this override ends,
unless you change them in EVCC in the meantime.

[Cancel]                         [Confirm and create plan]
```

Opening this dialog does not write to EVCC. The qualitative indicator itself has no fixed charging amount; any automatic target mapping must be explicitly configured by the user. Review postponement uses a similar proposal with a defer-until time; required trip charging remains protected. Advice auto-apply is off by default and independent of V5 trip automation.

**Delivery.** V1 delivers the shell, status/trip views, recurring schedules, personal settings, previews, and manual **Send plan to EVCC**. V2 adds advice cards, relevant graphs/settings, and extra-plan proposals. V3 adds the Activity (audit) page. V4 shows the SoC from Home Assistant while the car is unplugged, with its source and freshness. V5 adds automation state, notification preferences, reminders, and pause/resume. V6 leads the Overview with the ready check and adds the week strip, one combined chart, the day panel, bottom navigation on phones, and accessibility checks. V7/V8 extend health/recovery views and schedule explanations.
