<script setup lang="ts">
import { computed, ref } from 'vue'
import { RouterLink } from 'vue-router'
import type { AdjustmentKind, Plan, Rate, SendOption, Status, Trip } from '../api/types'
import { useDialog } from '../lib/dialog'
import { clock, dayLong, km, kwh, localDate, price } from '../lib/format'
import { chargingWindows, dayRange } from '../lib/planchart'
import { blockLabel, dayLabel, dayName, departureTarget, reviewText, sendOptionLabel } from '../lib/status'
import { confirmTrip, deleteTrip, resetTrip, setTripEnabled } from '../store'
import AdviceCard from './AdviceCard.vue'
import AppIcon from './AppIcon.vue'
import PlanChart from './PlanChart.vue'
import TripRow from './TripRow.vue'

// One day in detail, over the Overview: a sheet from the bottom on phones, a
// panel at the side on wide screens. The day's chart, its trips with on/off
// and edit, the charge it needs and when the planner expects it to charge,
// the advice with prices and weather, and quick actions. Arrows (or a swipe
// on phones) move between days; Escape, the close button, or the back
// gesture closes it.
const props = defineProps<{
  date: string
  plan: Plan
  status: Status | null
  /** The planner's expected charging slots for the automatic target. */
  charging: Rate[]
  canSend: boolean
}>()
const emit = defineEmits<{
  close: []
  select: [date: string]
  add: [date: string]
  edit: [trip: Trip]
  history: [trip: Trip]
  send: [option: SendOption]
  propose: [kind: AdjustmentKind, date: string]
  dismiss: [date: string, undo: boolean]
  cancel: [id: number]
}>()

const root = ref<HTMLElement | null>(null)
useDialog(root, () => emit('close'), '[data-autofocus]')

const dates = computed(() => props.plan.days.map((d) => d.date))
const index = computed(() => dates.value.indexOf(props.date))
const prev = computed(() => dates.value[index.value - 1])
const next = computed(() => dates.value[index.value + 1])

const day = computed(() => props.plan.days.find((d) => d.date === props.date))
const label = computed(() => (day.value ? dayLabel(day.value) : null))
const relative = computed(() => {
  const name = dayName(props.date, props.plan.now)
  return name === 'today' || name === 'tomorrow' ? name.charAt(0).toUpperCase() + name.slice(1) : ''
})
const range = computed(() => dayRange(props.date))
const trips = computed(() => props.plan.trips.filter((t) => t.date === props.date))
const advice = computed(() =>
  props.plan.advice.enabled ? props.plan.advice.days.find((a) => a.date === props.date) : undefined,
)

// What each departure that day needs, and when the planner expects to charge.
const departures = computed(() =>
  props.plan.blocks
    .filter((b) => localDate(b.departAt) === props.date && b.status !== 'done' && b.status !== 'in_progress')
    .map((b) => ({ block: b, label: blockLabel(b) })),
)
const windows = computed(() => chargingWindows(props.charging, range.value.start, range.value.end))
// "03:00–03:15, 03:45–04:00 at about €0.16/kWh" (time-weighted).
const windowText = computed(() => {
  const ws = windows.value
  const hours = ws.reduce((sum, w) => sum + (w.end - w.start), 0)
  const avg = ws.reduce((sum, w) => sum + w.price * (w.end - w.start), 0) / (hours || 1)
  return `${ws.map((w) => `${clock(w.start)}–${clock(w.end)}`).join(', ')} at about ${price(avg)}`
})

// A plan covering the trips through this day, when one exists.
const sendOption = computed(() => props.plan.sendOptions.find((o) => o.through === props.date))

// A horizontal swipe moves between days, except on the chart (its readout).
let touch: { x: number; y: number } | null = null
function onTouchStart(e: TouchEvent) {
  const t = e.touches[0]
  const target = e.target instanceof Element ? e.target : null
  touch =
    t && e.touches.length === 1 && !target?.closest('canvas, input, select')
      ? { x: t.clientX, y: t.clientY }
      : null
}
function onTouchEnd(e: TouchEvent) {
  const t = e.changedTouches[0]
  if (!touch || !t) return
  const [dx, dy] = [t.clientX - touch.x, t.clientY - touch.y]
  touch = null
  if (Math.abs(dx) < 60 || Math.abs(dx) < Math.abs(dy) * 1.5) return
  const to = dx < 0 ? next.value : prev.value
  if (to) emit('select', to)
}
</script>

<template>
  <div class="panel-backdrop" @mousedown.self="emit('close')">
    <aside
      ref="root"
      class="day-panel"
      role="dialog"
      aria-modal="true"
      aria-labelledby="day-panel-title"
      data-testid="day-panel"
      @touchstart.passive="onTouchStart"
      @touchend="onTouchEnd"
    >
      <header class="dp-head">
        <button
          class="btn ghost icon-only"
          type="button"
          aria-label="Previous day"
          :disabled="!prev"
          @click="prev && emit('select', prev)"
        >
          <AppIcon name="chevron-left" />
        </button>
        <div class="dp-title">
          <h2 id="day-panel-title" tabindex="-1" data-autofocus>{{ dayLong(date) }}</h2>
          <span v-if="relative" class="small muted">{{ relative }}</span>
        </div>
        <button
          class="btn ghost icon-only"
          type="button"
          aria-label="Next day"
          :disabled="!next"
          @click="next && emit('select', next)"
        >
          <AppIcon name="chevron-right" />
        </button>
        <button class="btn ghost icon-only" type="button" aria-label="Close" @click="emit('close')">
          <AppIcon name="close" />
        </button>
      </header>

      <div class="dp-body">
        <div v-if="day && label" class="dp-summary" data-testid="day-summary">
          <span class="dp-figure num"><AppIcon name="route" />{{ km(day.distanceKm) }}</span>
          <span class="dp-figure num"><AppIcon name="bolt" />{{ kwh(day.energyKWh) }}</span>
          <span class="pill" :class="`tone-${label.tone}`">{{ label.text }}</span>
          <span v-if="label.detail" class="dp-figure small muted num"
            ><AppIcon name="battery" />{{ label.detail }}</span
          >
          <span v-if="day.reviewCount" class="pill tone-warn">{{ reviewText(day.reviewCount) }}</span>
        </div>

        <PlanChart
          v-if="plan.timeline.length || status?.priceSlots.length"
          compact
          :timeline="plan.timeline"
          :blocks="plan.blocks"
          :slots="status?.priceSlots ?? []"
          :solar="status?.solar ?? []"
          :charging="charging"
          :advice="plan.advice.enabled ? plan.advice.days : []"
          :dates="[date]"
          :reserve="plan.settings.arrivalReservePct"
          :now="plan.now"
          :from="new Date(range.start).toISOString()"
          :to="new Date(range.end).toISOString()"
        />

        <section aria-labelledby="dp-trips">
          <h3 id="dp-trips" class="dp-section">Trips</h3>
          <ul v-if="trips.length" class="list-plain">
            <TripRow
              v-for="t in trips"
              :key="t.key"
              :trip="t"
              :target="departureTarget(plan, t)"
              @toggle="setTripEnabled(t, $event)"
              @edit="emit('edit', t)"
              @review="confirmTrip(t)"
              @reset="resetTrip(t)"
              @remove="deleteTrip(t)"
              @history="emit('history', t)"
            />
          </ul>
          <p v-else class="small muted m-0">{{ day?.doneCount ? 'No more trips.' : 'No trips this day.' }}</p>
        </section>

        <section
          v-if="departures.length || windows.length"
          aria-labelledby="dp-charging"
          data-testid="day-charging"
        >
          <h3 id="dp-charging" class="dp-section">Charging</h3>
          <p v-for="d in departures" :key="d.block.index" class="small m-0">
            <strong class="num">{{ clock(d.block.departAt) }}</strong> needs {{ d.block.targetSoc }}%:
            <span :class="{ 'text-bad': d.label.tone === 'bad' }">{{ d.label.text }}</span>
          </p>
          <p v-if="windows.length" class="small m-0" data-testid="day-windows">
            Expected {{ windowText }}
            <span class="muted">(the planner's estimate; EVCC picks the slots)</span>
          </p>
        </section>

        <section v-if="advice" aria-labelledby="dp-advice">
          <h3 id="dp-advice" class="dp-section">Advice</h3>
          <AdviceCard
            bare
            :advice="advice"
            :day="day"
            :sources="plan.advice.sources"
            :now="plan.now"
            :dates="dates"
            @propose="(k) => emit('propose', k, date)"
            @dismiss="(undo) => emit('dismiss', date, undo)"
            @cancel="(id) => emit('cancel', id)"
          />
        </section>

        <div class="row dp-actions">
          <button class="btn" type="button" data-testid="day-add" @click="emit('add', date)">
            <AppIcon name="plus" />Add trip
          </button>
          <button
            v-if="sendOption && plan.next"
            class="btn"
            type="button"
            :disabled="!canSend"
            data-testid="day-send"
            @click="emit('send', sendOption)"
          >
            Send plan: {{ sendOptionLabel(sendOption, plan.now) }}
          </button>
          <RouterLink class="btn" :to="{ path: '/trips', hash: `#day-${date}` }">Open in Trips</RouterLink>
        </div>
      </div>
    </aside>
  </div>
</template>

<style scoped>
.panel-backdrop {
  position: fixed;
  inset: 0;
  z-index: 50;
  background: var(--overlay);
  display: flex;
  justify-content: flex-end;
}

.day-panel {
  display: flex;
  flex-direction: column;
  width: min(480px, 100%);
  height: 100%;
  background: var(--surface);
  border-left: 1px solid var(--border);
  box-shadow: var(--shadow-lg);
  animation: panel-in 0.2s ease-out;
}

@keyframes panel-in {
  from {
    transform: translateX(100%);
  }
}

.dp-head {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  padding: calc(var(--space-3) + env(safe-area-inset-top)) var(--edge-right) var(--space-3) var(--space-3);
  border-bottom: 1px solid var(--border);
}

.dp-title {
  flex: 1;
  min-width: 0;
  display: grid;
  text-align: center;
}

.dp-title h2 {
  font-size: var(--text-lg);
}

/* Focus lands on the title so screen readers announce the day; it is not
   a control, so it shows no focus ring. */
.dp-title h2:focus {
  outline: none;
}

.dp-body {
  flex: 1;
  overflow-y: auto;
  overscroll-behavior: contain;
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
  padding: var(--space-4) var(--edge-right) calc(var(--space-5) + env(safe-area-inset-bottom)) var(--space-4);
}

.dp-summary {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2) var(--space-3);
}

.dp-figure {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
}

.dp-figure .icon {
  color: var(--muted);
}

.dp-section {
  margin: 0 0 var(--space-2);
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  letter-spacing: 0.08em;
  text-transform: uppercase;
  color: var(--muted);
}

section {
  display: grid;
  gap: var(--space-1);
}

.dp-actions {
  padding-top: var(--space-3);
  border-top: 1px solid var(--border);
}

/* Phones: a sheet from the bottom, below the status bar. */
@media (max-width: 640px) {
  .panel-backdrop {
    align-items: flex-end;
  }

  .day-panel {
    width: 100%;
    height: auto;
    max-height: calc(100dvh - env(safe-area-inset-top) - var(--space-6));
    border-left: 0;
    border-radius: var(--radius) var(--radius) 0 0;
    animation-name: sheet-up;
  }

  .dp-head {
    padding-top: var(--space-3);
  }

  .dp-body {
    padding-left: var(--edge-left);
  }
}
</style>
