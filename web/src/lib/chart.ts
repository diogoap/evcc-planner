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

/** A #rrggbb token colour with the given opacity (0–1). */
export function alpha(color: string, opacity: number) {
  if (!/^#[0-9a-f]{6}$/i.test(color)) return color
  return (
    color +
    Math.round(opacity * 255)
      .toString(16)
      .padStart(2, '0')
  )
}

/**
 * Calls render when the system switches between light and dark, so a chart
 * picks up the new colours. Returns the function that stops listening.
 */
export function onThemeChange(render: () => void) {
  if (!window.matchMedia) return () => {}
  const media = window.matchMedia('(prefers-color-scheme: dark)')
  media.addEventListener('change', render)
  return () => media.removeEventListener('change', render)
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
