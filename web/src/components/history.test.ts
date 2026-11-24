import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { DayStats, History } from '../api/types'

const api = vi.hoisted(() => ({ history: vi.fn() }))
vi.mock('../api/client', () => ({ api }))
vi.mock('./HistoryChart.vue', () => ({
  default: {
    props: ['days', 'today', 'show'],
    template: `<div data-testid="history-chart">{{ days.length }} {{ today }} {{ Object.keys(show).filter((k) => show[k]).join(',') }}</div>`,
  },
}))

const { historyChoice } = await import('../layout')
const { default: PriceHistoryCard } = await import('./PriceHistoryCard.vue')

const now = '2026-11-09T18:00:00Z' // Monday 19:00 in Amsterdam
const day = (date: string): DayStats => ({
  date,
  priceMin: 0.18,
  priceAvg: 0.25,
  priceMax: 0.39,
  priceHours: 24,
  distanceKm: 24,
  energyKWh: 4.3,
  windBft: 3,
  sunshinePct: 20,
  solarKWh: 6.2,
  updatedAt: `${date}T18:00:00Z`,
})
const history = (days: DayStats[]): History => ({ today: '2026-11-09', first: days[0]?.date, days })

describe('PriceHistoryCard', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    localStorage.clear()
    historyChoice.period = 'month'
    Object.assign(historyChoice.show, {
      range: true,
      average: true,
      wind: false,
      sun: false,
      driving: false,
      energy: false,
      solar: false,
    })
  })

  it('shows the last month by default, and the period chosen', async () => {
    api.history.mockResolvedValue(history([day('2026-11-08'), day('2026-11-09')]))
    const w = mount(PriceHistoryCard, { props: { now } })
    await flushPromises()
    expect(api.history).toHaveBeenCalledWith('2026-10-10')
    expect(w.get('[data-testid=history-chart]').text()).toBe('2 2026-11-09 range,average')
    expect(w.text()).toContain('saved since Sun 8 Nov')

    await w.get('[data-testid=history-period]').setValue('week')
    await flushPromises()
    expect(api.history).toHaveBeenLastCalledWith('2026-11-03')
    await w.get('[data-testid=history-period]').setValue('all')
    await flushPromises()
    expect(api.history).toHaveBeenLastCalledWith(undefined)
    await flushPromises()
    expect(JSON.parse(localStorage.getItem('evcc-planner.history')!).period).toBe('all')
  })

  it('shows wind and sun on request, and remembers it', async () => {
    api.history.mockResolvedValue(history([day('2026-11-09')]))
    const w = mount(PriceHistoryCard, { props: { now } })
    await flushPromises()
    const wind = w.get('[data-testid=history-legend-wind]')
    expect(wind.attributes('aria-pressed')).toBe('false')
    await wind.trigger('click')
    await w.get('[data-testid=history-legend-range]').trigger('click')
    expect(wind.attributes('aria-pressed')).toBe('true')
    expect(w.get('[data-testid=history-chart]').text()).toBe('1 2026-11-09 average,wind')
    await flushPromises()
    expect(JSON.parse(localStorage.getItem('evcc-planner.history')!).show).toEqual({
      range: false,
      average: true,
      wind: true,
      sun: false,
      driving: false,
      energy: false,
      solar: false,
    })
  })

  it('shows driving and energy on request (off by default)', async () => {
    api.history.mockResolvedValue(history([day('2026-11-09')]))
    const w = mount(PriceHistoryCard, { props: { now } })
    await flushPromises()
    const [driving, energy] = [
      w.get('[data-testid=history-legend-driving]'),
      w.get('[data-testid=history-legend-energy]'),
    ]
    expect(driving.text()).toBe('Driving')
    expect(energy.text()).toBe('Energy')
    expect(driving.attributes('aria-pressed')).toBe('false')
    expect(energy.attributes('aria-pressed')).toBe('false')
    await driving.trigger('click')
    await energy.trigger('click')
    expect(w.get('[data-testid=history-chart]').text()).toBe('1 2026-11-09 range,average,driving,energy')
    expect(w.text()).toContain('driving (km) and energy (kWh the trips need) from the day')
  })

  it('shows the solar forecast on request (off by default)', async () => {
    api.history.mockResolvedValue(history([day('2026-11-09')]))
    const w = mount(PriceHistoryCard, { props: { now } })
    await flushPromises()
    const solar = w.get('[data-testid=history-legend-solar]')
    expect(solar.text()).toBe('Solar forecast')
    expect(solar.attributes('aria-pressed')).toBe('false')
    await solar.trigger('click')
    expect(w.get('[data-testid=history-chart]').text()).toBe('1 2026-11-09 range,average,solar')
    expect(w.text()).toContain('the solar forecast (kWh for the whole day, as it starts) from EVCC')
  })

  it('reloads every hour (the planner saves each day twice)', async () => {
    api.history.mockResolvedValue(history([day('2026-11-09')]))
    const w = mount(PriceHistoryCard, { props: { now } })
    await flushPromises()
    await w.setProps({ now: '2026-11-09T18:59:00Z' })
    await flushPromises()
    expect(api.history).toHaveBeenCalledTimes(1)
    await w.setProps({ now: '2026-11-09T19:00:00Z' })
    await flushPromises()
    expect(api.history).toHaveBeenCalledTimes(2)
  })

  it('says when nothing is recorded yet, and offers Retry after an error', async () => {
    api.history.mockResolvedValueOnce(history([]))
    const w = mount(PriceHistoryCard, { props: { now } })
    await flushPromises()
    expect(w.find('[data-testid=history-empty]').exists()).toBe(true)
    expect(w.find('[data-testid=history-chart]').exists()).toBe(false)

    api.history.mockRejectedValueOnce(new Error('The planner service is not reachable.'))
    const broken = mount(PriceHistoryCard, { props: { now } })
    await flushPromises()
    expect(broken.get('[data-testid=error-banner]').text()).toContain('not reachable')
    api.history.mockResolvedValueOnce(history([day('2026-11-09')]))
    await broken.get('[data-testid=error-banner] button').trigger('click')
    await flushPromises()
    expect(broken.find('[data-testid=error-banner]').exists()).toBe(false)
    expect(broken.find('[data-testid=history-chart]').exists()).toBe(true)
  })
})
