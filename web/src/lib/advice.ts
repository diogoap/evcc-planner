import type { AdviceSource, Day, DayAdvice, DayPrice, Indicator } from '../api/types'
import { clock } from './format'
import type { Tone } from './status'

export const confidenceNames = ['', 'Low', 'Medium', 'High'] as const

/** "3/3 — High" */
export function confidenceLabel(c: number) {
  return c ? `${c}/3 — ${confidenceNames[c]}` : '—'
}

export const indicatorNames: Record<Indicator, string> = {
  little: 'Little',
  medium: 'Medium',
  high: 'High (more)',
}

export const indicatorLevel: Record<Indicator, number> = { little: 1, medium: 2, high: 3 }

export const basisNames: Record<DayPrice['basis'], string> = {
  published: 'Published',
  estimate: 'Estimate',
  weekly: 'Weekly average',
}

export type AdviceIcon = 'bolt' | 'clock' | 'check' | 'dash' | 'sun'

interface Label {
  text: string
  short: string
  tone: Tone
  icon: AdviceIcon
}

/**
 * The day's advice as a label (and a short form for the grid). "None" reads
 * as keep or no plan, depending on whether the day's trips need charging.
 */
export function adviceLabel(a: DayAdvice, day?: Day): Label {
  if (!a.noSuggestion) {
    switch (a.recommendation) {
      case 'charge_ahead':
        if (a.solar) return { text: 'Charge ahead from solar', short: 'Solar', tone: 'ok', icon: 'sun' }
        return {
          text: 'Charge ahead',
          short: 'Charge ahead',
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
  // Today's trips may all be done while its advice still applies.
  if (day?.status === 'completed')
    return { text: 'Completed', short: 'Completed', tone: 'muted', icon: 'check' }
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
