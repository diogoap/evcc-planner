# EVCC Trip Planner — text UI mockup

Illustrative demo data only, set on Monday evening, 9 November 2026. EVCC-inspired: spacious cards, rounded corners, clear typography, muted surfaces, and restrained green/amber accents. Layouts stack on mobile; confidence and status always have text labels. All energy prices include VAT.

**Overview — the default screen.**

```text
EVCC Trip Planner                         DEMO
Overview       Trips       Recurring trips       Settings

NISSAN LEAF                  ENERGY NOW
38%  [########------------]  Grid: EUR 0.24/kWh incl. VAT
(i) Last known: Mon 18:42    Solar: 0.0 kW
Unplugged · Charging 0.0 kW  Data updated: just now

NEXT DEPARTURE               CONTROL
Tue 10 Nov, leave 07:50      Trip plans: Send manually [V1]
At school by 08:15           Advice auto-apply: Off
Required departure SoC: 46%  [Settings]
[View trips] [Send plan to EVCC]

NEXT SEVEN DAYS                            [+ Add trip]
Day         Driving   Energy   Daily advice [V2]
Mon 09      24 km     4.3 kWh   Completed
Tue 10     100 km    18.0 kWh   Charge ahead
Wed 11      24 km     4.3 kWh   Defer optional charging
Thu 12      24 km     4.3 kWh   No data / no suggestion
Fri 13      24 km     4.3 kWh   Keep plan
Sat 14       0 km     0.0 kWh   No plan needed
Sun 15       0 km     0.0 kWh   No plan needed

TUESDAY'S ADVICE [V2]
Charge ahead      Charge indicator: High (more)
Confidence: 3/3 — High
Published Tuesday prices are favourable compared with
the later-week outlook. Upcoming trips may benefit.
Sources: published prices + weekly outlook + weather
[Create extra plan] [Dismiss] [Why?]

PRICE AND CHARGING GRAPH
[Published price slots | planned charging overlay]
Confirmed and estimated values are visually distinct.
```

EVCC only reports SoC while the car is plugged in. When unplugged, the card shows the last known value with an info tip giving when it was read; it is never presented as current. Click a day to see its trips and advice. Confidence **1 low / 2 medium / 3 high** is separate from the **little / medium / high** charge indicator. Weak/no-data advice shows its reason and no plan action. Deferred charging has **Review postponement**; keeping a plan needs no action. Long-range days use qualitative signals unless numerical estimates are actually available.

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

**Delivery.** V1 delivers the shell, status/trip views, recurring schedules, personal settings, previews, and manual **Send plan to EVCC**. V2 adds advice cards, relevant graphs/settings, and extra-plan proposals. V3 adds the Activity (audit) page. V4 shows the SoC from Home Assistant while the car is unplugged, with its source and freshness. V5 adds automation state, notification preferences, reminders, and pause/resume. V6 polishes the visual design, graphs, and phone experience. V7/V8 extend health/recovery views and schedule explanations.
