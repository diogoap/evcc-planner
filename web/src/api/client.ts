import type {
  ActivityFilter,
  ActivityPage,
  ActivityUsage,
  AdjustInput,
  Adjustment,
  AdjustmentKind,
  AdviceSettings,
  Connection,
  ConnectionSettings,
  ConnectionSettingsView,
  EVCCPlan,
  HASensor,
  Meta,
  Place,
  Plan,
  Preview,
  Proposal,
  Settings,
  Status,
  Template,
  TemplateView,
  TripInput,
} from './types'

export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message)
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const init: RequestInit = { method, headers: { Accept: 'application/json' } }
  if (body !== undefined) {
    init.body = JSON.stringify(body)
    init.headers = { ...init.headers, 'Content-Type': 'application/json' }
  }
  let res: Response
  try {
    res = await fetch(path, init)
  } catch {
    throw new ApiError('The planner service is not reachable.', 0)
  }
  if (res.status === 204) return undefined as T
  const text = await res.text()
  let data: unknown = undefined
  if (text) {
    try {
      data = JSON.parse(text)
    } catch {
      data = undefined
    }
  }
  if (!res.ok) {
    const msg = (data as { error?: string } | undefined)?.error ?? `Request failed (${res.status})`
    throw new ApiError(msg, res.status)
  }
  return data as T
}

const enc = encodeURIComponent

/** Query string for activity filters; empty values are left out. */
export function activityQuery(f: ActivityFilter, extra: Record<string, string | number | undefined> = {}) {
  const params = new URLSearchParams()
  for (const [k, v] of Object.entries({ ...f, ...extra })) {
    if (v !== undefined && v !== '') params.set(k, String(v))
  }
  const q = params.toString()
  return q ? `?${q}` : ''
}

export const api = {
  meta: () => request<Meta>('GET', '/api/meta'),
  plan: () => request<Plan>('GET', '/api/plan'),
  status: () => request<Status>('GET', '/api/status'),
  refreshEVCC: () => request<Status>('POST', '/api/evcc/refresh', {}),

  createTrip: (t: TripInput) => request<{ key: string }>('POST', '/api/trips', t),
  updateTrip: (key: string, t: TripInput) => request<{ key: string }>('PATCH', `/api/trips/${enc(key)}`, t),
  deleteTrip: (key: string) => request<void>('DELETE', `/api/trips/${enc(key)}`),
  resetTrip: (key: string) => request<void>('POST', `/api/trips/${enc(key)}/reset`, {}),

  templates: () => request<TemplateView[]>('GET', '/api/recurring'),
  createTemplate: (t: Omit<Template, 'id'>) => request<Template>('POST', '/api/recurring', t),
  updateTemplate: (t: Template) => request<Template>('PUT', `/api/recurring/${t.id}`, t),
  deleteTemplate: (id: number) => request<void>('DELETE', `/api/recurring/${id}`),

  places: () => request<Place[]>('GET', '/api/places'),
  createPlace: (p: Omit<Place, 'id'>) => request<Place>('POST', '/api/places', p),
  updatePlace: (p: Place) => request<Place>('PUT', `/api/places/${p.id}`, p),
  deletePlace: (id: number) => request<void>('DELETE', `/api/places/${id}`),

  settings: () => request<Settings>('GET', '/api/settings'),
  saveSettings: (s: Settings) => request<Settings>('PUT', '/api/settings', s),

  preview: (soc: number, time: string) =>
    request<Preview>('GET', `/api/evcc/preview?soc=${soc}&time=${enc(time)}`),
  sendPlan: (soc: number, time: string, tripKey: string, through?: string) =>
    request<EVCCPlan>('POST', '/api/evcc/plan', { soc, time, tripKey, through, confirm: true }),

  connections: () => request<Connection[]>('GET', '/api/connections'),
  syncCalendar: () => request<Connection[]>('POST', '/api/calendar/sync', {}),
  refreshForecasts: () => request<Connection[]>('POST', '/api/forecasts/refresh', {}),
  forecasts: () => request<unknown>('GET', '/api/forecasts'),

  connectionSettings: () => request<ConnectionSettingsView>('GET', '/api/settings/connections'),
  saveConnectionSettings: (s: ConnectionSettings) =>
    request<ConnectionSettingsView>('PUT', '/api/settings/connections', s),
  homeAssistantSensors: () => request<HASensor[]>('GET', '/api/homeassistant/sensors'),

  adviceSettings: () => request<AdviceSettings>('GET', '/api/settings/advice'),
  saveAdviceSettings: (s: AdviceSettings) => request<AdviceSettings>('PUT', '/api/settings/advice', s),
  proposal: (date: string, kind: AdjustmentKind) =>
    request<Proposal>('GET', `/api/advice/${enc(date)}/proposal?kind=${kind}`),
  dismiss: (date: string, undo = false) =>
    request<void>('POST', `/api/advice/${enc(date)}/dismiss`, { undo }),
  applyAdjustment: (a: AdjustInput) =>
    request<Adjustment>('POST', '/api/adjustments', { ...a, confirm: true }),
  cancelAdjustment: (id: number) => request<Adjustment>('POST', `/api/adjustments/${id}/cancel`, {}),
  activity: (f: ActivityFilter, before?: number) =>
    request<ActivityPage>('GET', `/api/activity${activityQuery(f, { before })}`),
  activityUsage: (month?: string) =>
    request<ActivityUsage>('GET', `/api/activity/usage${activityQuery({}, { month })}`),
  /** Download link for the filtered activity log. */
  activityExportUrl: (format: 'csv' | 'json', f: ActivityFilter) =>
    `/api/activity/export${activityQuery(f, { format })}`,
  demoVehicle: (connected: boolean, soc: number) =>
    request<Status>('POST', '/api/demo/vehicle', { connected, soc }),
}
