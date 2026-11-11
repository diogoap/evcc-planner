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
  /** EVCC's range at the reading (km); only reported while plugged in. */
  rangeKm: number | null
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
  /** Set when the distance is unknown (no location or no route); the trip is off. */
  disabledReason?: string
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
  /** Trips already back home; they are not listed. */
  doneCount: number
  reviewCount: number
  status: DayStatus
  /** SoC points to add for the day's departures (minimum plan). */
  chargePct: number
  /** Target of the day's first upcoming departure. */
  requiredSoc?: number
  targetSoc?: number
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
  chargePct: number
  chargeKWh: number
  gridKWh: number
  chargeMinutes: number
  chargeFrom: string | null
  latestChargeStart: string | null
  shortfall: number
}

/** A plan by the next departure covering every trip through a day, with no charging in between. */
export interface SendOption {
  days: number
  from: string
  through: string
  readyBy: string
  requiredSoc: number
  targetSoc: number
  tripKeys: string[]
  titles: string[]
  distanceKm: number
  energyKWh: number
  exceeds: boolean
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
  /** Plans for the next departure that also cover one to three days of trips. */
  sendOptions: SendOption[]
  timeline: TimelinePoint[]
  warnings: string[]
  advice: AdviceView
}

export type Recommendation = 'none' | 'charge_ahead' | 'defer'
export type Indicator = 'little' | 'medium' | 'high'
export type PriceBasis = 'published' | 'estimate' | 'weekly'
export type AdjustmentKind = 'extra_plan' | 'postpone'

export interface DayPrice {
  /** EUR/kWh including VAT (daily average). */
  value: number
  basis: PriceBasis
  detail: string
  stale: boolean
  /** Published days: the lowest slot (for today, of the remaining ones). */
  min?: number
  minAt?: string
}

export interface WeatherDay {
  date: string
  sunshinePct: number | null
  precipChancePct: number | null
  precipMinMm: number | null
  precipMaxMm: number | null
  tempMinLowC: number | null
  tempMinHighC: number | null
  tempMaxLowC: number | null
  tempMaxHighC: number | null
  windDir: string
  windBft: number | null
}

export interface PlanValue {
  soc: number
  time: string
}

export interface Adjustment {
  id: number
  kind: AdjustmentKind
  source: 'ui' | 'auto'
  adviceDate: string
  vehicle: string
  loadpoint: number
  plan?: PlanValue
  prevPlan?: PlanValue
  smartCostLimit?: number
  prevSmartCostLimit?: number
  expiresAt: string
  status: 'active' | 'restored' | 'kept' | 'cancelled' | 'failed'
  note: string
  createdAt: string
  endedAt?: string
}

export interface DayAdvice {
  date: string
  recommendation: Recommendation
  /** Weak or missing evidence: "No data / no suggestion". */
  noSuggestion: boolean
  reason?: string
  /** 1 low, 2 medium, 3 high; 0 without a suggestion. */
  confidence: number
  indicator?: Indicator
  summary: string
  rationale: string[]
  compareDate?: string
  deferUntil?: string
  laterChargePct: number
  price: DayPrice | null
  /** KNMI's national outlook (wind, temperature). */
  weather: WeatherDay | null
  /** Local PV from EVCC's solar forecast, kWh before household use; null beyond the forecast. */
  solarKWh: number | null
  solarHomeKWh: number | null
  /** Where the day's sun figure comes from: EVCC (local, kWh) or KNMI (national, %). */
  sunSource: 'evcc' | 'knmi' | ''
  marketWind?: string
  marketWindGW?: number
  outlookNote?: string
  dismissed: boolean
  /** Plan action offered; absent for weak, no-data, dismissed, or "none" advice. */
  action?: AdjustmentKind
  adjustment?: Adjustment
}

export interface AdviceSource {
  name: string
  kind: 'published' | 'solar' | 'estimate' | 'weather'
  status: 'ok' | 'stale' | 'missing' | 'error' | 'stub'
  detail: string
  updatedAt?: string
}

export interface AdviceView {
  enabled: boolean
  autoApply: boolean
  days: DayAdvice[]
  sources: AdviceSource[]
  adjustments: Adjustment[]
}

export interface AdviceSettings {
  enabled: boolean
  autoApply: boolean
  autoMinConfidence: 2 | 3
  targetLittle: number | null
  targetMedium: number | null
  targetHigh: number | null
  readyBy: string
  expiryHours: number | null
  autoPostpone: boolean
}

export interface Proposal {
  kind: AdjustmentKind
  date: string
  available: boolean
  reason?: string
  advice: DayAdvice | null
  soc?: number
  minSoc?: number
  startSoc: number | null
  capacityKWh: number
  readyBy?: string
  latestReadyBy?: string
  protected?: { title: string; departAt: string; targetSoc: number }
  currentPlan: EVCCPlan | null
  smartCostLimit: number | null
  expiresAt?: string
  maxExpiresAt?: string
  notes: string[]
}

export interface AdjustInput {
  kind: AdjustmentKind
  date: string
  soc?: number
  readyBy?: string
  expiresAt: string
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

export type ActivitySource = 'ui' | 'job' | 'calendar_sync' | 'automation'
export type ActivityType = 'trip' | 'schedule' | 'place' | 'settings' | 'evcc' | 'advice' | 'api' | 'system'

/** One entry of the append-only activity log; before/after hold the changed values. */
export interface AuditEvent {
  id: number
  at: string
  type: 'action' | 'api_call'
  source: ActivitySource
  action: string
  subjectType: string
  subjectId?: string
  related?: string
  summary: string
  before?: Record<string, unknown> | null
  after?: Record<string, unknown> | null
  detail?: Record<string, unknown> | null
  outcome: 'success' | 'failure'
  error?: string
  service?: string
  statusCode?: number
  durationMs?: number
  bytes?: number
}

export interface ActivityPage {
  events: AuditEvent[]
  next: number | null
  retentionDays: number
}

export interface ActivityFilter {
  type?: ActivityType | ''
  source?: ActivitySource | ''
  from?: string
  to?: string
  /** e.g. trip:manual:3, schedule:2, plan */
  subject?: string
}

export interface ServiceUsage {
  service: string
  name: string
  calls: number
  failures: number
  bytes: number
  avgMs: number
  freeCalls?: number
  live: boolean
  note?: string
}

export interface ActivityUsage {
  month: string
  services: ServiceUsage[]
}
