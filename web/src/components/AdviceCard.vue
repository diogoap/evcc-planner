<script setup lang="ts">
import { computed } from 'vue'
import type { AdviceSource, Day, DayAdvice, Target } from '../api/types'
import {
  aheadDays,
  adviceLabel,
  basisNames,
  confidenceNames,
  indicationNames,
  priceFact,
  referenceText,
  sourceTone,
  sunFact,
  tempText,
  windFact,
} from '../lib/advice'
import { ago, clock, dateTime, dayLong, dayShort } from '../lib/format'
import AppIcon from './AppIcon.vue'

// bare: inside the day panel, without the card and its day navigation.
const props = defineProps<{
  advice: DayAdvice
  day?: Day
  sources: AdviceSource[]
  now: string
  dates: string[]
  bare?: boolean
  /** The trip plan's target, for a day it charges later trips ahead. */
  aheadTarget?: Target | null
}>()
const emit = defineEmits<{
  select: [date: string]
  // The date between the arrows opens the day.
  open: [date: string]
  propose: [kind: 'extra_plan' | 'postpone']
  dismiss: [undo: boolean]
  cancel: [id: number]
}>()

const label = computed(() => adviceLabel(props.advice, props.day, props.aheadTarget))
// The trip plan charges later trips ahead on this day.
const ahead = computed(() =>
  props.aheadTarget && aheadDays(props.aheadTarget).has(props.advice.date) ? props.aheadTarget : null,
)
const weekday = computed(() => dayLong(props.advice.date).split(' ')[0])
const index = computed(() => props.dates.indexOf(props.advice.date))
const canDismiss = computed(
  () => !props.advice.noSuggestion && props.advice.recommendation !== 'none' && !props.advice.adjustment,
)
const adj = computed(() => props.advice.adjustment)
// The day's facts: price, sun (EVCC's local forecast, else KNMI's national
// outlook), wind, and temperature.
const price = computed(() => priceFact(props.advice))
const sun = computed(() => sunFact(props.advice))
const wind = computed(() => windFact(props.advice))
const temp = computed(() => tempText(props.advice))
// Charge extra's target, and how strongly the coming days look cheaper.
const target = computed(() =>
  props.advice.recommendation === 'charge_extra' && !props.advice.solar && !props.advice.noSuggestion
    ? props.advice.targetSoc
    : undefined,
)
const indication = computed(() =>
  props.advice.indication && !props.advice.noSuggestion ? indicationNames[props.advice.indication] : '',
)
const reference = computed(() => referenceText(props.advice))
</script>

<template>
  <section
    class="advice"
    :class="{ card: !bare }"
    :aria-labelledby="bare ? undefined : 'advice-title'"
    :aria-label="bare ? 'Advice' : undefined"
    :data-testid="bare ? 'panel-advice' : 'advice-card'"
  >
    <div v-if="!bare" id="advice-title" class="card-title">
      <span>{{ weekday }}'s advice</span>
      <span class="row gap-2">
        <button
          class="btn nav-btn"
          type="button"
          aria-label="Previous day"
          :disabled="index <= 0"
          @click="emit('select', dates[index - 1]!)"
        >
          <AppIcon name="chevron-left" />
        </button>
        <button
          class="btn sm ghost num normal-case"
          type="button"
          :aria-label="`Open ${dayShort(advice.date)}`"
          title="Open the day"
          data-testid="advice-open-day"
          @click="emit('open', advice.date)"
        >
          {{ dayShort(advice.date) }}
        </button>
        <button
          class="btn nav-btn"
          type="button"
          aria-label="Next day"
          :disabled="index >= dates.length - 1"
          @click="emit('select', dates[index + 1]!)"
        >
          <AppIcon name="chevron-right" />
        </button>
      </span>
    </div>

    <div class="advice-head">
      <span class="advice-icon" :class="`tone-${label.tone}`"><AppIcon :name="label.icon" /></span>
      <strong class="text-lg" data-testid="advice-label">{{ label.text }}</strong>
    </div>
    <div v-if="advice.confidence || target || indication || advice.ai" class="row gap-1">
      <span
        v-if="advice.ai"
        class="badge tone-info"
        title="Decided by the AI model (AI mode, Settings → Advice → AI mode)"
        data-testid="advice-ai"
        >AI</span
      >
      <span v-if="advice.confidence" class="badge" data-testid="advice-confidence">
        <span
          v-for="i in 3"
          :key="i"
          class="dot"
          :class="{ on: i <= advice.confidence }"
          aria-hidden="true"
        />
        {{ confidenceNames[advice.confidence] }} confidence
      </span>
      <span v-if="target" class="badge num" data-testid="advice-target">
        <AppIcon name="battery" />To {{ target }}%
      </span>
      <span
        v-if="indication"
        class="badge"
        :title="`How strongly the coming days look cheaper (weekly outlook, wind, sun): ${indication.toLowerCase()}`"
        data-testid="advice-indication"
      >
        Cheaper days ahead: {{ indication.toLowerCase() }}
      </span>
      <span v-if="reference" class="badge num" data-testid="advice-reference">{{ reference }}</span>
    </div>
    <p class="m-0">{{ advice.summary }}</p>
    <p v-if="ahead" class="banner tone-info small m-0" data-testid="advice-ahead">
      <AppIcon name="bolt" /> The trip plan charges to {{ ahead.soc }}% by {{ clock(ahead.readyBy) }}, also
      for {{ ahead.ahead.map((t) => t.title).join(' and ') }}. Charge extra would come on top.
    </p>

    <dl class="facts">
      <div v-if="price && advice.price" class="fact">
        <dt><AppIcon name="tag" />Price</dt>
        <dd class="fact-value num">
          <AppIcon v-if="price.lowest" name="arrow-down" label="Lowest" />{{ price.value }}
        </dd>
        <dd v-if="price.detail" class="small muted num">{{ price.detail }}</dd>
        <dd>
          <span
            class="pill"
            :class="advice.price.basis === 'published' ? 'tone-ok' : 'tone-warn'"
            data-testid="advice-basis"
            >{{ basisNames[advice.price.basis] }}{{ advice.price.stale ? ' · stale' : '' }}</span
          >
        </dd>
      </div>
      <div class="fact" data-testid="advice-sun">
        <dt>
          <AppIcon name="sun" />Sun
          <span v-if="sun" class="fact-source">{{ advice.sunSource === 'evcc' ? 'EVCC' : 'KNMI' }}</span>
        </dt>
        <template v-if="sun">
          <dd class="fact-value num">{{ sun.value }}</dd>
          <dd v-if="sun.detail" class="small muted">{{ sun.detail }}</dd>
        </template>
        <dd v-else class="small muted">No forecast for this day</dd>
      </div>
      <div v-if="wind" class="fact" data-testid="advice-wind">
        <dt><AppIcon name="wind" />Wind</dt>
        <dd class="fact-value">{{ wind.value }}</dd>
        <dd v-if="wind.detail" class="small muted">{{ wind.detail }}</dd>
      </div>
      <div v-if="temp" class="fact">
        <dt><AppIcon name="thermometer" />Temperature</dt>
        <dd class="fact-value num">{{ temp }}</dd>
      </div>
    </dl>

    <p v-if="advice.awayHint" class="banner tone-info small m-0" data-testid="advice-away">
      <AppIcon name="route" /> {{ advice.awayHint }}
    </p>
    <p v-if="advice.exportHint" class="banner tone-warn small m-0" data-testid="advice-export">
      <AppIcon name="solar-panel" /> {{ advice.exportHint }}
    </p>

    <div v-if="adj" class="banner tone-info small m-0" data-testid="advice-adjustment">
      <template v-if="adj.kind === 'extra_plan'">
        Extra plan active{{ adj.source === 'auto' ? ' (automatic)' : '' }}: {{ adj.plan?.soc }}% by
        {{ dateTime(adj.plan!.time) }}.
      </template>
      <template v-else>Cheap-grid charging paused{{ adj.source === 'auto' ? ' (automatic)' : '' }}.</template>
      Restores the previous setting {{ dateTime(adj.expiresAt) }}, unless you change it in EVCC.
      <div v-if="adj.note && adj.note.startsWith('Restoring failed')" class="text-bad">{{ adj.note }}</div>
      <div class="mt-2">
        <button class="btn sm" type="button" @click="emit('cancel', adj.id)">End now and restore</button>
      </div>
    </div>
    <p v-else-if="advice.dismissed" class="small muted m-0">
      Dismissed; nothing was changed.
      <button class="btn sm ghost" type="button" @click="emit('dismiss', true)">Undo</button>
    </p>
    <p
      v-else-if="advice.solar && advice.solarFrom && advice.solarUntil"
      class="banner tone-ok small m-0"
      data-testid="advice-solar"
    >
      <AppIcon name="plug" />
      Keep the car plugged in from {{ clock(advice.solarFrom) }} to {{ clock(advice.solarUntil) }}: EVCC
      charges from the sun by itself, about {{ Math.round(advice.solarPct ?? 0) }}% at €{{
        (advice.solarGridPrice ?? advice.solarPrice)?.toFixed(2)
      }}/kWh.
    </p>
    <p
      v-else-if="!advice.noSuggestion && advice.recommendation !== 'none' && advice.confidence < 2"
      class="small muted m-0"
    >
      Low confidence: no plan action.
    </p>
    <p v-else-if="advice.actionNote" class="small muted m-0" data-testid="advice-action-note">
      {{ advice.actionNote }}
    </p>

    <div v-if="!adj && !advice.dismissed && (advice.action || canDismiss)" class="row">
      <button
        v-if="advice.action === 'extra_plan'"
        class="btn primary"
        type="button"
        @click="emit('propose', 'extra_plan')"
      >
        Create extra plan
      </button>
      <button
        v-if="advice.action === 'postpone'"
        class="btn primary"
        type="button"
        @click="emit('propose', 'postpone')"
      >
        Review postponement
      </button>
      <button v-if="canDismiss" class="btn" type="button" @click="emit('dismiss', false)">Dismiss</button>
    </div>

    <details>
      <summary class="small">Why?</summary>
      <ul class="small list-indent my-2">
        <li v-for="r in advice.rationale" :key="r">{{ r }}</li>
        <li v-if="advice.outlookNote">Outlook: {{ advice.outlookNote }}</li>
        <li v-if="advice.price">Price basis: {{ advice.price.detail }}.</li>
      </ul>
      <div class="small muted mb-1">Sources</div>
      <ul class="small list-plain stack gap-1" data-testid="advice-sources">
        <li v-for="s in sources" :key="s.name" class="row">
          <span class="pill" :class="`tone-${sourceTone[s.status]}`">{{ s.status }}</span>
          <strong>{{ s.name }}</strong>
          <span class="muted"
            >{{ s.detail }}<template v-if="s.updatedAt"> · {{ ago(s.updatedAt, now) }}</template></span
          >
        </li>
      </ul>
      <p v-if="advice.ai" class="small muted m-0 mt-2">
        Decided by the AI model (AI mode). Charge extra comes on top of the trips' targets, which always stay
        protected. The prompt and the latest run are under Settings → Advice → AI mode; prices shown are the
        planner's own.
      </p>
      <p v-else class="small muted m-0 mt-2">
        Charge extra comes on top of the trips' targets, which always stay protected. Its steps (how much
        cheaper, to which SoC) are set under Settings → Advice.
      </p>
    </details>
  </section>
</template>

<style scoped>
.advice {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.advice .card-title {
  margin-bottom: 0;
}

.advice-head {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.advice-icon {
  display: grid;
  place-items: center;
  width: 28px;
  height: 28px;
  border-radius: 50%;
  flex: none;
}

.advice-icon .icon {
  width: 16px;
  height: 16px;
}

.dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  border: 1px solid currentColor;
}

.dot.on {
  background: currentColor;
}

/* The day's facts as small panels, two per row. */
.facts {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-2);
  margin: 0;
}

.fact {
  display: grid;
  align-content: start;
  gap: 2px;
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-md);
  background: var(--surface-2);
}

.fact dt {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  font-size: var(--text-xs);
  font-weight: var(--weight-semibold);
  letter-spacing: 0.06em;
  text-transform: uppercase;
  color: var(--muted);
}

.fact dd {
  margin: 0;
}

.fact-value {
  font-size: var(--text-lg);
  font-weight: var(--weight-semibold);
}

.fact-source {
  margin-left: auto;
  letter-spacing: 0;
}

.fact .pill {
  margin-top: 2px;
}
</style>
