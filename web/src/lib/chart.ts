// Registers only the Chart.js pieces the app uses, to keep the bundle small.
import {
  Chart,
  Filler,
  Legend,
  LineController,
  LineElement,
  LinearScale,
  PointElement,
  ScatterController,
  Tooltip,
} from 'chart.js'
import { clock, dayShort, localDate } from './format'

Chart.register(
  LineController,
  ScatterController,
  LineElement,
  PointElement,
  LinearScale,
  Filler,
  Tooltip,
  Legend,
)

export { Chart }

export function cssVar(name: string) {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim() || '#888'
}

/** Axis label: weekday at local midnight, otherwise HH:MM. */
export function timeTick(value: number | string) {
  const t = Number(value)
  const hhmm = clock(t)
  return hhmm === '00:00' ? dayShort(localDate(t)) : hhmm
}

/**
 * Ticks every `hours` hours on local clock boundaries (00:00, 03:00, ...),
 * for use in a scale's afterBuildTicks.
 */
export function hourTicks(hours: number) {
  return (axis: { min: number; max: number; ticks: { value: number }[] }) => {
    const ticks: { value: number }[] = []
    const hour = 3600 * 1000
    for (let t = Math.ceil(axis.min / hour) * hour; t <= axis.max; t += hour) {
      if (Number(clock(t).slice(0, 2)) % hours === 0) ticks.push({ value: t })
    }
    axis.ticks = ticks
  }
}
