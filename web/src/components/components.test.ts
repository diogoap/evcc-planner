import { flushPromises, mount, RouterLinkStub } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import type { AuditEvent, Block, Day, Plan, SendOption, SoCView, Status, Trip } from '../api/types'
import ActivityItem from './ActivityItem.vue'
import ModalDialog from './ModalDialog.vue'
import SendPlanButton from './SendPlanButton.vue'
import ReadyCard from './ReadyCard.vue'
import TripRow from './TripRow.vue'
import WeekStrip from './WeekStrip.vue'

const status: Status = {
  mode: 'stub',
  reachable: true,
  connected: false,
  charging: false,
  chargePowerW: 0,
  gridPrice: 0.24,
  pvPowerW: 0,
  priceSlots: [],
  solar: [],
  plan: null,
  location: null,
  vehicleTitle: 'Nissan Leaf',
}

const soc = (over: Partial<SoCView>): SoCView => ({
  percent: 38,
  known: true,
  source: 'evcc',
  live: false,
  observedAt: '2026-11-09T17:42:00Z',
  stale: false,
  estimated: false,
  projected: 38,
  rangeKm: 122,
  other: [],
  ...over,
})

const trip = (over: Partial<Trip>): Trip => ({
  key: 'recurring:1:2026-11-10',
  source: 'recurring',
  sourceLabel: 'School drop-off',
  title: 'School drop-off',
  destination: 'School',
  address: 'Schoolstraat 1',
  placeName: 'School',
  date: '2026-11-10',
  arriveAt: '2026-11-10T07:15:00Z',
  endAt: '2026-11-10T07:20:00Z',
  arriveLocal: '08:15',
  endLocal: '08:20',
  departAt: '2026-11-10T06:48:00Z',
  returnAt: '2026-11-10T07:32:00Z',
  departLocal: '07:48',
  enabled: true,
  needsReview: false,
  overridden: [],
  distanceKm: 12,
  distanceOverrideKm: null,
  distanceSource: 'route',
  energyKWh: 2.16,
  state: 'upcoming',
  ...over,
})

// Tuesday's school run, leaving 07:48 local, needing 42% with 2.3 kWh to charge.
const block = (over: Partial<Block> = {}): Block => ({
  index: 0,
  tripKeys: ['recurring:1:2026-11-10', 'calendar:office'],
  titles: ['School drop-off', 'Office visit'],
  firstTitle: 'School drop-off',
  firstDestination: 'School',
  firstArriveAt: '2026-11-10T07:15:00Z',
  departAt: '2026-11-10T06:48:00Z',
  returnAt: '2026-11-10T11:30:00Z',
  distanceKm: 100,
  energyKWh: 18,
  energyPct: 30,
  ownRequiredSoc: 19,
  requiredSoc: 41.8,
  targetSoc: 42,
  status: 'charge',
  overlap: false,
  startSoc: 38,
  departSoc: 42,
  arrivalSoc: 15,
  chargePct: 4,
  chargeKWh: 2.3,
  gridKWh: 2.6,
  chargeMinutes: 42,
  chargeFrom: null,
  latestChargeStart: '2026-11-10T05:36:00Z',
  shortfall: 0,
  ...over,
})

const plan = (over: Partial<Plan> = {}) =>
  ({
    now: '2026-11-09T18:00:00Z',
    soc: soc({}),
    next: block(),
    sendOptions: [],
    control: { mode: 'automatic', paused: false, target: null },
    advice: { enabled: true, autoApply: false, days: [], sources: [], adjustments: [] },
    ...over,
  }) as Plan

const ready = (p: Plan, s: Status | null = status) =>
  mount(ReadyCard, {
    props: { plan: p, status: s, canSend: true },
    global: { stubs: { RouterLink: RouterLinkStub } },
  })

describe('ReadyCard', () => {
  it('says when to plug in for the next departure and what it needs', () => {
    const w = ready(plan())
    expect(w.get('[data-testid=ready-headline]').text()).toBe('Plug in by 06:36 for tomorrow 07:48')
    expect(w.get('.ready-icon').classes()).toContain('tone-bad')
    expect(w.get('[data-testid=next-needs]').text()).toBe('needs 42%')
    expect(w.get('[data-testid=next-trips]').text()).toBe(
      'School drop-off, at School by 08:15 · then Office visit',
    )
    expect(w.get('[data-testid=next-charge]').text()).toBe('2.3 kWh to charge, about 42 min')
    expect(w.get('[data-testid=connection]').text()).toBe('Unplugged')
    expect(w.find('[data-testid=location]').exists()).toBe(false)
    expect(w.get('[data-testid=energy-now]').text()).toBe('€0.24/kWh incl. VAT · solar 0.0 kW')
    expect(w.get('[data-testid=evcc-plan]').text()).toBe('None')
  })

  it('follows the car: charging, plugged in, ready, short of time, or no trips', () => {
    const charging = ready(plan(), { ...status, connected: true, charging: true, chargePowerW: 3680 })
    expect(charging.get('[data-testid=ready-headline]').text()).toBe('Charging for tomorrow 07:48')
    expect(charging.get('.ready-icon').classes()).toContain('tone-ok')
    expect(charging.get('[data-testid=connection]').text()).toBe('Charging 3.7 kW')

    const waiting = ready(plan(), { ...status, connected: true })
    expect(waiting.get('[data-testid=ready-headline]').text()).toBe('Plugged in for tomorrow 07:48')
    expect(waiting.get('[data-testid=connection]').text()).toBe('Plugged in')

    const covered = ready(plan({ next: block({ status: 'ready' }) }))
    expect(covered.get('[data-testid=ready-headline]').text()).toBe('Ready for tomorrow 07:48')
    expect(covered.get('.ready-icon').classes()).toContain('tone-ok')
    expect(covered.find('[data-testid=next-charge]').exists()).toBe(false)

    const short = ready(plan({ next: block({ status: 'shortfall', shortfall: 4.2 }) }))
    expect(short.get('[data-testid=ready-headline]').text()).toBe(
      'Not enough time to charge for tomorrow 07:48',
    )
    expect(short.get('[data-testid=next-charge]').text()).toContain('about 5% short at departure')

    const none = ready(plan({ next: null }))
    expect(none.get('[data-testid=ready-headline]').text()).toBe('No trips in the next seven days')
    expect(none.text()).toContain('Add a trip')
  })

  it('shows where Home Assistant puts the car', () => {
    const at = (state: string) =>
      ready(plan(), { ...status, location: { state, home: state === 'home', since: '2026-11-09T16:00:00Z' } })
    expect(at('home').get('[data-testid=location]').text()).toBe('Home')
    expect(at('not_home').get('[data-testid=location]').text()).toBe('Away')
    expect(at('Work').get('[data-testid=location]').text()).toBe('Work')
  })

  it('shows a paused automatic plan, the plan in EVCC, and active extra plans', async () => {
    const w = ready(
      plan({
        control: {
          mode: 'automatic',
          paused: true,
          pausedPlan: { soc: 60, time: '2026-11-10T06:48:00Z' },
          target: null,
        },
        advice: {
          enabled: true,
          autoApply: true,
          days: [],
          sources: [],
          adjustments: [
            {
              id: 7,
              kind: 'extra_plan',
              status: 'active',
              plan: { soc: 60, time: '2026-11-10T06:48:00Z' },
              expiresAt: '2026-11-10T06:48:00Z',
            } as Plan['advice']['adjustments'][number],
          ],
        },
      }),
      {
        ...status,
        reachable: false,
        error: 'timeout',
        plan: { soc: 60, time: '2026-11-10T06:48:00Z', setByPlanner: false },
      },
    )
    expect(w.get('[data-testid=trip-plans]').text()).toBe('Automatic · paused')
    expect(w.get('[data-testid=control-detail]').text()).toContain('chosen plan (60% by Tue 10 Nov, 07:48)')
    expect(w.get('[data-testid=evcc-plan]').text()).toBe('60% by Tue 10 Nov, 07:48 · Set in EVCC')
    expect(w.get('[data-testid=active-adjustment]').text()).toContain('restores Tue 10 Nov, 07:48')
    expect(w.get('[data-testid=auto-apply]').text()).toBe('Plans created automatically')
    expect(w.get('[data-testid=energy-now]').text()).toContain('EVCC unreachable: timeout')
    await w.get('[data-testid=resume]').trigger('click')
    expect(w.emitted('resume')).toHaveLength(1)
    await w.get('[data-testid=active-adjustment] button').trigger('click')
    expect(w.emitted('cancel')).toEqual([[7]])
  })

  it('shows just the SoC and range, with each source in the tooltip', () => {
    const w = ready(
      plan({
        soc: soc({
          source: 'homeassistant',
          percent: 61,
          rangeKm: null,
          other: [{ source: 'evcc', percent: 70, observedAt: '2026-11-08T21:10:00Z', rangeKm: 224 }],
        }),
      }),
    )
    expect(w.get('[data-testid=soc]').text()).toBe('61%')
    expect(w.get('[data-testid=soc-range]').text()).toBe('Range: not reported')
    expect(w.get('[data-testid=soc]').element.parentElement!.title).toBe(
      'Home Assistant: 61% · 18 min ago\nEVCC: 70% · 21 h ago',
    )
    expect(w.text()).not.toContain('From Home Assistant')
    expect(ready(plan()).get('[data-testid=soc-range]').text()).toBe('122 km range')
  })

  it('marks a stale SoC as last known with its time and source', () => {
    const evcc = ready(plan({ soc: soc({ stale: true }), now: '2026-11-09T22:00:00Z' }))
    expect(evcc.get('[data-testid=soc-stale]').text()).toBe('Last known, Mon 9 Nov, 18:42, from EVCC')
    expect(evcc.find('.bar-fill.stale').exists()).toBe(true)
    const ha = ready(
      plan({ soc: soc({ source: 'homeassistant', stale: true }), now: '2026-11-09T23:00:00Z' }),
    )
    expect(ha.get('[data-testid=soc-stale]').text()).toBe('Last known, Mon 9 Nov, 18:42, from Home Assistant')
    expect(ready(plan()).find('[data-testid=soc-stale]').exists()).toBe(false)
  })

  it('asks to plug in when the SoC was never read', () => {
    const w = ready(plan({ soc: soc({ known: false, percent: null, observedAt: null, rangeKm: null }) }))
    expect(w.text()).toContain('SoC unknown')
  })
})

describe('TripRow', () => {
  it('toggles a date and shows source and departure', async () => {
    const w = mount(TripRow, { props: { trip: trip({}) } })
    expect(w.text()).toContain('Leave 07:48')
    expect(w.text()).toContain('Recurring: School drop-off')
    expect(w.text()).toContain('12 km')
    await w.get('[role=switch]').trigger('click')
    expect(w.emitted('toggle')).toEqual([[false]])
    expect(w.text()).not.toContain('Reset')
    expect(w.text()).not.toContain('Delete')
  })

  it('shows the departure target when given', () => {
    const w = mount(TripRow, { props: { trip: trip({}), target: 42 } })
    expect(w.get('[data-testid=departure-target]').text()).toBe('Needs 42% at departure')
    expect(
      mount(TripRow, { props: { trip: trip({}) } })
        .find('[data-testid=departure-target]')
        .exists(),
    ).toBe(false)
  })

  it('offers reset for edited dates and delete for manual trips only', () => {
    const edited = mount(TripRow, { props: { trip: trip({ overridden: ['time'] }) } })
    expect(edited.text()).toContain('Reset')
    expect(edited.text()).toContain('Edited')
    const manual = mount(TripRow, {
      props: { trip: trip({ source: 'manual', key: 'manual:1', overridden: [] }) },
    })
    expect(manual.text()).toContain('Delete')
    expect(manual.text()).toContain('Manual')
  })

  it('opens the trip history', async () => {
    const w = mount(TripRow, { props: { trip: trip({}) } })
    await w.get('[aria-label="History of School drop-off on 2026-11-10"]').trigger('click')
    expect(w.emitted('history')).toHaveLength(1)
  })

  it('mutes disabled trips and asks to review unclear calendar trips', async () => {
    const w = mount(TripRow, {
      props: { trip: trip({ enabled: false, needsReview: true, source: 'calendar', sourceLabel: 'Family' }) },
    })
    expect(w.classes()).toContain('off')
    expect(w.get('[role=switch]').attributes('aria-checked')).toBe('false')
    await w.get('button.btn').trigger('click')
    expect(w.emitted('review')).toHaveLength(1)
  })
})

describe('TripRow without a route', () => {
  it('locks the switch and explains why', () => {
    const w = mount(TripRow, {
      props: {
        trip: trip({
          source: 'calendar',
          sourceLabel: 'Kids',
          destination: '',
          placeName: undefined,
          enabled: false,
          distanceKm: null,
          energyKWh: null,
          disabledReason: 'No location',
        }),
      },
    })
    const toggle = w.get('[role=switch]')
    expect(toggle.attributes('disabled')).toBeDefined()
    expect(toggle.attributes('aria-checked')).toBe('false')
    expect(w.get('[data-testid=disabled-reason]').text()).toBe('No location')
    expect(w.text()).toContain('edit to add a destination')
  })
})

describe('WeekStrip', () => {
  it('shows each day’s charge and figures, reads it out in full, and emits the selected date', async () => {
    const days: Day[] = [
      {
        date: '2026-11-10',
        distanceKm: 100,
        energyKWh: 18,
        tripCount: 3,
        disabledCount: 0,
        doneCount: 0,
        reviewCount: 1,
        status: 'charge',
        chargePct: 3.8,
        requiredSoc: 41.8,
        targetSoc: 42,
        firstDepartAt: '2026-11-10T06:48:00Z',
      },
    ]
    const w = mount(WeekStrip, { props: { days, advice: [] } })
    const tile = w.get('[data-testid="day-2026-11-10"]')
    for (const text of ['Tue', '10', '+4%', '100 km, 18.0 kWh', '42% by 07:48', '1 trip to review'])
      expect(tile.text()).toContain(text)
    // Words for screen readers where the tile shows a symbol.
    expect(tile.get('.week-charge .sr-only').text()).toBe('+4% charge')
    await tile.trigger('click')
    expect(w.emitted('select')).toEqual([['2026-11-10']])
  })
})

describe('ActivityItem', () => {
  const event: AuditEvent = {
    id: 7,
    at: '2026-11-09T18:02:00Z',
    type: 'action',
    source: 'ui',
    action: 'trip.disable',
    subjectType: 'trip',
    subjectId: 'recurring:2:2026-11-12',
    related: 'schedule:2',
    summary: 'Disabled School pickup on Thu 12 Nov',
    before: { enabled: true },
    after: { enabled: false },
    outcome: 'success',
  }

  it('shows the change and links to the trip history', async () => {
    const w = mount(ActivityItem, { props: { event, showHistory: true } })
    expect(w.text()).toContain('19:02')
    expect(w.text()).toContain('Disabled School pickup on Thu 12 Nov')
    expect(w.text()).toContain('UI')
    const change = w.get('[data-testid=event-changes] tr')
    expect(change.get('th').text()).toBe('On')
    expect(change.get('td').text()).toBe('Yes → No')
    await w.get('[aria-label="History of this trip"]').trigger('click')
    expect(w.emitted('history')).toEqual([['trip:recurring:2:2026-11-12', 'this trip']])
    expect(mount(ActivityItem, { props: { event } }).find('[aria-label^="History"]').exists()).toBe(false)
  })

  it('marks failed external calls with their status and details', () => {
    const w = mount(ActivityItem, {
      props: {
        event: {
          ...event,
          type: 'api_call',
          source: 'calendar_sync',
          action: 'api.call',
          subjectType: 'api',
          service: 'calendar',
          summary: 'Calendar feed: Family',
          before: null,
          after: null,
          detail: { method: 'GET', endpoint: 'calendar.google.com' },
          outcome: 'failure',
          error: 'HTTP 500 Internal Server Error',
          statusCode: 500,
          durationMs: 120,
          bytes: 0,
        },
      },
    })
    expect(w.classes()).toContain('failed')
    expect(w.text()).toContain('Google Calendar')
    expect(w.text()).toContain('Calendar sync')
    expect(w.text()).toContain('Failed')
    expect(w.text()).toContain('HTTP 500 · 120 ms · 0 B')
    expect(w.get('[data-testid=event-error]').text()).toBe('HTTP 500 Internal Server Error')
    expect(w.find('[data-testid=event-changes]').exists()).toBe(false)
    expect(w.get('details').text()).toContain('Endpoint')
  })
})

describe('SendPlanButton', () => {
  const option = (
    days: number,
    through: string,
    trips: number,
    km: number,
    targetSoc: number,
    covered = false,
  ): SendOption => ({
    days,
    from: '2026-11-10',
    through,
    readyBy: '2026-11-10T06:48:00Z',
    requiredSoc: targetSoc,
    targetSoc,
    tripKeys: Array.from({ length: trips }, (_, i) => `t${i}`),
    titles: [],
    distanceKm: km,
    energyKWh: 0,
    exceeds: false,
    covered,
  })
  const options = [
    option(1, '2026-11-10', 3, 100, 46, true),
    option(2, '2026-11-11', 3, 100, 46),
    option(3, '2026-11-12', 6, 132, 56),
  ]
  const now = '2026-11-09T18:00:00Z'

  it('sends the next departure, or a plan covering more days from the menu', async () => {
    const w = mount(SendPlanButton, { props: { options, now, disabled: false }, attachTo: document.body })
    await w.get('.split-main').trigger('click')
    expect(w.emitted('send')).toEqual([[]])

    const toggle = w.get('[data-testid=send-more]')
    expect(toggle.attributes('aria-expanded')).toBe('false')
    await toggle.trigger('click')
    expect(toggle.attributes('aria-expanded')).toBe('true')
    expect(w.get('[data-testid=send-option-1]').text()).toContain('Trips tomorrow')
    expect(w.get('[data-testid=send-option-1]').text()).toContain('46%')
    expect(w.get('[data-testid=send-option-1]').text()).toContain('3 trips · 100 km')
    // The current charge already reaches day 1's target, not day 3's.
    expect(w.get('[data-testid=send-option-1] [data-testid=option-covered]').text()).toBe('Covered')
    expect(w.find('[data-testid=send-option-3] [data-testid=option-covered]').exists()).toBe(false)
    // Wednesday adds no trips: nothing to choose.
    const wed = w.get('[data-testid=send-option-2]')
    expect(wed.attributes('disabled')).toBeDefined()
    expect(wed.text()).toContain('No trips on Wed 11 Nov')
    expect(w.get('[data-testid=send-option-3]').text()).toContain('Tomorrow to Thu 12 Nov')

    await w.get('[data-testid=send-option-3]').trigger('click')
    expect(w.emitted('send')?.[1]).toEqual([options[2]])
    expect(w.find('[data-testid=send-option-1]').exists()).toBe(false)
    w.unmount()
  })

  it('closes with Escape and stays off while EVCC is unreachable', async () => {
    const w = mount(SendPlanButton, { props: { options, now, disabled: false }, attachTo: document.body })
    await w.get('[data-testid=send-more]').trigger('click')
    await w.get('.split').trigger('keydown', { key: 'Escape' })
    expect(w.find('[data-testid=send-option-1]').exists()).toBe(false)
    w.unmount()

    const off = mount(SendPlanButton, { props: { options, now, disabled: true } })
    expect(off.get('.split-main').attributes('disabled')).toBeDefined()
    expect(off.get('[data-testid=send-more]').attributes('disabled')).toBeDefined()
  })
})

describe('ModalDialog', () => {
  it('focuses the first control, keeps Tab inside, and returns focus on close', async () => {
    const opener = document.createElement('button')
    document.body.append(opener)
    opener.focus()
    const w = mount(ModalDialog, {
      props: { title: 'Edit trip' },
      slots: { default: '<input data-testid="first" /><button data-testid="last">Save</button>' },
      attachTo: document.body,
    })
    await flushPromises()
    const first = w.get('[data-testid=first]').element
    const last = w.get('[data-testid=last]').element as HTMLElement
    expect(document.activeElement).toBe(first)

    last.focus()
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab' }))
    expect(document.activeElement).toBe(first)
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', shiftKey: true }))
    expect(document.activeElement).toBe(last)

    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    expect(w.emitted('close')).toHaveLength(1)
    w.unmount()
    expect(document.activeElement).toBe(opener)
    opener.remove()
  })

  it('stacks: Escape closes only the top one, and the page stays locked until the last closes', () => {
    const below = mount(ModalDialog, { props: { title: 'Day' }, attachTo: document.body })
    const top = mount(ModalDialog, { props: { title: 'Edit trip' }, attachTo: document.body })
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    expect(top.emitted('close')).toHaveLength(1)
    expect(below.emitted('close')).toBeUndefined()
    top.unmount()
    expect(document.documentElement.classList.contains('modal-open')).toBe(true)
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    expect(below.emitted('close')).toHaveLength(1)
    below.unmount()
    expect(document.documentElement.classList.contains('modal-open')).toBe(false)
  })

  it('stops the page behind from scrolling while open', () => {
    const w = mount(ModalDialog, { props: { title: 'Edit trip' }, attachTo: document.body })
    expect(document.documentElement.classList.contains('modal-open')).toBe(true)
    w.unmount()
    expect(document.documentElement.classList.contains('modal-open')).toBe(false)
  })
})
