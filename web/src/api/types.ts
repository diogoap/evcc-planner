// Types mirror the Go API (internal/service and internal/model).

export interface Meta {
  demo: boolean
  stubs: boolean
  timezone: string
  now: string
  windowDays: number
}

export interface Settings {
  homeAddress: string
  usableCapacityKWh: number
  chargePowerKW: number
  consumptionKWhPer100Km: number
  arrivalReservePct: number
  chargingEfficiencyPct: number
  departureBufferMin: number
  chargeBufferMin: number
  defaultStayMin: number
}

export interface SoCView {
  percent: number | null
  known: boolean
  live: boolean
  observedAt: string | null
  estimated: boolean
  projected: number | null
}

export type TripSource = 'manual' | 'recurring' | 'calendar'

export interface Trip {
  key: string
  source: TripSource
  sourceLabel: string
  sourceId?: number
  title: string
  destination: string
  address: string
  placeName?: string
  date: string
  arriveAt: string
  endAt: string
  arriveLocal: string
  endLocal: string
  departAt: string
  returnAt: string
  departLocal: string
  enabled: boolean
  needsReview: boolean
  overridden: string[]
  distanceKm: number | null
  distanceOverrideKm: number | null
  distanceSource: 'route' | 'manual' | 'unknown'
  routeProvider?: string
  routeStale?: boolean
  routeError?: string
  energyKWh: number | null
  state: 'upcoming' | 'in_progress' | 'done'
  blockIndex?: number
  requiredSoc?: number
}

export interface IgnoredEvent {
  key: string
  calendar: string
  title: string
  location: string
  reason: string
  start: string
}

export type BlockStatus = 'done' | 'in_progress' | 'ready' | 'charge' | 'shortfall' | 'exceeds' | 'unknown'
export type DayStatus = 'none' | 'completed' | BlockStatus

export interface Day {
  date: string
  distanceKm: number
  energyKWh: number
  tripCount: number
  disabledCount: number
  reviewCount: number
  status: DayStatus
  requiredSoc?: number
  firstDepartAt?: string
}

export interface Block {
  index: number
  tripKeys: string[]
  titles: string[]
  firstTitle: string
  firstDestination: string
  firstArriveAt: string
  departAt: string
  returnAt: string
  distanceKm: number
  energyKWh: number
  energyPct: number
  ownRequiredSoc: number
  requiredSoc: number
  targetSoc: number
  status: BlockStatus
  overlap: boolean
  startSoc: number | null
  departSoc: number | null
  arrivalSoc: number | null
  chargeKWh: number
  gridKWh: number
  chargeMinutes: number
  chargeFrom: string | null
  latestChargeStart: string | null
  shortfall: number
}

export interface TimelinePoint {
  at: string
  soc: number
  kind: 'now' | 'charge_start' | 'charge_end' | 'depart' | 'return'
}

export interface Plan {
  now: string
  timezone: string
  from: string
  to: string
  settings: Settings
  homeMissing: boolean
  soc: SoCView
  trips: Trip[]
  ignored: IgnoredEvent[]
  days: Day[]
  blocks: Block[]
  next: Block | null
  timeline: TimelinePoint[]
  warnings: string[]
}

export interface Rate {
  start: string
  end: string
  value: number
}

export interface EVCCPlan {
  soc: number
  time: string
  setByPlanner: boolean
}

export interface Status {
  mode: string
  reachable: boolean
  error?: string
  updatedAt?: string
  version?: string
  charger?: string
  vehicle?: string
  vehicleTitle?: string
  connected: boolean
  charging: boolean
  chargePowerW: number
  chargeMode?: string
  limitSoc?: number
  gridPrice: number | null
  pvPowerW: number
  currency?: string
  priceSlots: Rate[]
  plan: EVCCPlan | null
}

export interface Preview {
  available: boolean
  reason?: string
  soc: number
  time: string
  durationMinutes: number
  powerW: number
  slots: Rate[]
  priceSlots: Rate[]
  currentPlan: EVCCPlan | null
  vehicle: string
}

export interface Place {
  id: number
  name: string
  address: string
}

export interface DateRange {
  from: string
  to: string
  note?: string
}

export interface Template {
  id: number
  title: string
  destination: string
  weekdays: number[]
  start: string
  end: string
  enabled: boolean
  exceptions: DateRange[]
}

export interface Occurrence {
  date: string
  key: string
  enabled: boolean
  exception?: string
  overridden: string[]
}

export interface TemplateView extends Template {
  occurrences: Occurrence[]
}

export interface Connection {
  name: string
  mode?: string
  status: 'ok' | 'error' | 'stub' | 'not_configured' | 'planned'
  detail: string
  lastSuccessAt?: string
}

export interface TripInput {
  title?: string
  destination?: string
  date?: string
  start?: string
  end?: string
  enabled?: boolean
  distanceOverrideKm?: number | null
  reviewed?: boolean
}
