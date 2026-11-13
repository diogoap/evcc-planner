// Types mirror the Go API (internal/service and internal/model).

export interface Meta {
  demo: boolean
  stubs: boolean
  timezone: string
  now: string
  windowDays: number
  /** Identifies the compiled UI the server serves; empty in development. */
  build: string
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
  /** How trip plans reach EVCC (AUTOMATION.md). */
  tripPlans: TripPlans
  /** Automatic mode: charge later trips before the next departure when cheaper. */
  chargeAhead: boolean
  /** EUR/kWh that charging ahead must save to start. */
  chargeAheadMinSaving: number
  /** Charging ahead never raises the target above this SoC (%). */
  chargeAheadMaxSoc: number
  /** Subtracted from the solar forecast (kW). */
  householdKW: number
  /** EUR/kWh of tax not paid on own solar: a solar kWh counts as the grid price minus this. */
  solarTaxSaving: number
}

export type TripPlans = 'manual' | 'plug_in' | 'automatic'

/** The plan for the next departure: its own target plus later trips charged ahead. */
export interface Target {
  soc: number
  minSoc: number
  readyBy: string
  tripKey: string
  title: string
  /** The projected SoC already reaches the target. */
  covered: boolean
  /** Later trips charged ahead (automatic mode). */
  ahead: AheadTrip[]
  savingEur: number
  /** The planner's estimate of the charging slots; EVCC picks the actual ones. */
  charging: Rate[]
  horizonEnd?: string
}

export interface AheadTrip {
  tripKey: string
  title: string
  departAt: string
  energyPct: number
  /** EUR/kWh before the next departure. */
  priceAhead: number
  /** EUR/kWh at home before the trip; null when that time is too short. */
  priceLater: number | null
  savingEur: number
}

export interface ControlView {
  mode: TripPlans
  /** A chosen plan has paused automatic changes. */
  paused: boolean
  pausedAt?: string
  pausedPlan?: PlanValue
  target: Target | null
}

/** Where a SoC reading comes from: EVCC (only while plugged in) or Home Assistant (always). */
export type SoCSource = 'evcc' | 'homeassistant'

export interface SoCView {
  percent: number | null
  known: boolean
  /** Empty when the SoC is unknown. */
  source: SoCSource | ''
  /** Read from EVCC just now (car plugged in). */
  live: boolean
  observedAt: string | null
  /** Not live and older than three hours. */
  stale: boolean
  estimated: boolean
  projected: number | null
  /** The source's range at the reading (km), if it reports one. */
  rangeKm: number | null
  /** The other sources' last readings, newest first. */
  other: SoCReading[]
}

export interface SoCReading {
  source: SoCSource
  percent: number
  observedAt: string
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
  /** The projected SoC at the next departure already reaches the target. */
  covered: boolean
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
  control: ControlView
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
  /**
   * Charge ahead from solar: keep the car plugged in while the sun shines
   * (solarFrom to solarUntil), at solarPrice for up to solarPct; no plan.
   */
  solar?: boolean
  solarPct?: number
  solarPrice?: number
  solarFrom?: string
  solarUntil?: string
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
  /** EVCC's PV forecast from the current hour (today and tomorrow). */
  solar: SolarPoint[]
  plan: EVCCPlan | null
  /** Where Home Assistant's car location sensor puts the car, if one is set. */
  location: CarLocation | null
}

/** "home", "not_home", or the name of a Home Assistant zone. */
export interface CarLocation {
  state: string
  home: boolean
  since: string
}

/** Forecast PV power at an instant, linear in between, with EVCC's correction. */
export interface SolarPoint {
  time: string
  kw: number
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

/** Integration settings edited under Settings → Connections (no secrets). */
export interface ConnectionSettings {
  evccUrl: string
  evccLoadpoint: number
  evccVehicle: string
  evccPollIntervalSec: number
  evccTimeoutSec: number
  haUrl: string
  haSocEntity: string
  haRangeEntity: string
  haUpdatedEntity: string
  haPollIntervalMin: number
  haTimeoutSec: number
  calendarSyncIntervalMin: number
  routesTrafficAware: boolean
  routesCacheTtlHours: number
  routesMonthlyFreeCalls: number
  tibberUrl: string
  openRouterModel: string
  openRouterTimeoutSec: number
  knmiDataset: string
  knmiVersion: string
  forecastDailyAt: string
  forecastRetryIntervalMin: number
  forecastVatPct: number
  retentionDays: number
}

/** A secret that stays in an environment variable: only whether it is set. */
export interface EnvValue {
  name: string
  env: string
  set: boolean
}

export interface ConnectionSettingsView {
  settings: ConnectionSettings
  defaults: ConnectionSettings
  env: EnvValue[]
  /** The status after a save checked EVCC and Home Assistant. */
  connections?: Connection[]
}

export interface HASensor {
  entityId: string
  name: string
  state: string
  unit: string
  kind: 'battery' | 'range' | 'timestamp'
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

export type ActivitySource = 'ui' | 'job' | 'calendar_sync' | 'automation' | 'notification'
export type ActivityType =
  'trip' | 'schedule' | 'place' | 'settings' | 'evcc' | 'advice' | 'notification' | 'api' | 'system'

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

/** Phone notifications through the Home Assistant Companion app (AUTOMATION.md). */
export interface NotificationSettings {
  enabled: boolean
  /** Home Assistant notify services, e.g. mobile_app_phone. */
  phones: string[]
  /** The planner's address as the phone reaches it, for View plan. */
  linkUrl: string
  /** Optional entity that is "home" while the car is home. */
  locationEntity: string
  early: boolean
  earlyLeadMin: number
  urgentLeadMin: number
  quietFrom: string
  quietTo: string
  urgentInQuiet: boolean
  snoozeMin: number
  problems: boolean
  questions: boolean
  ready: boolean
  /** Plug in for the sun: today's advice is to charge ahead from solar. */
  solar: boolean
}

export type NotificationKind =
  | 'test'
  | 'early'
  | 'urgent'
  | 'late'
  | 'stopped'
  | 'unplugged'
  | 'mode'
  | 'missed'
  | 'question'
  | 'ready'
  | 'solar'
  | 'info'

export type NotificationVerb = 'snooze' | 'skip' | 'view' | 'count' | 'notrip'

export interface PhoneNotification {
  id: number
  key: string
  kind: NotificationKind
  subject?: string
  title: string
  message: string
  verbs: NotificationVerb[]
  urgent: boolean
  state: 'sent' | 'snoozed' | 'done' | 'failed'
  note?: string
  latest?: string
  createdAt: string
  sentAt?: string
  snoozedUntil?: string
  resolvedAt?: string
}
