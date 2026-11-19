import type { AdviceSource, Day, DayAdvice, DayPrice, Indication, Target } from '../api/types'
import { clock, localDate } from './format'
import type { Tone } from './status'

export const confidenceNames = ['', 'Low', 'Medium', 'High'] as const

/** "3/3 — High" */
export function confidenceLabel(c: number) {
  return c ? `${c}/3 — ${confidenceNames[c]}` : '—'
}

/** How strongly the coming days look cheaper. */
export const indicationNames: Record<Indication, string> = {
  little: 'Little',
  medium: 'Medium',
  high: 'High',
}

export const basisNames: Record<DayPrice['basis'], string> = {
  published: 'Published',
  estimate: 'Estimate',
  weekly: 'Weekly average',
}

export type AdviceIcon = 'bolt' | 'clock' | 'check' | 'dash' | 'sun' | 'arrow-down' | 'tag'

interface Label {
  text: string
  short: string
  tone: Tone
  icon: AdviceIcon
}

/**
 * The days the trip plan charges later trips ahead: those of its expected
 * charging, else its ready-by day. Empty when it charges ahead for nothing
 * (or pass no target, e.g. while a chosen plan pauses it).
 */
export function aheadDays(target?: Target | null): Set<string> {
  const days = new Set<string>()
  if (!target || target.covered || !target.ahead.length) return days
  for (const r of target.charging) days.add(localDate(r.start))
  if (!days.size) days.add(localDate(target.readyBy))
  return days
}

/**
 * The day's advice as a label (and a short form for the grid). Without
 * charge extra or defer, a day the trip plan charges later trips ahead reads
 * "Charge ahead to 80%"; otherwise "none" reads as keep or no plan,
 * depending on whether the day's trips need charging.
 */
export function adviceLabel(a: DayAdvice, day?: Day, target?: Target | null): Label {
  if (!a.noSuggestion) {
    switch (a.recommendation) {
      case 'charge_extra':
        if (a.solar) return { text: 'Charge extra from solar', short: 'Solar', tone: 'ok', icon: 'sun' }
        return {
          text: a.targetSoc ? `Charge extra to ${a.targetSoc}%` : 'Charge extra',
          short: 'Charge extra',
          tone: a.confidence >= 2 ? 'ok' : 'muted',
          icon: 'bolt',
        }
      case 'defer':
        return {
          text: 'Defer optional charging',
          short: 'Defer',
          tone: a.confidence >= 2 ? 'info' : 'muted',
          icon: 'clock',
        }
    }
  }
  // Charge extra is the advice's; charging ahead is the trip plan's.
  if (target && aheadDays(target).has(a.date))
    return { text: `Charge ahead to ${target.soc}%`, short: 'Charge ahead', tone: 'ok', icon: 'bolt' }
  // Today's trips may all be done while its advice still applies.
  if (day?.status === 'completed')
    return { text: 'Completed', short: 'Completed', tone: 'muted', icon: 'check' }
  // Days without published prices show what the outlook expects.
  if (a.outlook === 'cheaper')
    return { text: 'Looks cheaper (not published yet)', short: 'Cheaper?', tone: 'muted', icon: 'arrow-down' }
  if (a.outlook === 'dearer')
    return { text: 'Looks dearer (not published yet)', short: 'Dearer?', tone: 'muted', icon: 'tag' }
  if (a.outlook === 'similar')
    return { text: 'Looks similar (not published yet)', short: 'Similar?', tone: 'muted', icon: 'dash' }
  if (a.noSuggestion)
    return { text: 'No data / no suggestion', short: 'No data', tone: 'muted', icon: 'dash' }
  const needsCharge = day && (day.chargePct > 0 || ['shortfall', 'unknown', 'exceeds'].includes(day.status))
  return needsCharge
    ? { text: 'Keep plan', short: 'Keep plan', tone: 'muted', icon: 'check' }
    : { text: 'No plan needed', short: 'No plan', tone: 'muted', icon: 'check' }
}

/** "€0.16 (13:30)": the day's lowest published slot and when it starts. */
export function lowestText(p: DayPrice) {
  return `€${(p.min ?? p.value).toFixed(2)}${p.minAt ? ` (${clock(p.minAt)})` : ''}`
}

/**
 * The day's price as a short badge: the lowest slot for published days
 * ("€0.14", shown with a down arrow), else the day level ("~€0.30" for
 * estimates).
 */
export function priceBadge(
  a: DayAdvice,
): { text: string; basis: DayPrice['basis']; title: string; lowest: boolean } | null {
  const p = a.price
  if (!p) return null
  if (p.basis === 'published' && p.min != null) {
    return {
      text: `€${p.min.toFixed(2)}`,
      lowest: true,
      basis: p.basis,
      title: `Lowest published price €${p.min.toFixed(3)}/kWh${p.minAt ? ` at ${clock(p.minAt)}` : ''} (average €${p.value.toFixed(3)})`,
    }
  }
  const value = `€${p.value.toFixed(2)}`
  return {
    text: p.basis === 'published' ? value : `~${value}`,
    lowest: false,
    basis: p.basis,
    title: `${basisNames[p.basis]}: ${p.detail}`,
  }
}

/**
 * Days whose low prices the planner charges at, with why: charge extra
 * from the grid charges then (on the advice's day, unless its lowest price
 * is outside the cheap hours, and on a later day the hours run into, such
 * as the AI's cheap night), or the trip plan charges later trips ahead in
 * its hours (its expected charging). The week highlights their lowest
 * price.
 */
export function goodPrices(advice: DayAdvice[], target: Target | null): Map<string, string> {
  const why = new Map<string, string[]>()
  const add = (date: string, text: string) => {
    const list = why.get(date) ?? []
    if (!list.includes(text)) why.set(date, [...list, text])
  }
  // Whether a day's lowest price falls in the cheap hours; undefined when
  // either isn't known.
  const inHours = (d: DayAdvice, a: DayAdvice) => {
    if (!d.price?.minAt || !a.blocks?.length) return undefined
    const at = Date.parse(d.price.minAt)
    return a.blocks.some((b) => at >= Date.parse(b.from) && at < Date.parse(b.until))
  }
  for (const a of advice) {
    if (a.recommendation !== 'charge_extra' || a.solar || a.noSuggestion || !a.targetSoc) continue
    for (const d of advice) {
      const inside = inHours(d, a)
      if (d.date === a.date ? inside !== false : inside)
        add(d.date, `cheap enough to charge extra to ${a.targetSoc}%`)
    }
  }
  if (target && !target.covered && target.ahead.length) {
    const saving = target.savingEur > 0 ? ` (saves about €${target.savingEur.toFixed(2)})` : ''
    for (const r of target.charging)
      add(localDate(r.start), `the trip plan charges later trips ahead${saving}`)
  }
  return new Map([...why].map(([date, list]) => [date, `Good price: ${list.join('; ')}`]))
}

/**
 * The day's sun: "8.2 kWh · 6.5 kWh while home" from EVCC's local solar
 * forecast (today and tomorrow), else "30% of possible sunshine" from KNMI.
 */
export function sunText(a: DayAdvice) {
  if (a.sunSource === 'evcc' && a.solarKWh != null) {
    const home = a.solarHomeKWh != null ? ` · ${a.solarHomeKWh.toFixed(1)} kWh while home` : ''
    return `${a.solarKWh.toFixed(1)} kWh${home}`
  }
  if (a.sunSource === 'knmi' && a.weather?.sunshinePct != null)
    return `${a.weather.sunshinePct}% of possible sunshine`
  return ''
}

export const sunSourceNames = {
  evcc: 'EVCC solar forecast, local',
  knmi: 'KNMI outlook, national',
  '': '',
} as const

/** One fact about a day for the advice card: a main value and a detail line. */
export interface Fact {
  value: string
  detail: string
  /** The value is the day's lowest price (shown with a down arrow). */
  lowest?: boolean
}

/**
 * Published days lead with the lowest slot, "€0.16 (13:30)", over "€0.26
 * avg"; estimates have only the day level, "€0.30 avg".
 */
export function priceFact(a: DayAdvice): Fact | null {
  const p = a.price
  if (!p) return null
  const avg = `€${p.value.toFixed(2)} avg`
  return p.min != null ? { value: lowestText(p), detail: avg, lowest: true } : { value: avg, detail: '' }
}

/** "7.0 kWh", "6.0 kWh while home" from EVCC; "40%", "of possible sunshine" from KNMI. */
export function sunFact(a: DayAdvice): Fact | null {
  if (a.sunSource === 'evcc' && a.solarKWh != null) {
    const home = a.solarHomeKWh != null ? `${a.solarHomeKWh.toFixed(1)} kWh while home` : ''
    return { value: `${a.solarKWh.toFixed(1)} kWh`, detail: home }
  }
  if (a.sunSource === 'knmi' && a.weather?.sunshinePct != null)
    return { value: `${a.weather.sunshinePct}%`, detail: 'of possible sunshine' }
  return null
}

/**
 * Windy (Bft 5 or more) and sunny (more than 50% of possible sunshine) days
 * usually have cheaper prices; their badges are highlighted.
 */
export const windy = (bft: number | null | undefined) => bft != null && bft >= 5
export const sunny = (pct: number | null | undefined) => pct != null && pct > 50
export const cheapHint = 'usually cheaper prices'

/** "Bft 5 W", "market high (6.5 GW)". */
export function windFact(a: DayAdvice): Fact | null {
  const [value, ...rest] = windText(a).split(' · ')
  return value ? { value, detail: rest.join(' · ') } : null
}

/** "Bft 5 W · market high (6.5 GW)" from KNMI and the price outlook. */
export function windText(a: DayAdvice) {
  const parts: string[] = []
  const w = a.weather
  if (w?.windBft != null) parts.push(`Bft ${w.windBft}${w.windDir ? ' ' + w.windDir : ''}`)
  if (a.marketWind && a.marketWind !== 'unknown') {
    parts.push(`market ${a.marketWind}${a.marketWindGW != null ? ` (${a.marketWindGW.toFixed(1)} GW)` : ''}`)
  }
  return parts.join(' · ')
}

/** "5–10 °C" */
export function tempText(a: DayAdvice) {
  const w = a.weather
  return w?.tempMinLowC != null && w.tempMaxHighC != null ? `${w.tempMinLowC}–${w.tempMaxHighC} °C` : ''
}

export const sourceTone: Record<AdviceSource['status'], Tone> = {
  ok: 'ok',
  stale: 'warn',
  missing: 'warn',
  error: 'bad',
  stub: 'warn',
}

/** "Tomorrow's lowest €0.20", "Thu's expected lowest €0.29", or "Past 7 days' lowest €0.30": what charge extra compared with. */
export function referenceText(a: DayAdvice) {
  const r = a.reference
  if (!r) return ''
  const v = `€${r.value.toFixed(2)}`
  switch (r.basis) {
    case 'published':
      return `Tomorrow's lowest ${v}`
    case 'outlook':
      return `${r.day ? r.day.slice(0, 3) + "'s" : "Tomorrow's"} expected lowest ${v}`
  }
  return `Past ${r.days ?? 7} days' lowest ${v}`
}
