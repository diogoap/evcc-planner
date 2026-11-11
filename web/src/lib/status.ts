import type { Block, BlockStatus, Day, SendOption } from '../api/types'
import { clock, duration, pct, relativeDay } from './format'

export type Tone = 'ok' | 'warn' | 'bad' | 'muted' | 'info'

export const blockTone: Record<BlockStatus, Tone> = {
  done: 'muted',
  in_progress: 'info',
  ready: 'ok',
  charge: 'info',
  shortfall: 'bad',
  exceeds: 'bad',
  unknown: 'warn',
}

// Same whole-percent target the next-departure card shows.
const target = (d: Day) => (d.targetSoc != null ? `${d.targetSoc}%` : pct(d.requiredSoc))
const first = (d: Day) => (d.firstDepartAt ? `${target(d)} by ${clock(d.firstDepartAt)}` : undefined)

/**
 * Plan status for a day row: the charge the day's departures need (as SoC
 * points), with the first departure's target as detail.
 */
export function dayLabel(d: Day): { text: string; tone: Tone; detail?: string } {
  switch (d.status) {
    case 'none':
      return { text: d.disabledCount ? 'All trips off' : 'No trips', tone: 'muted' }
    case 'completed':
      return { text: 'Completed', tone: 'muted' }
    case 'ready':
      return { text: 'Covered', tone: 'ok', detail: first(d) }
    case 'in_progress':
      return { text: 'On the road', tone: 'info' }
    case 'charge':
      return d.chargePct > 0
        ? { text: `+${Math.ceil(d.chargePct - 1e-6)}% charge`, tone: 'info', detail: first(d) }
        : { text: 'Covered', tone: 'ok', detail: first(d) }
    case 'shortfall':
      return { text: 'Not enough time to charge', tone: 'bad', detail: first(d) }
    case 'exceeds':
      return { text: 'More than a full battery', tone: 'bad' }
    case 'unknown':
      return { text: 'SoC unknown', tone: 'warn', detail: first(d) }
    default:
      return { text: d.status, tone: 'muted' }
  }
}

/** Explains what a departure needs. */
export function blockLabel(b: Block): { text: string; tone: Tone } {
  switch (b.status) {
    case 'ready':
      return { text: 'Current charge already covers this.', tone: 'ok' }
    case 'charge': {
      const start = b.latestChargeStart ? `; start charging by ${clock(b.latestChargeStart)}` : ''
      return {
        text: `Charge ${b.chargeKWh.toFixed(1)} kWh (about ${duration(b.chargeMinutes)})${start}.`,
        tone: 'info',
      }
    }
    case 'shortfall':
      return {
        text: `Not enough time to charge: about ${Math.ceil(b.shortfall)}% short at departure.`,
        tone: 'bad',
      }
    case 'exceeds':
      return { text: 'Needs more than a full battery; plan a charging stop.', tone: 'bad' }
    case 'unknown':
      return { text: 'SoC unknown: plug in the car so EVCC can read it.', tone: 'warn' }
    case 'in_progress':
      return { text: 'On the road.', tone: 'info' }
    default:
      return { text: '', tone: 'muted' }
  }
}

/** "today", "tomorrow", or "Thu 12 Nov" for a local date. */
export function dayName(date: string, now: string) {
  const d = relativeDay(`${date}T12:00:00Z`, now)
  return d === 'Today' || d === 'Tomorrow' ? d.toLowerCase() : d
}

/** "on Thu 12 Nov", or just "today" / "tomorrow". */
export function onDay(date: string, now: string) {
  const d = dayName(date, now)
  return d === 'today' || d === 'tomorrow' ? d : `on ${d}`
}

const capitalize = (s: string) => s.charAt(0).toUpperCase() + s.slice(1)

/** "Trips today", "Today and tomorrow", "Tomorrow to Thu 12 Nov". */
export function sendOptionLabel(o: SendOption, now: string) {
  if (o.days === 1) return `Trips ${onDay(o.from, now)}`
  return capitalize(`${dayName(o.from, now)} ${o.days === 2 ? 'and' : 'to'} ${dayName(o.through, now)}`)
}
