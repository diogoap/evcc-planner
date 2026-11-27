import type { Block, HeatingRun, HeatingSummary } from '../api/types'
import { clock } from './format'

/** 2 °C, 2.6 °C: one decimal when it isn't whole, so a reading never shows as the threshold it is below. */
export function temp(v: number) {
  const r = Math.round(v * 10) / 10
  return `${r === 0 ? 0 : r} °C`
}

/** The temperature a decision used: "2 °C outside", or "a low of 5 °C forecast". */
export function outsideText(r: Pick<HeatingRun, 'outsideC' | 'outsideSource'>) {
  if (r.outsideC == null) return 'outside temperature unknown'
  return r.outsideSource === 'forecast'
    ? `a low of ${temp(r.outsideC)} forecast`
    : `${temp(r.outsideC)} outside`
}

export type HeatingTone = 'ok' | 'warn' | 'bad' | 'muted'

/** What a departure's decision was, in a few words. */
export function outcomeText(r: HeatingRun): string {
  switch (r.outcome) {
    case 'heated':
      return `On since ${clock(r.heatedAt ?? r.decidedAt)} (${outsideText(r)})`
    case 'confirmed':
      return `On since ${clock(r.heatedAt ?? r.decidedAt)}, after Heat now (${outsideText(r)})`
    case 'asked':
      return `Asked on the phone at ${clock(r.decidedAt)} (${outsideText(r)})`
    case 'no_answer':
      return 'Not on: no answer within 30 minutes'
    case 'warm':
      return `Not needed: ${outsideText(r)} (heats below ${temp(r.belowC)})`
    case 'skipped':
      return `Not on: ${r.reason ?? ''}`
    case 'failed':
      return `Failed: ${r.reason ?? ''}${r.retry ? '; trying again' : ''}`
  }
}

export function outcomeTone(r: HeatingRun): HeatingTone {
  switch (r.outcome) {
    case 'heated':
    case 'confirmed':
      return 'ok'
    case 'failed':
      return 'bad'
    case 'skipped':
    case 'no_answer':
      return 'warn'
  }
  return 'muted'
}

/**
 * The Overview's Heating row: the next departure's decision once made, else
 * when it would heat for it, else the latest decision; with the temperature
 * inside the car (the climate's current_temperature) when known. Null while
 * car heating is off and nothing was decided recently.
 */
export function heatingLine(
  h: HeatingSummary | undefined,
  next: Pick<Block, 'tripKeys' | 'firstTitle' | 'departAt'> | null,
  insideC?: number | null,
): { text: string; tone: HeatingTone } | null {
  if (!h) return null
  const inside = insideC != null ? ` · ${temp(insideC)} in the car` : ''
  const last = h.last
  if (last && (!next || last.tripKey === next.tripKeys[0] || !h.enabled)) {
    return { text: outcomeText(last) + inside, tone: outcomeTone(last) }
  }
  if (!next || !h.enabled) return null
  const at = new Date(new Date(next.departAt).getTime() - h.leadMin * 60000)
  return {
    text: `Below ${temp(h.belowC)}: heats to ${temp(h.targetC)} at ${clock(at)}, before ${next.firstTitle}${inside}`,
    tone: 'muted',
  }
}
