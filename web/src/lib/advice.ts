import type { AdviceSource, Day, DayAdvice, DayPrice, Indicator } from '../api/types'
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

export type AdviceIcon = 'bolt' | 'clock' | 'check' | 'dash'

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
