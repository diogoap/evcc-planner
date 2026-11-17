import { describe, expect, it } from 'vitest'
import type { DayStats } from '../api/types'
import {
  addDays,
  addMonths,
  dailySeries,
  dateTicks,
  dayLine,
  periodFrom,
  summarize,
  summaryText,
  tickLabel,
} from './history'

const day = (date: string, s: Partial<DayStats> = {}): DayStats => ({
  date,
  priceMin: 0.18,
  priceAvg: 0.25,
  priceMax: 0.39,
  priceHours: 24,
  distanceKm: 24,
  energyKWh: 4.3,
  windBft: 3,
  sunshinePct: 20,
  updatedAt: `${date}T22:50:00Z`,
  ...s,
})

describe('price history', () => {
  it('counts calendar days and months', () => {
    expect(addDays('2026-11-09', 1)).toBe('2026-11-10')
    expect(addDays('2026-03-29', 1)).toBe('2026-03-30') // clocks change; dates don't care
    expect(addDays('2026-01-01', -1)).toBe('2025-12-31')
    expect(addMonths('2026-11-09', -1)).toBe('2026-10-09')
    expect(addMonths('2026-03-31', -1)).toBe('2026-02-28')
    expect(addMonths('2026-02-15', -12)).toBe('2025-02-15')
  })

  it('starts each period so that it ends today', () => {
    expect(periodFrom('week', '2026-11-09')).toBe('2026-11-03')
    expect(periodFrom('month', '2026-11-09')).toBe('2026-10-10')
    expect(periodFrom('quarter', '2026-11-09')).toBe('2026-08-10')
    expect(periodFrom('year', '2026-11-09')).toBe('2025-11-10')
    expect(periodFrom('all', '2026-11-09')).toBeUndefined()
  })

  it('lists every day from the first saved to the last, with gaps for missing ones', () => {
    const s = dailySeries([day('2026-11-07'), day('2026-11-09'), day('2026-11-10')])
    expect(s.map((d) => d.date)).toEqual(['2026-11-07', '2026-11-08', '2026-11-09', '2026-11-10'])
    expect(s[1]!.day).toBeNull()
    expect(s[2]!.day?.date).toBe('2026-11-09')
    expect(dailySeries([])).toEqual([])
  })

  it('labels the axis more sparsely as the period grows', () => {
    const dates = (from: string, n: number) => Array.from({ length: n }, (_, i) => addDays(from, i))
    expect(dateTicks(dates('2026-11-03', 8))).toHaveLength(8)
    // A month: Mondays (2, 9, 16, 23, 30 November).
    const month = dates('2026-11-01', 30)
    expect(dateTicks(month).map((i) => month[i])).toEqual([
      '2026-11-02',
      '2026-11-09',
      '2026-11-16',
      '2026-11-23',
      '2026-11-30',
    ])
    const quarter = dates('2026-08-10', 92)
    expect(dateTicks(quarter).map((i) => quarter[i])).toEqual([
      '2026-08-15',
      '2026-09-01',
      '2026-09-15',
      '2026-10-01',
      '2026-10-15',
      '2026-11-01',
    ])
    const year = dates('2025-11-10', 365)
    expect(dateTicks(year)).toHaveLength(12)
    expect(tickLabel('2026-11-09', 30)).toBe('9 Nov')
    expect(tickLabel('2026-11-01', 365)).toBe('Nov')
    expect(tickLabel('2027-01-01', 365)).toBe('Jan 2027')
  })

  it('summarizes the period', () => {
    const s = summarize([
      day('2026-11-07', { priceMin: 0.15, priceAvg: 0.22 }),
      day('2026-11-08', { priceMin: null, priceAvg: null, priceMax: null, priceHours: 0 }),
      day('2026-11-09', { priceAvg: 0.28, priceMax: 0.45 }),
    ])
    expect(s).toEqual({
      average: 0.25,
      low: { value: 0.15, date: '2026-11-07' },
      high: { value: 0.45, date: '2026-11-09' },
      days: 2,
    })
    expect(summaryText(s!)).toBe(
      'Average €0.25/kWh · lowest €0.15/kWh (Sat 7 Nov) · highest €0.45/kWh (Mon 9 Nov) · 2 days',
    )
    expect(summarize([day('2026-11-08', { priceMin: null, priceAvg: null, priceMax: null })])).toBeNull()
  })

  it('reads out a day', () => {
    const today = '2026-11-09'
    expect(dayLine(day(today), today, today)).toBe(
      'Today · average €0.25/kWh · €0.18–€0.39 · wind Bft 3 · sun 20% · 24 km, 4.3 kWh',
    )
    expect(
      dayLine(
        day('2026-11-08', { windBft: null, sunshinePct: null, distanceKm: null, energyKWh: null }),
        '2026-11-08',
        today,
      ),
    ).toBe('Sun 8 Nov · average €0.25/kWh · €0.18–€0.39')
    expect(dayLine(day('2026-11-07', { priceHours: 9.5, energyKWh: null }), '2026-11-07', today)).toBe(
      'Sat 7 Nov · average €0.25/kWh · €0.18–€0.39 · prices for 10 h · wind Bft 3 · sun 20% · 24 km',
    )
    expect(
      dayLine(
        day('2026-11-06', { priceMin: null, priceAvg: null, priceMax: null, priceHours: 0 }),
        '2026-11-06',
        today,
      ),
    ).toBe('Fri 6 Nov · no prices · wind Bft 3 · sun 20% · 24 km, 4.3 kWh')
    expect(dayLine(null, '2026-11-05', today)).toBe('Thu 5 Nov · not recorded')
  })
})
