<script setup lang="ts">
import { computed } from 'vue'
import type { AdviceSource, Day, DayAdvice } from '../api/types'
import {
  adviceLabel,
  basisNames,
  confidenceLabel,
  indicatorNames,
  sourceTone,
  sunSourceNames,
  sunText,
  tempText,
  windText,
} from '../lib/advice'
import { ago, dateTime, dayLong, dayShort } from '../lib/format'

const props = defineProps<{
  advice: DayAdvice
  day?: Day
  sources: AdviceSource[]
  now: string
  dates: string[]
}>()
const emit = defineEmits<{
  select: [date: string]
  propose: [kind: 'extra_plan' | 'postpone']
  dismiss: [undo: boolean]
  cancel: [id: number]
}>()

const label = computed(() => adviceLabel(props.advice, props.day))
const weekday = computed(() => dayLong(props.advice.date).split(' ')[0])
const index = computed(() => props.dates.indexOf(props.advice.date))
const canDismiss = computed(
  () => !props.advice.noSuggestion && props.advice.recommendation !== 'none' && !props.advice.adjustment,
)
const adj = computed(() => props.advice.adjustment)
</script>

<template>
  <section class="card" aria-labelledby="advice-title" data-testid="advice-card">
    <div id="advice-title" class="card-title">
      <span>{{ weekday }}'s advice</span>
      <span class="row" style="gap: 4px">
        <button
          class="btn sm ghost"
          type="button"
          aria-label="Previous day"
          :disabled="index <= 0"
          @click="emit('select', dates[index - 1]!)"
        >
          ‹
        </button>
        <span class="small num" style="text-transform: none; letter-spacing: 0">{{
          dayShort(advice.date)
        }}</span>
        <button
          class="btn sm ghost"
          type="button"
          aria-label="Next day"
          :disabled="index >= dates.length - 1"
          @click="emit('select', dates[index + 1]!)"
        >
          ›
        </button>
      </span>
    </div>

    <div class="row" style="justify-content: space-between; align-items: baseline">
      <strong style="font-size: 1.2rem" data-testid="advice-label">{{ label.text }}</strong>
      <span v-if="advice.indicator && !advice.noSuggestion" class="small" data-testid="advice-indicator">
        Charge indicator: <strong>{{ indicatorNames[advice.indicator] }}</strong>
      </span>
    </div>
    <div v-if="advice.confidence" class="small muted" data-testid="advice-confidence">
      Confidence: {{ confidenceLabel(advice.confidence) }}
    </div>
    <p style="margin: 10px 0">{{ advice.summary }}</p>

    <dl class="kv small">
      <template v-if="advice.price">
        <dt>Price</dt>
        <dd>
          €{{ advice.price.value.toFixed(3) }}/kWh avg incl. VAT
          <span
            class="pill"
            :class="advice.price.basis === 'published' ? 'tone-ok' : 'tone-warn'"
            data-testid="advice-basis"
            >{{ basisNames[advice.price.basis] }}{{ advice.price.stale ? ' · stale' : '' }}</span
          >
        </dd>
      </template>
      <dt>Sun</dt>
      <dd data-testid="advice-sun">
        <template v-if="advice.sunSource"
          >{{ sunText(advice) }} <span class="muted">({{ sunSourceNames[advice.sunSource] }})</span></template
        >
        <span v-else class="muted">No forecast for this day</span>
      </dd>
      <template v-if="windText(advice)">
        <dt>Wind</dt>
        <dd data-testid="advice-wind">{{ windText(advice) }}</dd>
      </template>
      <template v-if="tempText(advice)">
        <dt>Temperature</dt>
        <dd>{{ tempText(advice) }}</dd>
      </template>
    </dl>

    <div v-if="adj" class="banner tone-info small" style="margin: 12px 0 0" data-testid="advice-adjustment">
      <template v-if="adj.kind === 'extra_plan'">
        Extra plan active{{ adj.source === 'auto' ? ' (automatic)' : '' }}: {{ adj.plan?.soc }}% by
        {{ dateTime(adj.plan!.time) }}.
      </template>
      <template v-else>Cheap-grid charging paused{{ adj.source === 'auto' ? ' (automatic)' : '' }}.</template>
      Restores the previous setting {{ dateTime(adj.expiresAt) }}, unless you change it in EVCC.
      <div v-if="adj.note && adj.note.startsWith('Restoring failed')" class="text-bad">{{ adj.note }}</div>
      <div style="margin-top: 6px">
        <button class="btn sm" type="button" @click="emit('cancel', adj.id)">End now and restore</button>
      </div>
    </div>
    <p v-else-if="advice.dismissed" class="small muted" style="margin: 12px 0 0">
      Dismissed; nothing was changed.
      <button class="btn sm ghost" type="button" @click="emit('dismiss', true)">Undo</button>
    </p>
    <p
      v-else-if="!advice.noSuggestion && advice.recommendation !== 'none' && advice.confidence < 2"
      class="small muted"
      style="margin: 12px 0 0"
    >
      Low confidence: no plan action.
    </p>

    <div v-if="!adj && !advice.dismissed" class="row" style="margin-top: 12px">
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

    <details style="margin-top: 12px">
      <summary class="small">Why?</summary>
      <ul class="small" style="margin: 8px 0; padding-left: 18px">
        <li v-for="r in advice.rationale" :key="r">{{ r }}</li>
        <li v-if="advice.outlookNote">Outlook: {{ advice.outlookNote }}</li>
        <li v-if="advice.price">Price basis: {{ advice.price.detail }}.</li>
      </ul>
      <div class="small muted" style="margin-bottom: 4px">Sources</div>
      <ul class="small" style="margin: 0; padding: 0; list-style: none" data-testid="advice-sources">
        <li v-for="s in sources" :key="s.name" class="row" style="padding: 2px 0">
          <span class="pill" :class="`tone-${sourceTone[s.status]}`">{{ s.status }}</span>
          <strong>{{ s.name }}</strong>
          <span class="muted"
            >{{ s.detail }}<template v-if="s.updatedAt"> · {{ ago(s.updatedAt, now) }}</template></span
          >
        </li>
      </ul>
      <p class="small muted" style="margin: 8px 0 0">
        Advice is qualitative: the charge indicator does not prescribe an amount, and trip targets always stay
        protected.
      </p>
    </details>
  </section>
</template>
