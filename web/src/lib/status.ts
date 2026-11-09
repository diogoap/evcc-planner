import type { Block, BlockStatus, Day } from '../api/types'
import { clock, duration, pct } from './format'

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

/** One-line plan status for a day row. */
export function dayLabel(d: Day): { text: string; tone: Tone } {
  switch (d.status) {
    case 'none':
      return { text: d.disabledCount ? 'All trips off' : 'No trips', tone: 'muted' }
    case 'completed':
      return { text: 'Completed', tone: 'muted' }
    case 'ready':
      return { text: 'Covered', tone: 'ok' }
    case 'in_progress':
      return { text: 'On the road', tone: 'info' }
    case 'charge':
      return {
        text: `Needs ${pct(d.requiredSoc)}${d.firstDepartAt ? ` by ${clock(d.firstDepartAt)}` : ''}`,
        tone: 'info',
      }
    case 'shortfall':
      return { text: 'Not enough time to charge', tone: 'bad' }
    case 'exceeds':
      return { text: 'More than a full battery', tone: 'bad' }
    case 'unknown':
      return { text: `Needs ${pct(d.requiredSoc)} · SoC unknown`, tone: 'warn' }
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
