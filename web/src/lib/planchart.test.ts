import { describe, expect, it } from 'vitest'
import type { Block, Day, DayAdvice, Rate, TimelinePoint, WeatherDay } from '../api/types'
import {
  awaySpans,
  chargingWindows,
  chartSummary,
  dayRange,
  driveTitles,
  priceAt,
  priceSpans,
  priceStats,
  readout,
  socAt,
  solarAt,
  weatherTitles,
  type ChartData,
} from './planchart'

const t = (iso: string) => Date.parse(iso)

// Monday 9 November 2026, 19:00 in Amsterdam (18:00 UTC), as in the demo.
const timeline: TimelinePoint[] = [
  { at: '2026-11-09T18:00:00Z', soc: 38, kind: 'now' },
  { at: '2026-11-10T05:00:00Z', soc: 38, kind: 'charge_start' },
  { at: '2026-11-10T06:00:00Z', soc: 42, kind: 'charge_end' },
  { at: '2026-11-10T06:48:00Z', soc: 42, kind: 'depart' },
  { at: '2026-11-10T07:20:00Z', soc: 38, kind: 'return' },
]

const block = {
  departAt: '2026-11-10T06:48:00Z',
  returnAt: '2026-11-10T07:20:00Z',
  titles: ['School drop-off'],
  firstTitle: 'School drop-off',
  requiredSoc: 41.8,
} as Block

// Published prices up to Tuesday midnight (local).
const slots: Rate[] = [
  { start: '2026-11-09T18:00:00Z', end: '2026-11-09T19:00:00Z', value: 0.38 },
  { start: '2026-11-09T23:00:00Z', end: '2026-11-10T00:00:00Z', value: 0.17 },
  { start: '2026-11-10T22:00:00Z', end: '2026-11-10T23:00:00Z', value: 0.22 },
]

const day = (date: string, price?: DayAdvice['price'], weather?: Partial<WeatherDay>) =>
  ({ date, price, weather }) as DayAdvice
const advice = [
  day(
    '2026-11-10',
    { value: 0.2, basis: 'published', detail: '', stale: false },
    { sunshinePct: 40, windBft: 5 },
  ),
  day('2026-11-11', { value: 0.25, basis: 'weekly', detail: 'weekly average', stale: false }, { windBft: 3 }),
  day('2026-11-12', { value: 0.3, basis: 'estimate', detail: 'Tibber outlook', stale: false }),
]
const dates = ['2026-11-09', '2026-11-10', '2026-11-11', '2026-11-12', '2026-11-13']
const days = [
  { date: '2026-11-09', distanceKm: 12, energyKWh: 2.2 },
  { date: '2026-11-10', distanceKm: 100, energyKWh: 18 },
  { date: '2026-11-11', distanceKm: 0, energyKWh: 0 },
  { date: '2026-11-12', distanceKm: 6.5, energyKWh: 1.2 },
] as Day[]

const data: ChartData = {
  timeline,
  blocks: [block],
  slots,
  advice,
  solar: [
    { time: '2026-11-10T10:00:00Z', kw: 1 },
    { time: '2026-11-10T12:00:00Z', kw: 3 },
  ],
  charging: [{ start: '2026-11-09T23:00:00Z', end: '2026-11-09T23:45:00Z', value: 0.17 }],
  days,
}

describe('values at a moment', () => {
  it('reads SoC and solar on their lines, and nothing outside them', () => {
    expect(socAt(timeline, t('2026-11-10T05:30:00Z'))).toBe(40)
    expect(socAt(timeline, t('2026-11-09T12:00:00Z'))).toBeNull()
    expect(solarAt(data.solar, t('2026-11-10T11:00:00Z'))).toBe(2)
    expect(solarAt(data.solar, t('2026-11-10T13:00:00Z'))).toBeNull()
  })

  it('uses published slots, then each later day’s estimate, and leaves gaps unknown', () => {
    expect(priceAt(slots, advice, t('2026-11-09T18:30:00Z'))).toEqual({ value: 0.38, basis: 'published' })
    expect(priceAt(slots, advice, t('2026-11-09T21:00:00Z'))).toBeNull()
    expect(priceAt(slots, advice, t('2026-11-11T10:00:00Z'))).toEqual({ value: 0.25, basis: 'weekly' })
    expect(priceAt(slots, advice, t('2026-11-12T10:00:00Z'))).toEqual({ value: 0.3, basis: 'estimate' })
    expect(priceAt(slots, advice, t('2026-11-13T10:00:00Z'))).toBeNull()
  })
})

describe('spans', () => {
  it('measures local days across a daylight-saving change', () => {
    const autumn = dayRange('2026-10-25')
    expect((autumn.end - autumn.start) / 3_600_000).toBe(25)
    expect(dayRange('2026-11-10').start).toBe(t('2026-11-09T23:00:00Z'))
  })

  it('lists estimates after the published prices and the days without any', () => {
    const { estimates, missing } = priceSpans(
      slots,
      dates,
      advice,
      t('2026-11-09T18:00:00Z'),
      t('2026-11-14T23:00:00Z'),
    )
    expect(estimates).toEqual([
      { start: t('2026-11-10T23:00:00Z'), end: t('2026-11-11T23:00:00Z'), value: 0.25, basis: 'weekly' },
      { start: t('2026-11-11T23:00:00Z'), end: t('2026-11-12T23:00:00Z'), value: 0.3, basis: 'estimate' },
    ])
    expect(missing).toEqual([{ start: t('2026-11-12T23:00:00Z'), end: t('2026-11-13T23:00:00Z') }])
  })

  it('titles every day by its distance and energy, its day clipped to the chart', () => {
    const titles = driveTitles(days, t('2026-11-09T18:00:00Z'), t('2026-11-12T12:00:00Z'))
    expect(titles.map((s) => [s.start, s.end])).toEqual([
      [t('2026-11-09T18:00:00Z'), t('2026-11-09T23:00:00Z')],
      [t('2026-11-09T23:00:00Z'), t('2026-11-10T23:00:00Z')],
      [t('2026-11-10T23:00:00Z'), t('2026-11-11T23:00:00Z')],
      [t('2026-11-11T23:00:00Z'), t('2026-11-12T12:00:00Z')],
    ])
    expect(titles[1]!.items).toEqual([
      { icon: 'route', text: '100 km', short: '100', label: '100 km' },
      { icon: 'bolt', text: '18.0 kWh', short: '18.0', label: '18.0 kWh' },
    ])
    // A day without trips shows zeros.
    expect(titles[2]!.items.map((i) => `${i.text} (${i.short})`)).toEqual(['0 km (0)', '0.0 kWh (0.0)'])
    // Days before the chart are left out.
    expect(driveTitles(days, t('2026-11-10T23:00:00Z'), t('2026-11-12T23:00:00Z'))).toHaveLength(2)
  })

  it('titles each day by the sunshine and wind it has', () => {
    const titles = weatherTitles(advice, t('2026-11-09T18:00:00Z'), t('2026-11-14T23:00:00Z'))
    expect(titles.map((s) => s.items.map((i) => `${i.icon} ${i.text} (${i.short})`))).toEqual([
      ['sun 40% (40)', 'wind 5 Bft (5)'],
      ['wind 3 Bft (3)'],
    ])
  })

  it('clips away periods to the chart', () => {
    expect(awaySpans([block], t('2026-11-10T07:00:00Z'), t('2026-11-11T00:00:00Z'))).toEqual([
      { start: t('2026-11-10T07:00:00Z'), end: t('2026-11-10T07:20:00Z'), label: 'School drop-off' },
    ])
  })
})

describe('chargingWindows', () => {
  it('merges adjacent slots and averages their price', () => {
    const slot = (start: string, end: string, value: number) => ({ start, end, value })
    const windows = chargingWindows(
      [
        slot('2026-11-10T02:00:00Z', '2026-11-10T02:15:00Z', 0.16),
        slot('2026-11-10T02:15:00Z', '2026-11-10T02:30:00Z', 0.18),
        slot('2026-11-10T03:15:00Z', '2026-11-10T03:30:00Z', 0.15),
        slot('2026-11-11T02:00:00Z', '2026-11-11T02:15:00Z', 0.1),
      ],
      t('2026-11-09T23:00:00Z'),
      t('2026-11-10T23:00:00Z'),
    )
    expect(windows).toHaveLength(2)
    expect(windows[0]).toMatchObject({ start: t('2026-11-10T02:00:00Z'), end: t('2026-11-10T02:30:00Z') })
    expect(windows[0]!.price).toBeCloseTo(0.17)
    expect(windows[1]).toEqual({
      start: t('2026-11-10T03:15:00Z'),
      end: t('2026-11-10T03:30:00Z'),
      price: 0.15,
    })
  })
})

describe('priceStats', () => {
  it('weighs the average by slot length and finds the extremes', () => {
    const stats = priceStats([
      { start: '2026-11-09T18:00:00Z', end: '2026-11-09T20:00:00Z', value: 0.3 },
      { start: '2026-11-09T20:00:00Z', end: '2026-11-09T20:30:00Z', value: 0.1 },
      { start: '2026-11-09T20:30:00Z', end: '2026-11-09T21:00:00Z', value: 0.5 },
    ])
    expect(stats?.average).toBeCloseTo(0.3)
    expect(stats?.min.value).toBe(0.1)
    expect(stats?.max.value).toBe(0.5)
    expect(priceStats([])).toBeNull()
  })
})

describe('text', () => {
  it('describes a moment for the readout', () => {
    expect(readout(data, t('2026-11-09T18:00:00Z'), true)).toBe('Now · SoC 38% · €0.38/kWh published')
    expect(readout(data, t('2026-11-09T23:30:00Z'))).toBe(
      'Tue 10, 00:30 · SoC 38% · €0.17/kWh published · expected charging',
    )
    expect(readout(data, t('2026-11-10T07:00:00Z'))).toBe(
      'Tue 10, 08:00 · SoC 41% · away: School drop-off · no price data',
    )
    expect(readout(data, t('2026-11-12T10:00:00Z'))).toBe('Thu 12, 11:00 · €0.30/kWh estimate')
  })

  it('summarises the chart for screen readers', () => {
    expect(chartSummary(data, dates, t('2026-11-09T18:00:00Z'), t('2026-11-14T23:00:00Z'))).toBe(
      'Projected SoC 38% now, lowest 38% at Mon 09 19:00. Departures need up to 42%. ' +
        'Driving 12 km and 2.2 kWh on Mon 09, 100 km and 18.0 kWh on Tue 10, 0 km and 0.0 kWh on Wed 11, ' +
        '6.5 km and 1.2 kWh on Thu 12. ' +
        'Published prices from €0.17/kWh to €0.38/kWh until Wed 11 00:00. ' +
        'Expected charging from Tue 10 00:00. Later days show estimated day prices. No price data for Fri 13. ' +
        'KNMI outlook: sun 40% and wind 5 Bft on Tue 10, wind 3 Bft on Wed 11.',
    )
  })
})
