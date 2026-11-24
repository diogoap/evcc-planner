import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { Trip } from '../api/types'

const api = vi.hoisted(() => ({ createTrip: vi.fn(), updateTrip: vi.fn() }))
vi.mock('../api/client', () => ({ api }))

const { default: TripEditor } = await import('./TripEditor.vue')

const home = { name: 'Home', placeName: 'Home', home: true }
const trip = (over: Partial<Trip>): Trip => ({
  key: 'manual:1',
  source: 'manual',
  sourceLabel: 'Manual',
  title: 'Errands',
  destination: 'School',
  address: 'Schoolstraat 1',
  placeName: 'School',
  date: '2026-11-10',
  arriveAt: '2026-11-10T09:00:00Z',
  endAt: '2026-11-10T10:00:00Z',
  arriveLocal: '10:00',
  endLocal: '11:00',
  departAt: '2026-11-10T08:33:00Z',
  returnAt: '2026-11-10T10:12:00Z',
  departLocal: '09:33',
  enabled: true,
  needsReview: false,
  overridden: [],
  points: [home, { name: 'School', placeName: 'School', home: false }, home],
  legs: [
    { distanceKm: 6, minutes: 12 },
    { distanceKm: 6, minutes: 12 },
  ],
  oneWay: false,
  distanceKm: 12,
  distanceOverrideKm: null,
  distanceSource: 'route',
  energyKWh: 2.16,
  state: 'upcoming',
  ...over,
})

const open = (t?: Trip) =>
  mount(TripEditor, {
    props: { trip: t, date: '2026-11-10', places: [], defaultStayMin: 15 },
    attachTo: document.body,
  })
const names = (w: ReturnType<typeof open>) =>
  w.findAll('[data-testid=route-points] input').map((i) => (i.element as HTMLInputElement).value)
const labels = (w: ReturnType<typeof open>) =>
  w.findAll('[data-testid=route-points] label').map((l) => l.text())
const button = (w: ReturnType<typeof open>, name: string) => w.get(`button[aria-label="${name}"]`)

beforeEach(() => {
  api.createTrip.mockReset().mockResolvedValue({ key: 'manual:2' })
  api.updateTrip.mockReset().mockResolvedValue({ key: 'manual:1' })
})

describe('TripEditor route', () => {
  it('starts as home → destination → home with the legs as calculated', () => {
    const w = open(trip({}))
    expect(names(w)).toEqual(['Home', 'School', 'Home'])
    expect(labels(w)).toEqual(['Start', 'Stop 1', 'End'])
    expect(w.findAll('[data-testid=route-leg]').map((l) => l.text())).toEqual([
      '6.0 km · 12 min',
      '6.0 km · 12 min',
    ])
    expect(button(w, 'Move Start up').attributes('disabled')).toBeDefined()
    expect(button(w, 'Move End down').attributes('disabled')).toBeDefined()
    w.unmount()
  })

  it('adds stops before the end, up to five points, and saves the route', async () => {
    const w = open(trip({}))
    const add = w.findAll('button').find((b) => b.text() === 'Add stop')!
    await add.trigger('click')
    expect(names(w)).toEqual(['Home', 'School', '', 'Home'])
    // The legs were for the old route.
    expect(w.findAll('[data-testid=route-leg]')).toHaveLength(0)
    expect(w.text()).toContain('Arrive by (first stop)')
    expect(w.text()).toContain('Until (leave the last stop)')
    await w.findAll('[data-testid=route-points] input')[2].setValue('Bouwmarkt 5')
    await add.trigger('click')
    expect(add.attributes('disabled')).toBeDefined()
    expect(w.text()).toContain('5 of 5 points')
    await button(w, 'Remove Stop 3').trigger('click')
    await flushPromises()
    // Focus stays on a button where the point was, not on a field.
    expect(document.activeElement).toBe(button(w, 'Remove End').element)
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(api.updateTrip).toHaveBeenCalledWith(
      'manual:1',
      expect.objectContaining({ points: ['Home', 'School', 'Bouwmarkt 5', 'Home'], end: '11:00' }),
    )
    expect(w.emitted('saved')).toEqual([['Trip updated']])
    w.unmount()
  })

  it('moves points, and a one-way trip has no end time', async () => {
    const w = open()
    await w.findAll('[data-testid=route-points] input')[1].setValue('Airport 1')
    await button(w, 'Remove End').trigger('click')
    await flushPromises()
    expect(document.activeElement?.textContent?.trim()).toBe('Add stop')
    expect(names(w)).toEqual(['Home', 'Airport 1'])
    expect(labels(w)).toEqual(['Start', 'End'])
    expect(w.text()).not.toContain('Until')
    expect(w.find('[data-testid=route-ends-away]').exists()).toBe(true)
    expect(button(w, 'Remove End').attributes('disabled')).toBeDefined()
    await button(w, 'Move End up').trigger('click')
    await flushPromises()
    // At the top it can only move down again.
    expect(document.activeElement).toBe(button(w, 'Move Start down').element)
    expect(names(w)).toEqual(['Airport 1', 'Home'])
    expect(w.find('[data-testid=route-starts-away]').exists()).toBe(true)
    expect(w.find('[data-testid=route-ends-away]').exists()).toBe(false)
    await w.get('form').trigger('submit')
    await flushPromises()
    const input = api.createTrip.mock.calls[0][0]
    expect(input.points).toEqual(['Airport 1', 'Home'])
    expect(input.end).toBeUndefined()
    expect(input.destination).toBeUndefined()
    w.unmount()
  })

  it('keeps recurring dates to a destination', async () => {
    const w = open(trip({ key: 'recurring:1:2026-11-10', source: 'recurring', sourceLabel: 'School run' }))
    expect(w.find('[data-testid=route-points]').exists()).toBe(false)
    expect(w.get('[data-testid=route-fixed]').text()).toContain('Home → School → Home')
    await w.get('form').trigger('submit')
    await flushPromises()
    const input = api.updateTrip.mock.calls[0][1]
    expect(input.destination).toBe('School')
    expect(input.points).toBeUndefined()
    w.unmount()
  })
})
