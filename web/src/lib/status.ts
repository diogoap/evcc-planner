import type {
  AheadTrip,
  Block,
  BlockStatus,
  ControlView,
  Day,
  PlanValue,
  SendOption,
  TripPlans,
} from '../api/types'
import { clock, dateTime, dayShort, duration, localDate, pct, price, relativeDay } from './format'

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

export const tripPlansText: Record<TripPlans, string> = {
  manual: 'Send manually',
  plug_in: 'Sent when plugged in',
  automatic: 'Automatic',
}

/** "80% by Wed 11 Nov, 07:00". */
export function planText(p: PlanValue) {
  return `${Math.round(p.soc)}% by ${dateTime(p.time)}`
}

/** The trip plan mode, and what automatic plans do right now. */
export function controlLabel(
  c: ControlView,
  connected: boolean,
): { mode: string; detail: string; tone: Tone } {
  if (c.mode !== 'automatic') return { mode: tripPlansText[c.mode], detail: '', tone: 'muted' }
  if (c.paused) {
    const chosen = c.pausedPlan ? ` (${planText(c.pausedPlan)})` : ''
    return {
      mode: 'Automatic · paused',
      detail: `Paused while the chosen plan${chosen} is in EVCC; resumes when it finishes.`,
      tone: 'warn',
    }
  }
  const t = c.target
  if (!t) return { mode: 'Automatic', detail: 'No upcoming trip needs a plan.', tone: 'muted' }
  const target = planText({ soc: t.soc, time: t.readyBy })
  if (t.covered) return { mode: 'Automatic', detail: `${target}: the charge already covers it.`, tone: 'ok' }
  if (!connected) return { mode: 'Automatic', detail: `Sends ${target} when plugged in.`, tone: 'info' }
  return { mode: 'Automatic', detail: `Target ${target}.`, tone: 'info' }
}

/** Why a later trip is in the target: "Includes School pickup (Tue 10 Nov 14:33): …". */
export function aheadText(a: AheadTrip) {
  const when = `${dayShort(localDate(a.departAt))} ${clock(a.departAt)}`
  if (a.priceLater == null)
    return `Includes ${a.title} (${when}): too little time at home before it to charge.`
  const saving = a.savingEur >= 0.05 ? `, saves about €${a.savingEur.toFixed(2)}` : ''
  return `Includes ${a.title} (${when}): ${price(a.priceAhead)} before the next departure vs ${price(a.priceLater)} before it${saving}.`
}
