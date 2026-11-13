import { describe, expect, it } from 'vitest'
import type { Block, DayAdvice, Rate, TimelinePoint } from '../api/types'
import {
  awaySpans,
  chartSummary,
  dayRange,
  priceAt,
  priceSpans,
  priceStats,
  readout,
  socAt,
  solarAt,
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

const day = (date: string, price?: DayAdvice['price']) => ({ date, price }) as DayAdvice
const advice = [
  day('2026-11-10', { value: 0.2, basis: 'published', detail: '', stale: false }),
  day('2026-11-11', { value: 0.25, basis: 'weekly', detail: 'weekly average', stale: false }),
  day('2026-11-12', { value: 0.3, basis: 'estimate', detail: 'Tibber outlook', stale: false }),
]
const dates = ['2026-11-09', '2026-11-10', '2026-11-11', '2026-11-12', '2026-11-13']

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

  it('clips away periods to the chart', () => {
    expect(awaySpans([block], t('2026-11-10T07:00:00Z'), t('2026-11-11T00:00:00Z'))).toEqual([
      { start: t('2026-11-10T07:00:00Z'), end: t('2026-11-10T07:20:00Z'), label: 'School drop-off' },
    ])
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
        'Published prices from €0.17/kWh to €0.38/kWh until Wed 11 00:00. ' +
        'Expected charging from Tue 10 00:00. Later days show estimated day prices. No price data for Fri 13.',
    )
  })
})
