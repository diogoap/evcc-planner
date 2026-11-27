<script setup lang="ts">
import { computed, ref } from 'vue'
import { RouterLink } from 'vue-router'
import type { Plan, SendOption, SoCSource, Status } from '../api/types'
import { historyLink } from '../lib/activity'
import { heatingLine, temp } from '../lib/heating'
import { ago, clock, dateTime, duration, km, kw, kwh, localDate, pct, price } from '../lib/format'
import {
  aheadText,
  blockLabel,
  chargesAhead,
  climateLabel,
  controlLabel,
  locationLabel,
  readyHeadline,
} from '../lib/status'
import { runAIPlan } from '../store'
import AppIcon from './AppIcon.vue'
import SendPlanButton from './SendPlanButton.vue'

// The ready check: will the car be ready for its next departure? The SoC and
// range, the next departure and what it needs, what the planner does about
// it, and the energy now. Where each SoC reading comes from is under
// Settings → Connections (and in the SoC's tooltip).
const props = defineProps<{ plan: Plan; status: Status | null; canSend: boolean }>()
const emit = defineEmits<{ send: [option?: SendOption]; resume: []; cancel: [id: number] }>()

const sourceName: Record<SoCSource, string> = { evcc: 'EVCC', homeassistant: 'Home Assistant' }

const soc = computed(() => props.plan.soc)
const next = computed(() => props.plan.next)
// Car heating: the next departure's decision, or when it would heat.
const heating = computed(() =>
  heatingLine(props.status?.heating, props.plan.next, props.status?.connected ?? false),
)
const fromHA = computed(() => soc.value.source === 'homeassistant')
// Each source's reading and its age, for the tooltip.
const sources = computed(() =>
  [
    soc.value.source && soc.value.observedAt
      ? `${sourceName[soc.value.source]}: ${pct(soc.value.percent)} · ${ago(soc.value.observedAt, props.plan.now)}`
      : '',
    ...soc.value.other.map(
      (o) => `${sourceName[o.source]}: ${pct(o.percent)} · ${ago(o.observedAt, props.plan.now)}`,
    ),
  ]
    .filter(Boolean)
    .join('\n'),
)

// Unplugged, plugged in, or charging.
const connection = computed(() => {
  const s = props.status
  if (s?.charging) return { text: `Charging ${kw(s.chargePowerW)}`, tone: 'ok', icon: 'bolt' as const }
  if (s?.connected) return { text: 'Plugged in', tone: 'info', icon: 'plug' as const }
  return { text: 'Unplugged', tone: 'muted', icon: 'plug' as const }
})
const location = computed(() => (props.status?.location ? locationLabel(props.status.location) : null))
const climate = computed(() => (props.status?.climate ? climateLabel(props.status.climate) : null))
// The climate badge's tooltip: mode, target, and inside temperature.
const climateTitle = computed(() => {
  const c = props.status?.climate
  if (!c) return ''
  const parts = [`Car climate from Home Assistant: ${c.mode}`]
  if (c.mode !== 'off' && c.targetC != null) parts.push(`set to ${temp(c.targetC)}`)
  if (c.currentC != null) parts.push(`${temp(c.currentC)} inside`)
  return `${parts.join(', ')}; since ${dateTime(c.since)}`
})

const headline = computed(() => readyHeadline(next.value, props.status, props.plan.now))
// "School drop-off, at School by 08:15 · then Office visit"
const tripLine = computed(() => {
  const b = next.value
  if (!b) return ''
  const later = b.titles.slice(1)
  return `${b.firstTitle}, at ${b.firstDestination} by ${clock(b.firstArriveAt)}${later.length ? ` · then ${later.join(', ')}` : ''}`
})
// What charging the departure still needs, or why it can't make it.
const chargeLine = computed(() => {
  const b = next.value
  if (!b) return null
  if (b.status === 'charge')
    return { text: `${kwh(b.chargeKWh)} to charge, about ${duration(b.chargeMinutes)}`, tone: 'muted' }
  const label = blockLabel(b)
  return label.tone === 'bad' ? { text: label.text, tone: 'bad' } : null
})

const control = computed(() => controlLabel(props.plan.control, !!props.status?.connected))
// Later trips the target charges ahead (the rules', or the AI's).
const ahead = computed(() =>
  chargesAhead(props.plan.control.mode) && !props.plan.control.paused
    ? (props.plan.control.target?.ahead ?? [])
    : [],
)
const aiTarget = computed(() => props.plan.control.target?.source === 'ai' && !props.plan.control.paused)
const aiRunning = ref(false)
async function runAI() {
  aiRunning.value = true
  await runAIPlan()
  aiRunning.value = false
}
const adjustments = computed(() => props.plan.advice.adjustments.filter((a) => a.status === 'active'))
</script>

<template>
  <section class="card ready" aria-labelledby="ready-title" data-testid="ready-card">
    <div class="card-title">
      <span id="ready-title">{{ status?.vehicleTitle || 'Vehicle' }}</span>
      <span class="row gap-1">
        <span
          v-if="climate"
          class="pill normal-case"
          :class="`tone-${climate.tone}`"
          :title="climateTitle"
          data-testid="climate"
          ><AppIcon :name="climate.icon" />{{ climate.text }}</span
        >
        <span
          v-if="location"
          class="pill normal-case"
          :class="`tone-${location.tone}`"
          :title="`Car location from Home Assistant${status?.location ? ` since ${dateTime(status.location.since)}` : ''}`"
          data-testid="location"
          ><AppIcon :name="location.icon" />{{ location.text }}</span
        >
        <span class="pill normal-case" :class="`tone-${connection.tone}`" data-testid="connection"
          ><AppIcon :name="connection.icon" />{{ connection.text }}</span
        >
      </span>
    </div>

    <template v-if="soc.known">
      <div class="row items-baseline" :title="sources">
        <span class="big" data-testid="soc">{{ pct(soc.percent) }}</span>
        <span v-if="soc.rangeKm != null" class="muted" data-testid="soc-range"
          >{{ km(soc.rangeKm) }} range</span
        >
        <span v-else class="muted small" data-testid="soc-range">Range: not reported</span>
        <span v-if="soc.estimated && soc.projected != null" class="muted small">
          ≈ {{ pct(soc.projected) }} after trips since
        </span>
      </div>
      <div class="bar" role="img" :aria-label="`State of charge ${pct(soc.percent)}`">
        <div class="bar-fill" :class="{ stale: soc.stale }" :style="{ width: `${soc.percent ?? 0}%` }" />
        <div
          v-if="next"
          class="bar-target"
          :style="{ left: `${Math.min(next.targetSoc, 100)}%` }"
          :title="`Target ${next.targetSoc}%`"
        />
      </div>
      <p v-if="soc.stale" class="small text-warn m-0" data-testid="soc-stale">
        <AppIcon name="info" />
        Last known, {{ soc.observedAt ? dateTime(soc.observedAt) : '' }}, from
        {{ fromHA ? 'Home Assistant' : 'EVCC' }}
      </p>
    </template>
    <template v-else>
      <div class="big muted">—</div>
      <p class="small text-warn m-0">SoC unknown. Plug in the car so EVCC can read it.</p>
    </template>

    <div class="ready-next">
      <div class="ready-headline">
        <span class="ready-icon" :class="`tone-${headline.tone}`"><AppIcon :name="headline.icon" /></span>
        <p class="text-lg strong m-0 grow" data-testid="ready-headline">{{ headline.text }}</p>
        <span v-if="next" class="pill normal-case" data-testid="next-needs"
          >needs <span class="num" data-testid="next-target">{{ next.targetSoc }}%</span></span
        >
      </div>
      <template v-if="next">
        <p class="small muted m-0" data-testid="next-trips">{{ tripLine }}</p>
        <p v-if="chargeLine" class="small m-0" :class="`text-${chargeLine.tone}`" data-testid="next-charge">
          {{ chargeLine.text }}
        </p>
      </template>
    </div>

    <dl class="kv kv-left small ready-plan">
      <dt>Trip plans</dt>
      <dd>
        <span data-testid="trip-plans">{{ control.mode }}</span>
        <div v-if="control.detail" :class="`text-${control.tone}`" data-testid="control-detail">
          {{ control.detail }}
        </div>
        <ul v-if="ahead.length" class="muted list-indent mt-1" data-testid="ahead">
          <li v-for="a in ahead" :key="a.tripKey">{{ aheadText(a, aiTarget) }}</li>
        </ul>
        <div v-if="aiTarget && plan.control.target?.reason" class="muted mt-1" data-testid="ai-reason">
          Why: {{ plan.control.target.reason }}
        </div>
        <button
          v-if="plan.control.paused"
          class="btn sm mt-2"
          type="button"
          data-testid="resume"
          @click="emit('resume')"
        >
          Resume
        </button>
      </dd>
      <template v-if="plan.ai">
        <dt>AI plan</dt>
        <!-- The target's reason is under Trip plans and each day's under its
             advice; the week's summary is under Settings → Advice → AI mode. -->
        <dd data-testid="ai-plan">
          <!-- A note beside an AI plan in use is information, not a warning. -->
          <span
            v-if="plan.ai.note"
            :class="['block', { 'text-warn': !plan.ai.inUse }]"
            data-testid="ai-plan-note"
            >{{ plan.ai.note }}</span
          >
          <span class="row gap-2">
            <span v-if="plan.ai.inUse && plan.ai.runAt" class="muted"
              >Made {{ dateTime(plan.ai.runAt) }}</span
            >
            <button
              v-if="plan.ai.canRun"
              class="btn sm"
              type="button"
              :disabled="aiRunning || plan.ai.running"
              data-testid="ai-run-overview"
              @click="runAI"
            >
              {{ aiRunning || plan.ai.running ? 'Running…' : 'Run AI now' }}
            </button>
          </span>
        </dd>
      </template>
      <dt>EVCC plan</dt>
      <dd data-testid="evcc-plan">
        <template v-if="status?.plan">
          {{ Math.round(status.plan.soc) }}% by {{ dateTime(status.plan.time) }}
          <span class="muted">· {{ status.plan.setByPlanner ? 'Set by planner' : 'Set in EVCC' }}</span>
        </template>
        <template v-else>None</template>
      </dd>
      <template v-for="a in adjustments" :key="a.id">
        <dt>{{ a.kind === 'extra_plan' ? 'Extra plan' : 'Postponed' }}</dt>
        <dd data-testid="active-adjustment">
          <template v-if="a.kind === 'extra_plan' && a.plan"
            >{{ a.plan.soc }}% by {{ dateTime(a.plan.time) }}</template
          >
          <template v-else>Cheap charging paused</template>
          <span class="muted"> · restores {{ dateTime(a.expiresAt) }}</span>
          <button class="btn sm ghost" type="button" @click="emit('cancel', a.id)">End now</button>
        </dd>
      </template>
      <template v-if="plan.advice.autoApply">
        <dt>Advice</dt>
        <dd data-testid="auto-apply">Plans created automatically</dd>
      </template>
      <template v-if="heating">
        <dt>Heating</dt>
        <dd
          data-testid="heating"
          :class="{ 'text-bad': heating.tone === 'bad', 'text-warn': heating.tone === 'warn' }"
        >
          {{ heating.text }}
        </dd>
      </template>
      <dt>Energy now</dt>
      <dd data-testid="energy-now">
        {{ price(status?.gridPrice) }} incl. VAT · solar {{ kw(status?.pvPowerW ?? 0) }}
        <div v-if="status && !status.reachable" class="text-bad">
          EVCC unreachable{{ status.error ? `: ${status.error}` : '' }}. Showing the last known values.
        </div>
      </dd>
    </dl>

    <div class="row ready-actions">
      <template v-if="next">
        <SendPlanButton
          :options="plan.sendOptions"
          :now="plan.now"
          :disabled="!canSend"
          @send="(o?: SendOption) => emit('send', o)"
        />
        <RouterLink class="btn" :to="{ path: '/trips', hash: `#day-${localDate(next.firstArriveAt)}` }"
          >View trips</RouterLink
        >
      </template>
      <RouterLink v-else class="btn" to="/trips">Add a trip</RouterLink>
      <RouterLink
        class="btn history-link"
        :to="historyLink('plan', 'the EVCC plan')"
        title="Plan history"
        data-testid="plan-history"
        ><AppIcon name="clock" class="history-icon" /><span class="history-label"
          >Plan history</span
        ></RouterLink
      >
    </div>
  </section>
</template>

<style scoped>
.ready {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

/* The car's name stays on one line; the badges (climate, location,
   connection) move below it when they don't fit beside it. */
.ready .card-title {
  margin-bottom: 0;
  flex-wrap: wrap;
  row-gap: var(--space-2);
}

.ready .card-title > :first-child {
  white-space: nowrap;
}

.ready-next {
  display: grid;
  gap: 2px;
  padding-top: var(--space-3);
  border-top: 1px solid var(--border);
}

.ready-headline {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

/* The status at a glance: a coloured round icon before the headline. */
.ready-icon {
  display: grid;
  place-items: center;
  width: 28px;
  height: 28px;
  border-radius: 50%;
  flex: none;
}

.ready-icon .icon {
  width: 16px;
  height: 16px;
  stroke-width: 2.5;
}

.ready-plan {
  padding-top: var(--space-3);
  border-top: 1px solid var(--border);
}

/* With the card stretched beside the advice, the buttons stay at the bottom. */
.ready-actions {
  margin-top: auto;
}

/* Phones: Plan history as an icon, so the buttons fit on one line. */
.history-icon {
  display: none;
}

@media (max-width: 640px) {
  .ready-actions {
    gap: 6px;
  }

  .history-icon {
    display: inline;
  }

  .history-label {
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip: rect(0 0 0 0);
  }
}
</style>
