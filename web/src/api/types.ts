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
  /** Always stored in kWh/100 km. */
  consumptionKWhPer100Km: number
  /** How the consumption is entered and shown. */
  consumptionUnit: ConsumptionUnit
  /** Optional Home Assistant sensor whose last valid value replaces the consumption. */
  consumptionEntity: string
  /** The unit that sensor reports in. */
  consumptionEntityUnit: ConsumptionUnit
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

export type TripPlans = 'manual' | 'plug_in' | 'automatic' | 'ai'

export type ConsumptionUnit = 'kwh_per_100km' | 'km_per_kwh'

/** The consumption the plan uses, and where it comes from. */
export interface ConsumptionView {
  kwhPer100Km: number
  source: 'manual' | 'homeassistant'
  /** The Home Assistant sensor, when one is chosen, and its unit. */
  entity?: string
  unit?: ConsumptionUnit
  /** The sensor's last valid value (in its unit) and when Home Assistant received it. */
  sensorValue?: number
  observedAt?: string
  /** Why the latest reading was not used, or why the sensor isn't used at all. */
  note?: string
}

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
  /** "ai" when the AI plan set the target (AI mode), with its reason. */
  source?: 'ai'
  reason?: string
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
  consumption: ConsumptionView
  /** AI mode's state; absent in other modes. */
  ai?: AIPlanView
}

/** AI mode on the Overview: whether an AI plan applies, and if not, why. */
export interface AIPlanView {
  inUse: boolean
  running: boolean
  /** A model (or the demo's stand-in) is set up. */
  canRun: boolean
  note?: string
  runId?: number
  runAt?: string
  summary?: string
}

export type Recommendation = 'none' | 'charge_extra' | 'defer'
/** How strongly the coming days look cheaper (weekly outlook, wind, sun); it picks the charge-extra steps. */
export type Indication = 'little' | 'medium' | 'high'

/** The price charge extra compares a day's blocks with. */
export interface AdviceReference {
  value: number
  /** Tomorrow's lowest published price, the lowest the outlook expects for it, or the past days' average lowest price. */
  basis: 'published' | 'outlook' | 'history'
  days?: number
  detail: string
  /** Published and outlook: the day it is the lowest price of (YYYY-MM-DD) and its weekday's name. */
  date?: string
  day?: string
  /** Outlook: from the day's estimate or the week's average, and whether the outlook is old. */
  source?: 'estimate' | 'weekly'
  stale?: boolean
}

/** A time range of a day, with its (average) price and the SoC it charges. */
export interface AdviceSpan {
  from: string
  until: string
  price: number
  pct?: number
}
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
  indication?: Indication
  reference?: AdviceReference
  /** Charge extra: the SoC to charge to, the step's discount, SoC above the trips' need, and the blocks. */
  targetSoc?: number
  stepDiscountPct?: number
  extraPct?: number
  blocks?: AdviceSpan[]
  /** A trip that misses very cheap or negative prices. */
  awayHint?: string
  /** What to do with solar while prices are negative. */
  exportHint?: string
  /** Days without published prices: how the outlook compares with today. */
  outlook?: 'cheaper' | 'dearer' | 'similar'
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
  /** Why advice that would offer an action offers none: it would change nothing in EVCC. */
  actionNote?: string
  /**
   * Charge extra from solar: keep the car plugged in while the sun shines
   * (solarFrom to solarUntil), for up to solarPct; no plan. It counts at
   * solarPrice (without the tax not paid on own solar); the grid charges
   * solarGridPrice in those slots.
   */
  solar?: boolean
  solarPct?: number
  solarPrice?: number
  solarGridPrice?: number
  solarFrom?: string
  solarUntil?: string
  adjustment?: Adjustment
  /** Decided by the AI model (AI mode), not the automatic rules. */
  ai?: boolean
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

/** Charge extra up to targetSoc with blocks at least minDiscountPct below the reference price. */
export interface ExtraStep {
  minDiscountPct: number
  targetSoc: number
}

/** Charge extra at one indication of cheaper days ahead: off, or three steps. */
export interface ExtraLevel {
  enabled: boolean
  steps: ExtraStep[]
}

export interface AdviceSettings {
  enabled: boolean
  autoApply: boolean
  autoMinConfidence: 2 | 3
  autoPostpone: boolean
  extraLittle: ExtraLevel
  extraMedium: ExtraLevel
  extraHigh: ExtraLevel
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
  /** Connections that need attention: the Overview's health line. */
  health: HealthIssue[]
}

/** A connection with an error: what went wrong, and when it last worked. */
export interface HealthIssue {
  name: string
  detail: string
  /** What to do about it. */
  fix?: string
  lastSuccessAt?: string
}

/** A demo service that can be made to fail (demo mode only). */
export interface DemoFault {
  service: 'evcc' | 'homeassistant' | 'calendar' | 'routes'
  name: string
  /** '' while it works; key: it rejects its key (token, address). */
  fault: '' | 'down' | 'key'
  /** The faults it can have (EVCC has no key). */
  faults: ('down' | 'key')[]
}

/** "home", "not_home", or the name of a Home Assistant zone. */
/** One day's saved figures. Prices in EUR/kWh incl. VAT; null is unknown. */
export interface DayStats {
  date: string
  priceMin: number | null
  priceAvg: number | null
  priceMax: number | null
  /** Hours of the day the prices cover (24 when complete). */
  priceHours: number
  distanceKm: number | null
  energyKWh: number | null
  windBft: number | null
  sunshinePct: number | null
  updatedAt: string
}

/** Saved days, oldest first. */
export interface History {
  today: string
  /** The earliest saved day (absent before the first is recorded). */
  first?: string
  days: DayStats[]
}

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

/** Settings → Advice → AI mode: when the AI plan runs, the model, and the prompt. */
export interface AISettings {
  maxRunsPerDay: number
  onPrices: boolean
  onPlugIn: boolean
  /** Empty: the OpenRouter model under Connections. */
  model: string
  prompt: string
  /** What {{goals}} holds; the planner's rules from your settings by default. */
  goals: string
  /** Create plans from the AI's advice automatically, at this confidence or more; postpone only when asked. */
  autoApply: boolean
  autoMinConfidence: 2 | 3
  autoPostpone: boolean
  /** The three above were changed here; until then they follow Automatic rules' switch. */
  autoSet: boolean
}

/** A placeholder such as {{trips}}, filled with the planner's data. */
export interface AIMacro {
  name: string
  description: string
}

export interface AISettingsView {
  settings: AISettings
  defaultPrompt: string
  /** The planner's rules with your settings and EVCC's charge limit. */
  defaultGoals: string
  /** Models offered for AI mode, sorted by name, with prices; others can be typed in. */
  models: AIModelOption[]
  /** When the prices were read from OpenRouter (YYYY-MM-DD). */
  pricesDate: string
  macros: AIMacro[]
  defaultModel: string
  /** How decisions are made; empty without a model. */
  decider: string
  note?: string
}

/** A model offered for AI mode: OpenRouter's USD per million tokens, and a typical run's cost. */
export interface AIModelOption {
  id: string
  inputPerM: number
  outputPerM: number
  runUsd: number
}

export type AITrigger = 'prices' | 'plug_in' | 'manual'

export interface AIDayResult {
  date: string
  recommendation: 'charge_extra' | 'solar' | 'defer' | 'none'
  targetSoc?: number
  from?: string
  until?: string
  deferUntil?: string
  postpone?: boolean
  confidence: number
  summary: string
  rationale: string[]
}

/** A checked AI decision. */
export interface AIResult {
  summary: string
  important: string[]
  ahead?: { targetSoc: number; tripIds: string[]; reason: string }
  days: AIDayResult[]
  /** What the planner corrected or left out. */
  warnings: string[]
}

export interface AIRunView {
  id: number
  startedAt: string
  finishedAt: string
  trigger: AITrigger
  status: 'ok' | 'failed'
  error?: string
  decider: string
  model: string
  promptTokens: number
  completionTokens: number
  result: AIResult | null
}

/** A run with what was sent and what came back. */
export interface AIRun extends Omit<AIRunView, 'result'> {
  prompt?: string
  response?: string
  trips?: Record<string, string>
}

export interface AIView {
  active: boolean
  running: boolean
  decider: string
  note?: string
  runsToday: number
  maxRuns: number
  inUse: boolean
  status: string
  latest: AIRunView | null
  current: AIRunView | null
}

/** A prompt filled with today's data, as it would be sent. */
export interface AIPreview {
  system: string
  text: string
  chars: number
  tokens: number
}

export interface Connection {
  name: string
  mode?: string
  /** rejected: the service refused its key, token, or address. */
  status: 'ok' | 'error' | 'rejected' | 'stub' | 'not_configured' | 'planned'
  detail: string
  /** What to do about an error or a rejected key. */
  fix?: string
  lastSuccessAt?: string
  /** What can be done about it now (Settings → Connections). */
  actions?: ConnectionAction[]
}

/** check it, sync the calendars, fetch the forecasts, send a test
 * notification, make an AI plan, or send the plan to EVCC again. */
export type ConnectionAction = 'check' | 'sync' | 'refresh' | 'test' | 'run' | 'send'

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
  kind: 'battery' | 'range' | 'timestamp' | 'efficiency'
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
  /** Plug in for the sun: today's advice is to charge extra from solar. */
  solar: boolean
  /** Plug in for charge extra's plan, and a message when one is created automatically. */
  extra: boolean
  /** Plug in when the car gets back home from the day's last trip and still needs charging. */
  arrival: boolean
  /** Automatic and AI mode: new prices changed the plan (or brought charge extra) while unplugged. */
  prices: boolean
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
  | 'extra'
  | 'extra_plan'
  | 'arrived'
  | 'prices'
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
  /** retrying: the send failed and is tried again at retryAt. */
  state: 'sent' | 'snoozed' | 'retrying' | 'done' | 'failed'
  note?: string
  latest?: string
  createdAt: string
  sentAt?: string
  snoozedUntil?: string
  resolvedAt?: string
  tries?: number
  retryAt?: string
}
