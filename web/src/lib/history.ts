import type { DayStats } from '../api/types'
import { dayMonth, price, shortDate } from './format'

// The price history: one saved row per day (prices, driving, wind, sun),
// shown over a chosen period.

export type Period = 'week' | 'month' | 'quarter' | 'year' | 'all'

export const periods: { key: Period; label: string }[] = [
  { key: 'week', label: '1 week' },
  { key: 'month', label: '1 month' },
  { key: 'quarter', label: '3 months' },
  { key: 'year', label: '1 year' },
  { key: 'all', label: 'All' },
]

/** The series the chart can show. */
export type HistorySeries = 'range' | 'average' | 'wind' | 'sun'

const dayMs = 86_400_000
const toMs = (date: string) => Date.parse(`${date}T00:00:00Z`)
const fromMs = (ms: number) => new Date(ms).toISOString().slice(0, 10)

/** A YYYY-MM-DD date n calendar days later (or earlier). */
export function addDays(date: string, n: number) {
  return fromMs(toMs(date) + n * dayMs)
}

/** A YYYY-MM-DD date n months later (or earlier), on the same day or the month's last. */
export function addMonths(date: string, n: number) {
  const [y, m, d] = date.split('-').map(Number)
  const first = new Date(Date.UTC(y!, m! - 1 + n, 1))
  const last = new Date(Date.UTC(first.getUTCFullYear(), first.getUTCMonth() + 1, 0)).getUTCDate()
  first.setUTCDate(Math.min(d!, last))
  return fromMs(first.getTime())
}

/** The first day a period shows, ending today; undefined for all days. */
export function periodFrom(period: Period, today: string) {
  switch (period) {
    case 'week':
      return addDays(today, -6)
    case 'month':
      return addDays(addMonths(today, -1), 1)
    case 'quarter':
      return addDays(addMonths(today, -3), 1)
    case 'year':
      return addDays(addMonths(today, -12), 1)
    default:
      return undefined
  }
}

/** Every date from the first saved day to the last, with its figures (null when not saved). */
export function dailySeries(days: DayStats[]) {
  const first = days[0]
  const last = days[days.length - 1]
  if (!first || !last) return []
  const byDate = new Map(days.map((d) => [d.date, d]))
  const out: { date: string; day: DayStats | null }[] = []
  for (let d = first.date; d <= last.date; d = addDays(d, 1))
    out.push({ date: d, day: byDate.get(d) ?? null })
  return out
}

/**
 * The indexes of the dates that get an axis label: every day for two
 * weeks, Mondays up to two months, the 1st and 15th up to half a year, then
 * each month (each quarter beyond two years).
 */
export function dateTicks(dates: string[]) {
  const n = dates.length
  const keep = (date: string) => {
    const dom = Number(date.slice(8))
    if (n <= 14) return true
    if (n <= 62) return new Date(toMs(date)).getUTCDay() === 1
    if (n <= 190) return dom === 1 || dom === 15
    if (n <= 750) return dom === 1
    return dom === 1 && Number(date.slice(5, 7)) % 3 === 1
  }
  return dates.flatMap((d, i) => (keep(d) ? [i] : []))
}

/** An axis label: 9 Nov for days, Nov for months (Jan 2027 in January). */
export function tickLabel(date: string, span: number) {
  if (span <= 190) return shortDate(date)
  return shortDate(date, { month: true, year: date.slice(5, 7) === '01' })
}

export interface HistorySummary {
  average: number
  low: { value: number; date: string }
  high: { value: number; date: string }
  days: number
}

/** The average of the daily averages, and the lowest and highest price, over the days given. */
export function summarize(days: DayStats[]): HistorySummary | null {
  let sum = 0
  let count = 0
  let low: HistorySummary['low'] | undefined
  let high: HistorySummary['high'] | undefined
  for (const d of days) {
    if (d.priceAvg != null) {
      sum += d.priceAvg
      count++
    }
    if (d.priceMin != null && (!low || d.priceMin < low.value)) low = { value: d.priceMin, date: d.date }
    if (d.priceMax != null && (!high || d.priceMax > high.value)) high = { value: d.priceMax, date: d.date }
  }
  if (!count || !low || !high) return null
  return { average: Math.round((sum / count) * 10000) / 10000, low, high, days: count }
}

export function summaryText(s: HistorySummary) {
  const days = s.days === 1 ? '1 day' : `${s.days} days`
  return `Average ${price(s.average)} · lowest ${price(s.low.value)} (${dayMonth(s.low.date)}) · highest ${price(s.high.value)} (${dayMonth(s.high.date)}) · ${days}`
}

const euro = (v: number) => `€${v.toFixed(2)}`

/** The readout for one day: Today · average €0.25/kWh · €0.18–€0.39 · wind Bft 3 · sun 20% */
export function dayLine(day: DayStats | null, date: string, today: string) {
  const when = date === today ? 'Today' : dayMonth(date)
  if (!day) return `${when} · not recorded`
  const partial = day.priceHours > 0 && day.priceHours < 23
  return [
    when,
    day.priceAvg != null ? `average ${price(day.priceAvg)}` : 'no prices',
    day.priceMin != null && day.priceMax != null && `${euro(day.priceMin)}–${euro(day.priceMax)}`,
    partial && `prices for ${Math.round(day.priceHours)} h`,
    day.windBft != null && `wind Bft ${day.windBft}`,
    day.sunshinePct != null && `sun ${day.sunshinePct}%`,
  ]
    .filter(Boolean)
    .join(' · ')
}
