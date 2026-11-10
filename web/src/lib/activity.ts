import type { ActivitySource, ActivityType, AuditEvent } from '../api/types'
import { dayShort, localDate } from './format'

export const sourceLabels: Record<ActivitySource, string> = {
  ui: 'UI',
  job: 'Scheduled job',
  calendar_sync: 'Calendar sync',
  automation: 'Automation',
}

export const typeLabels: Record<ActivityType, string> = {
  trip: 'Trips',
  schedule: 'Recurring schedules',
  place: 'Places',
  settings: 'Settings',
  evcc: 'EVCC plans and changes',
  advice: 'Advice',
  api: 'External API calls',
  system: 'Maintenance',
}

const subjectLabels: Record<string, string> = {
  trip: 'Trip',
  schedule: 'Schedule',
  place: 'Place',
  settings: 'Settings',
  plan: 'EVCC plan',
  adjustment: 'EVCC change',
  advice: 'Advice',
  system: 'Maintenance',
}

const serviceNames: Record<string, string> = {
  google_routes: 'Google Routes',
  calendar: 'Google Calendar',
  tibber: 'Tibber',
  openrouter: 'OpenRouter',
  knmi: 'KNMI',
}

/** What an event is about, e.g. "Trip" or "Google Routes". */
export function subjectLabel(e: AuditEvent) {
  if (e.type === 'api_call') return serviceNames[e.service ?? ''] ?? e.service ?? 'API call'
  return subjectLabels[e.subjectType] ?? e.subjectType
}

const fieldLabels: Record<string, string> = {
  title: 'Title',
  destination: 'Destination',
  arriveBy: 'Arrive by',
  until: 'Until',
  enabled: 'On',
  manualDistanceKm: 'Manual distance (km)',
  needsReview: 'Needs review',
  weekdays: 'Weekdays',
  exceptions: 'Exceptions',
  name: 'Name',
  address: 'Address',
  homeAddress: 'Home address',
  usableCapacityKWh: 'Usable capacity (kWh)',
  chargePowerKW: 'Charging power (kW)',
  consumptionKWhPer100Km: 'Consumption (kWh/100 km)',
  arrivalReservePct: 'Arrival reserve (%)',
  chargingEfficiencyPct: 'Charging efficiency (%)',
  departureBufferMin: 'Departure buffer (min)',
  chargeBufferMin: 'Charging time buffer (min)',
  defaultStayMin: 'Default stay (min)',
  autoApply: 'Automatic plans',
  autoMinConfidence: 'Minimum confidence',
  targetLittle: 'Target for little (%)',
  targetMedium: 'Target for medium (%)',
  targetHigh: 'Target for high (%)',
  readyBy: 'Ready by',
  expiryHours: 'Expiry (hours after ready-by)',
  autoPostpone: 'Automatic postponement',
  evccPlan: 'EVCC plan',
  cheapChargingLimit: 'Cheap-charging limit',
  status: 'Status',
  dismissed: 'Dismissed',
  // Details.
  method: 'Method',
  endpoint: 'Endpoint',
  slot: 'Time slot',
  departAt: 'Departure',
  from: 'From',
  to: 'To',
  calendar: 'Calendar',
  calendarId: 'Calendar ID',
  model: 'Model',
  articleChars: 'Article length (characters)',
  tripKey: 'Trip',
  evccResponse: 'EVCC response',
  adviceDate: 'Advice date',
  recommendation: 'Recommendation',
  confidence: 'Confidence',
  chargeIndicator: 'Charge indicator',
  before: 'Older than',
  deleted: 'Deleted',
}

/** Label for a recorded field; "enabled" in advice settings is daily advice itself. */
export function fieldLabel(key: string, e?: AuditEvent) {
  if (key === 'enabled' && e?.subjectType === 'settings') return 'Daily advice'
  if (fieldLabels[key]) return fieldLabels[key]
  const words = key.replace(/([a-z0-9])([A-Z])/g, '$1 $2').toLowerCase()
  return words.charAt(0).toUpperCase() + words.slice(1)
}

/** Shows a recorded value: Yes/No, lists, or "—" for nothing. */
export function formatValue(v: unknown): string {
  if (v === null || v === undefined || v === '') return '—'
  if (typeof v === 'boolean') return v ? 'Yes' : 'No'
  if (Array.isArray(v)) return v.length ? v.map(formatValue).join(', ') : 'none'
  if (typeof v === 'object') return JSON.stringify(v)
  return String(v)
}

export interface ChangeRow {
  key: string
  label: string
  before?: string
  after?: string
}

/**
 * The fields an event changed. A creation has only "after" values and a
 * deletion only "before" values; an edit has both for each changed field.
 */
export function changeRows(e: AuditEvent): ChangeRow[] {
  const before = e.before ?? undefined
  const after = e.after ?? undefined
  const keys = [...new Set([...Object.keys(after ?? {}), ...Object.keys(before ?? {})])]
  return keys.map((key) => ({
    key,
    label: fieldLabel(key, e),
    before: before ? formatValue(before[key]) : undefined,
    after: after ? formatValue(after[key]) : undefined,
  }))
}

/** Extra facts such as an API call's endpoint or the advice an action used. */
export function detailRows(e: AuditEvent) {
  return Object.entries(e.detail ?? {}).map(([key, v]) => ({
    key,
    label: fieldLabel(key, e),
    value: formatValue(v),
  }))
}

/** 1.2 kB */
export function size(bytes: number | undefined) {
  const b = bytes ?? 0
  if (b < 1024) return `${b} B`
  if (b < 1024 * 1024) return `${(b / 1024).toFixed(1)} kB`
  return `${(b / 1024 / 1024).toFixed(1)} MB`
}

/** HTTP 200 · 240 ms · 1.2 kB */
export function callLine(e: AuditEvent) {
  const status = e.statusCode ? `HTTP ${e.statusCode}` : 'No response'
  return `${status} · ${e.durationMs ?? 0} ms · ${size(e.bytes)}`
}

/** Events grouped by local day, keeping their (newest first) order. */
export function groupByDay(events: AuditEvent[]) {
  const groups: { date: string; events: AuditEvent[] }[] = []
  for (const e of events) {
    const date = localDate(e.at)
    const last = groups[groups.length - 1]
    if (last?.date === date) last.events.push(e)
    else groups.push({ date, events: [e] })
  }
  return groups
}

/** Route to a subject's history on the Activity page. */
export function historyLink(subject: string, label: string) {
  return { path: '/activity', query: { subject, label } }
}

/**
 * The history an event belongs to, with a readable name: its trip, schedule,
 * place, or the EVCC plan. Settings, advice, and API calls have none.
 */
export function eventHistory(e: AuditEvent): { subject: string; label: string } | undefined {
  const values = { ...(e.before ?? {}), ...(e.after ?? {}) } as Record<string, unknown>
  const name = (values.title ?? values.name) as string | undefined
  switch (e.subjectType) {
    case 'trip': {
      const arrive = typeof values.arriveBy === 'string' ? values.arriveBy.slice(0, 10) : ''
      const label = name ? (arrive ? `${name}, ${dayShort(arrive)}` : name) : 'this trip'
      return { subject: `trip:${e.subjectId}`, label }
    }
    case 'schedule':
      return { subject: `schedule:${e.subjectId}`, label: name ?? 'this schedule' }
    case 'place':
      return { subject: `place:${e.subjectId}`, label: name ?? 'this place' }
    case 'plan':
      return { subject: 'plan', label: 'the EVCC plan' }
    case 'adjustment':
      return e.related === 'plan' ? { subject: 'plan', label: 'the EVCC plan' } : undefined
  }
  return undefined
}
