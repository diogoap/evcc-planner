import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import type { Day, SoCView, Status, Trip } from '../api/types'
import DaysTable from './DaysTable.vue'
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
  live: false,
  observedAt: '2026-11-09T17:42:00Z',
  estimated: false,
  projected: 38,
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
  it('marks an unplugged SoC as last known with its time', () => {
    const w = mount(VehicleCard, { props: { soc: soc({}), status, now: '2026-11-09T18:00:00Z' } })
    expect(w.get('[data-testid=soc]').text()).toBe('38%')
    const tip = w.get('[data-testid=soc-stale]').text()
    expect(tip).toContain('Last known, Mon 9 Nov, 18:42')
    expect(tip).toContain('only reads the SoC while the car is plugged in')
    expect(w.text()).toContain('Unplugged')
  })

  it('shows live readings without the stale tip', () => {
    const w = mount(VehicleCard, {
      props: {
        soc: soc({ live: true }),
        status: { ...status, connected: true },
        now: '2026-11-09T17:42:00Z',
      },
    })
    expect(w.find('[data-testid=soc-stale]').exists()).toBe(false)
    expect(w.text()).toContain('Live from EVCC')
    expect(w.text()).toContain('Plugged in')
  })

  it('asks to plug in when the SoC was never read', () => {
    const w = mount(VehicleCard, {
      props: {
        soc: soc({ known: false, percent: null, observedAt: null }),
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
