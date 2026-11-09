// Formatting always uses the planner's timezone (Europe/Amsterdam by
// default), not the browser's, so times match the plan everywhere.

let zone = 'Europe/Amsterdam'
const cache = new Map<string, Intl.DateTimeFormat>()

export function setTimeZone(tz: string) {
  zone = tz
  cache.clear()
}

export function timeZone() {
  return zone
}

function fmt(key: string, opts: Intl.DateTimeFormatOptions) {
  let f = cache.get(key)
  if (!f) {
    f = new Intl.DateTimeFormat('en-GB', { timeZone: zone, ...opts })
    cache.set(key, f)
  }
  return f
}

const toDate = (v: string | number | Date) => (v instanceof Date ? v : new Date(v))

/** 07:48 */
export function clock(v: string | number | Date) {
  return fmt('clock', { hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }).format(toDate(v))
}

function parts(v: string | number | Date) {
  const out: Record<string, string> = {}
  for (const p of fmt('parts', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hourCycle: 'h23',
  }).formatToParts(toDate(v))) {
    out[p.type] = p.value
  }
  return out
}

/** Local YYYY-MM-DD for an instant. */
export function localDate(v: string | number | Date) {
  const p = parts(v)
  return `${p.year}-${p.month}-${p.day}`
}

// Dates without time (YYYY-MM-DD) are formatted at noon UTC so the weekday
// is right in every European zone.
const noon = (date: string) => new Date(`${date}T12:00:00Z`)

/** Tue 10 */
export function dayShort(date: string) {
  const d = noon(date)
  const wd = new Intl.DateTimeFormat('en-GB', { weekday: 'short', timeZone: 'UTC' }).format(d)
  return `${wd} ${String(d.getUTCDate()).padStart(2, '0')}`
}

/** Tuesday 10 November */
export function dayLong(date: string) {
  return new Intl.DateTimeFormat('en-GB', {
    weekday: 'long',
    day: 'numeric',
    month: 'long',
    timeZone: 'UTC',
  }).format(noon(date))
}

/** Tue 10 Nov, 07:48 */
export function dateTime(v: string | number | Date) {
  const d = toDate(v)
  const day = fmt('dt', { weekday: 'short', day: 'numeric', month: 'short' }).format(d)
  return `${day.replace(/,/g, '')}, ${clock(d)}`
}

/** Today / Tomorrow / Tue 10 Nov, relative to now. */
export function relativeDay(v: string | number | Date, now: string | number | Date) {
  const target = localDate(v)
  const today = localDate(now)
  const tomorrow = localDate(toDate(now).getTime() + 86400000)
  if (target === today) return 'Today'
  if (target === tomorrow) return 'Tomorrow'
  return fmt('rd', { weekday: 'short', day: 'numeric', month: 'short' }).format(toDate(v)).replace(/,/g, '')
}

/** "just now", "12 min ago", "3 h ago", or a date. */
export function ago(v: string | number | Date, now: string | number | Date) {
  const mins = Math.round((toDate(now).getTime() - toDate(v).getTime()) / 60000)
  if (mins < 1) return 'just now'
  if (mins < 60) return `${mins} min ago`
  if (mins < 24 * 60) return `${Math.round(mins / 60)} h ago`
  return dateTime(v)
}

/** 12 km, 6.5 km */
export function km(v: number | null | undefined) {
  if (v == null) return '—'
  return v >= 10 || v === 0 ? `${Math.round(v)} km` : `${v.toFixed(1)} km`
}

/** 4.3 kWh */
export function kwh(v: number | null | undefined) {
  if (v == null) return '—'
  return `${v.toFixed(1)} kWh`
}

/** 0.0 kW from watts */
export function kw(watts: number | null | undefined) {
  if (watts == null) return '—'
  return `${(watts / 1000).toFixed(1)} kW`
}

/** €0.24/kWh, always including VAT */
export function price(v: number | null | undefined) {
  if (v == null) return '—'
  return `€${v.toFixed(2)}/kWh`
}

export function pct(v: number | null | undefined) {
  if (v == null) return '—'
  return `${Math.round(v)}%`
}

/** 42 min, 1 h 20 min */
export function duration(minutes: number) {
  if (minutes < 60) return `${minutes} min`
  const h = Math.floor(minutes / 60)
  const m = minutes % 60
  return m ? `${h} h ${m} min` : `${h} h`
}

const weekdayNames = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun']

/** Mon–Fri, Sat, Sun or Mon, Wed, Fri */
export function weekdays(days: number[]) {
  const sorted = [...new Set(days)].sort((a, b) => a - b)
  if (sorted.length === 7) return 'Every day'
  const runs: number[][] = []
  for (const d of sorted) {
    const last = runs[runs.length - 1]
    if (last && d === last[last.length - 1]! + 1) last.push(d)
    else runs.push([d])
  }
  return runs
    .map((r) =>
      r.length >= 3
        ? `${weekdayNames[r[0]! - 1]}–${weekdayNames[r[r.length - 1]! - 1]}`
        : r.map((d) => weekdayNames[d - 1]).join(', '),
    )
    .join(', ')
}

function offsetMinutes(instant: number) {
  const p = parts(instant)
  const asUTC = Date.UTC(+p.year!, +p.month! - 1, +p.day!, +p.hour! % 24, +p.minute!)
  return Math.round((asUTC - Math.floor(instant / 60000) * 60000) / 60000)
}

/** Converts a local date and HH:MM in the planner's zone to an ISO instant. */
export function zonedToISO(date: string, hhmm: string) {
  const [y, mo, d] = date.split('-').map(Number)
  const [h, mi] = hhmm.split(':').map(Number)
  const wall = Date.UTC(y!, mo! - 1, d!, h!, mi!)
  let t = wall - offsetMinutes(wall) * 60000
  t = wall - offsetMinutes(t) * 60000
  return new Date(t).toISOString().replace('.000Z', 'Z')
}
