import { flushPromises, mount, RouterLinkStub } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Block, Day, Plan, SendOption, Trip } from '../api/types'

const store = vi.hoisted(() => ({
  setTripEnabled: vi.fn(),
  confirmTrip: vi.fn(),
  resetTrip: vi.fn(),
  deleteTrip: vi.fn(),
}))
vi.mock('../store', () => store)
// Chart.js needs a canvas; the chart is not under test.
vi.mock('./PlanChart.vue', () => ({ default: { template: '<div data-testid="chart" />' } }))

const { default: DayPanel } = await import('./DayPanel.vue')

const day = (date: string, over: Partial<Day> = {}): Day => ({
  date,
  distanceKm: 100,
  energyKWh: 18,
  tripCount: 2,
  disabledCount: 0,
  doneCount: 0,
  reviewCount: 0,
  status: 'charge',
  chargePct: 3.8,
  targetSoc: 42,
  firstDepartAt: '2026-11-10T06:48:00Z',
  ...over,
})

const trip = {
  key: 'recurring:1:2026-11-10',
  title: 'School drop-off',
  date: '2026-11-10',
  blockIndex: 0,
} as Trip
const block = {
  index: 0,
  tripKeys: [trip.key],
  departAt: '2026-11-10T06:48:00Z',
  targetSoc: 42,
  status: 'charge',
  chargeKWh: 2.3,
  chargeMinutes: 42,
  latestChargeStart: '2026-11-10T05:36:00Z',
} as Block
const option = { days: 1, from: '2026-11-10', through: '2026-11-10', tripKeys: [trip.key] } as SendOption

const plan = {
  now: '2026-11-09T18:00:00Z',
  days: [day('2026-11-09', { status: 'completed', doneCount: 1 }), day('2026-11-10'), day('2026-11-11')],
  trips: [trip],
  blocks: [block],
  next: block,
  sendOptions: [option],
  timeline: [],
  settings: { arrivalReservePct: 15 },
  advice: { enabled: false, autoApply: false, days: [], sources: [], adjustments: [] },
} as unknown as Plan

// Tuesday's expected charging: 03:00–03:30 local, at 0.16 and 0.18.
const charging = [
  { start: '2026-11-10T02:00:00Z', end: '2026-11-10T02:15:00Z', value: 0.16 },
  { start: '2026-11-10T02:15:00Z', end: '2026-11-10T02:30:00Z', value: 0.18 },
]

const panel = (date = '2026-11-10') =>
  mount(DayPanel, {
    props: { date, plan, status: null, charging, canSend: true },
    global: {
      stubs: { RouterLink: RouterLinkStub, TripRow: { props: ['trip'], template: '<li class="trip" />' } },
    },
    attachTo: document.body,
  })

// A one-finger swipe from x0 to x1.
function swipe(el: Element, x0: number, x1: number) {
  const at = (type: string, x: number, list: string) => {
    const e = new Event(type, { bubbles: true })
    Object.defineProperty(e, list, { value: [{ clientX: x, clientY: 300 }] })
    el.dispatchEvent(e)
  }
  at('touchstart', x0, 'touches')
  at('touchend', x1, 'changedTouches')
}

describe('DayPanel', () => {
  afterEach(() => (document.body.innerHTML = ''))

  it('shows the day, its trips, what it needs to charge, and quick actions', async () => {
    const w = panel()
    await flushPromises()
    expect(w.get('#day-panel-title').text()).toBe('Tuesday 10 November')
    expect(w.text()).toContain('Tomorrow')
    expect(document.activeElement).toBe(w.get('#day-panel-title').element)
    const summary = w.get('[data-testid=day-summary]').text()
    for (const text of ['100 km', '18.0 kWh', '+4% charge', '42% by 07:48']) expect(summary).toContain(text)
    expect(w.findAll('.trip')).toHaveLength(1)
    expect(w.get('[data-testid=day-charging]').text()).toContain('07:48 needs 42%')
    expect(w.get('[data-testid=day-windows]').text()).toContain('Expected 03:00–03:30 at about €0.17/kWh')

    await w.get('[data-testid=day-add]').trigger('click')
    expect(w.emitted('add')).toEqual([['2026-11-10']])
    expect(w.get('[data-testid=day-send]').text()).toBe('Send plan: Trips tomorrow')
    await w.get('[data-testid=day-send]').trigger('click')
    expect(w.emitted('send')).toEqual([[option]])
    w.unmount()
  })

  it('moves between days with the arrows or a swipe, and closes with Escape', async () => {
    const w = panel()
    await w.get('[aria-label="Next day"]').trigger('click')
    swipe(w.get('[data-testid=day-panel]').element, 300, 100)
    swipe(w.get('[data-testid=day-panel]').element, 100, 300)
    expect(w.emitted('select')).toEqual([['2026-11-11'], ['2026-11-11'], ['2026-11-09']])
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    expect(w.emitted('close')).toHaveLength(1)
    w.unmount()

    const first = panel('2026-11-09')
    expect(first.get('[aria-label="Previous day"]').attributes('disabled')).toBeDefined()
    expect(first.text()).toContain('No more trips.')
    expect(first.find('[data-testid=day-send]').exists()).toBe(false)
    first.unmount()
  })
})
