// The Overview chart's data: values at a moment and the spans it shades. The
// chart shows SoC above prices on one time axis; everything here is pure so
// the readout and the screen-reader summary can be tested without a canvas.
import type { Block, DayAdvice, PriceBasis, Rate, SolarPoint, TimelinePoint } from '../api/types'
import { basisNames } from './advice'
import { clock, dayShort, localDate, pct, price, zonedToISO } from './format'

const ms = (v: string) => Date.parse(v)

export interface Span {
  start: number
  end: number
}

/** Linear interpolation over points sorted by time; null outside them. */
function interpolate<T>(points: T[], t: number, at: (p: T) => number, value: (p: T) => number) {
  const first = points[0]
  const last = points[points.length - 1]
  if (!first || !last || t < at(first) || t > at(last)) return null
  for (let i = 0; i + 1 < points.length; i++) {
    const a = points[i]!
    const b = points[i + 1]!
    const [ta, tb] = [at(a), at(b)]
    if (t >= ta && t <= tb)
      return tb === ta ? value(b) : value(a) + ((t - ta) / (tb - ta)) * (value(b) - value(a))
  }
  return value(last)
}

/** Projected SoC at t, on the line the chart draws. */
export function socAt(timeline: TimelinePoint[], t: number) {
  return interpolate(
    timeline,
    t,
    (p) => ms(p.at),
    (p) => p.soc,
  )
}

/** Forecast solar power (kW) at t; null outside the forecast. */
export function solarAt(points: SolarPoint[], t: number) {
  return interpolate(
    points,
    t,
    (p) => ms(p.time),
    (p) => p.kw,
  )
}

export interface PriceAt {
  value: number
  basis: PriceBasis
}

/**
 * The price at t: the published slot, else (after the last published slot)
 * the day's estimate or weekly average from the advice. Gaps between
 * published slots stay unknown.
 */
export function priceAt(slots: Rate[], advice: DayAdvice[], t: number): PriceAt | null {
  const slot = slots.find((r) => ms(r.start) <= t && t < ms(r.end))
  if (slot) return { value: slot.value, basis: 'published' }
  const last = slots[slots.length - 1]
  if (last && t < ms(last.end)) return null
  const p = advice.find((a) => a.date === localDate(t))?.price
  return p && p.basis !== 'published' ? { value: p.value, basis: p.basis } : null
}

/** The departure away from home at t (between leaving and the return). */
export function awayAt(blocks: Block[], t: number) {
  return blocks.find((b) => ms(b.departAt) <= t && t < ms(b.returnAt)) ?? null
}

export interface AwaySpan extends Span {
  label: string
}

/** Periods away from home within [from, to], named after their trips. */
export function awaySpans(blocks: Block[], from: number, to: number): AwaySpan[] {
  return blocks
    .map((b) => ({ start: ms(b.departAt), end: ms(b.returnAt), label: b.titles.join(', ') || b.firstTitle }))
    .filter((s) => s.end > from && s.start < to)
    .map((s) => ({ ...s, start: Math.max(s.start, from), end: Math.min(s.end, to) }))
}

function nextDate(date: string) {
  const [y, m, d] = date.split('-').map(Number)
  return new Date(Date.UTC(y!, m! - 1, d! + 1)).toISOString().slice(0, 10)
}

/** A local day as an interval of instants (DST-safe). */
export function dayRange(date: string): Span {
  return { start: ms(zonedToISO(date, '00:00')), end: ms(zonedToISO(nextDate(date), '00:00')) }
}

export interface EstimateSpan extends Span {
  value: number
  basis: 'estimate' | 'weekly'
}

/**
 * After the published slots: each day's estimate or weekly average, and the
 * days without any price.
 */
export function priceSpans(slots: Rate[], dates: string[], advice: DayAdvice[], from: number, to: number) {
  const last = slots[slots.length - 1]
  const after = Math.max(from, last ? ms(last.end) : from)
  const estimates: EstimateSpan[] = []
  const missing: Span[] = []
  for (const date of dates) {
    const day = dayRange(date)
    const span = { start: Math.max(day.start, after), end: Math.min(day.end, to) }
    if (span.end <= span.start) continue
    const p = advice.find((a) => a.date === date)?.price
    if (p && p.basis !== 'published') estimates.push({ ...span, value: p.value, basis: p.basis })
    else missing.push(span)
  }
  return { estimates, missing }
}

/** The time-weighted average and the lowest and highest published slot. */
export function priceStats(slots: Rate[]) {
  const first = slots[0]
  if (!first) return null
  let [min, max, sum, hours] = [first, first, 0, 0]
  for (const r of slots) {
    const h = (ms(r.end) - ms(r.start)) / 3_600_000
    sum += r.value * h
    hours += h
    if (r.value < min.value) min = r
    if (r.value > max.value) max = r
  }
  return { average: hours ? sum / hours : first.value, min, max }
}

export interface ChartData {
  timeline: TimelinePoint[]
  blocks: Block[]
  slots: Rate[]
  advice: DayAdvice[]
  solar: SolarPoint[]
  /** The planner's estimate of the charging slots. */
  charging: Rate[]
}

const basisWord = (b: PriceBasis) => (b === 'published' ? 'published' : basisNames[b].toLowerCase())

/** One line for the moment under the pointer or finger, or now. */
export function readout(d: ChartData, t: number, isNow = false) {
  const parts = [isNow ? 'Now' : `${dayShort(localDate(t))}, ${clock(t)}`]
  const soc = socAt(d.timeline, t)
  if (soc != null) parts.push(`SoC ${pct(soc)}`)
  const away = awayAt(d.blocks, t)
  if (away) parts.push(`away: ${away.titles.join(', ') || away.firstTitle}`)
  const p = priceAt(d.slots, d.advice, t)
  parts.push(p ? `${price(p.value)} ${basisWord(p.basis)}` : 'no price data')
  if (d.charging.some((r) => ms(r.start) <= t && t < ms(r.end))) parts.push('expected charging')
  const sun = solarAt(d.solar, t)
  if (sun != null && sun > 0) parts.push(`solar ${sun.toFixed(1)} kW`)
  return parts.join(' · ')
}

const when = (t: number) => `${dayShort(localDate(t))} ${clock(t)}`

/** What the chart shows, for screen readers. */
export function chartSummary(d: ChartData, dates: string[], from: number, to: number) {
  const out: string[] = []
  const points = d.timeline.filter((p) => ms(p.at) >= from)
  const low = points.reduce<TimelinePoint | null>((m, p) => (!m || p.soc < m.soc ? p : m), null)
  if (points[0] && low) {
    out.push(`Projected SoC ${pct(points[0].soc)} now, lowest ${pct(low.soc)} at ${when(ms(low.at))}.`)
  }
  const needs = d.blocks.filter((b) => ms(b.departAt) >= from).map((b) => b.requiredSoc)
  if (needs.length) out.push(`Departures need up to ${pct(Math.max(...needs))}.`)
  const last = d.slots[d.slots.length - 1]
  if (last) {
    const values = d.slots.map((r) => r.value)
    out.push(
      `Published prices from ${price(Math.min(...values))} to ${price(Math.max(...values))} until ${when(ms(last.end))}.`,
    )
  }
  const first = d.charging[0]
  if (first) out.push(`Expected charging from ${when(ms(first.start))}.`)
  const { estimates, missing } = priceSpans(d.slots, dates, d.advice, from, to)
  if (estimates.length) out.push('Later days show estimated day prices.')
  if (missing.length) {
    const days = [...new Set(missing.map((s) => dayShort(localDate(s.start))))]
    out.push(`No price data for ${days.join(', ')}.`)
  }
  return out.join(' ')
}
