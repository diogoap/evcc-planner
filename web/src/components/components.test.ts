import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import type { AuditEvent, Day, SendOption, SoCView, Status, Trip } from '../api/types'
import ActivityItem from './ActivityItem.vue'
import DaysTable from './DaysTable.vue'
import SendPlanButton from './SendPlanButton.vue'
import TripRow from './TripRow.vue'
import VehicleCard from './VehicleCard.vue'

const status: Status = {
  mode: 'stub',
  reachable: true,
  connected: false,
  charging: false,
  chargePowerW: 0,
  gridPrice: 0.24,
  pvPowerW: 0,
  priceSlots: [],
  plan: null,
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

describe('VehicleCard', () => {
  it('marks a stale EVCC SoC as last known with its time', () => {
    const w = mount(VehicleCard, {
      props: { soc: soc({ stale: true }), status, now: '2026-11-09T22:00:00Z' },
    })
    expect(w.get('[data-testid=soc]').text()).toBe('38%')
    expect(w.get('[data-testid=soc-range]').text()).toBe('122 km range')
    const tip = w.get('[data-testid=soc-stale]').text()
    expect(tip).toContain('Last known, Mon 9 Nov, 18:42, from EVCC')
    expect(tip).toContain('only reads the SoC while the car is plugged in')
    expect(w.find('.bar-fill.stale').exists()).toBe(true)
    expect(w.text()).toContain('Unplugged')
  })

  it('shows a recent EVCC reading from before unplugging as fresh', () => {
    const w = mount(VehicleCard, { props: { soc: soc({}), status, now: '2026-11-09T18:00:00Z' } })
    expect(w.find('[data-testid=soc-stale]').exists()).toBe(false)
    expect(w.get('[data-testid=soc-source]').text()).toBe(
      'From EVCC · 18 min ago, read before the car was unplugged',
    )
  })

  it('shows a Home Assistant reading with its age and the other source', () => {
    const w = mount(VehicleCard, {
      props: {
        soc: soc({
          source: 'homeassistant',
          percent: 61,
          rangeKm: null,
          other: [{ source: 'evcc', percent: 70, observedAt: '2026-11-08T21:10:00Z', rangeKm: 224 }],
        }),
        status,
        now: '2026-11-09T18:00:00Z',
      },
    })
    expect(w.get('[data-testid=soc]').text()).toBe('61%')
    expect(w.get('[data-testid=soc-source]').text()).toBe('From Home Assistant · 18 min ago')
    expect(w.get('[data-testid=soc-range]').text()).toBe('Range: not reported')
    expect(w.get('[data-testid=soc-other]').text()).toBe('EVCC: 70% · 21 h ago')
  })

  it('flags a Home Assistant reading that stopped updating', () => {
    const w = mount(VehicleCard, {
      props: { soc: soc({ source: 'homeassistant', stale: true }), status, now: '2026-11-09T23:00:00Z' },
    })
    const tip = w.get('[data-testid=soc-stale]').text()
    expect(tip).toContain('Last known, Mon 9 Nov, 18:42, from Home Assistant')
    expect(tip).toContain('Home Assistant has not updated it since')
  })

  it('shows live readings without the stale tip', () => {
    const w = mount(VehicleCard, {
      props: {
        soc: soc({ live: true, rangeKm: null }),
        status: { ...status, connected: true },
        now: '2026-11-09T17:42:00Z',
      },
    })
    expect(w.find('[data-testid=soc-stale]').exists()).toBe(false)
    expect(w.text()).toContain('Live from EVCC')
    // Not reported yet (e.g. a reading saved before EVCC gave the range).
    expect(w.get('[data-testid=soc-range]').text()).toBe('Range: read when plugged in')
    expect(w.text()).toContain('Plugged in')
  })

  it('asks to plug in when the SoC was never read', () => {
    const w = mount(VehicleCard, {
      props: {
        soc: soc({ known: false, percent: null, observedAt: null, rangeKm: null }),
        status,
        now: '2026-11-09T18:00:00Z',
      },
    })
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

describe('DaysTable', () => {
  it('lists days and emits the selected date', async () => {
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
    const w = mount(DaysTable, { props: { days } })
    const row = w.get('[data-testid="day-2026-11-10"]')
    expect(row.text()).toContain('Tue 10')
    expect(row.text()).toContain('100 km')
    expect(row.text()).toContain('18.0 kWh')
    expect(row.text()).toContain('+4% charge')
    expect(row.text()).toContain('42% by 07:48')
    expect(row.text()).toContain('1 to review')
    await row.trigger('click')
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
