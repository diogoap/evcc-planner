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
import { ago, clock, dateTime, dayLong, dayShort } from '../lib/format'
import AppIcon from './AppIcon.vue'

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
// "€0.510/kWh avg incl. VAT · lowest €0.370 at 03:45" (lowest: published days).
const priceText = computed(() => {
  const p = props.advice.price
  if (!p) return ''
  let text = `€${p.value.toFixed(3)}/kWh avg incl. VAT`
  if (p.min != null) text += ` · lowest €${p.min.toFixed(3)}${p.minAt ? ` at ${clock(p.minAt)}` : ''}`
  return text
})
</script>

<template>
  <section class="card" aria-labelledby="advice-title" data-testid="advice-card">
    <div id="advice-title" class="card-title">
      <span>{{ weekday }}'s advice</span>
      <span class="row gap-1">
        <button
          class="btn sm ghost icon-only"
          type="button"
          aria-label="Previous day"
          :disabled="index <= 0"
          @click="emit('select', dates[index - 1]!)"
        >
          <AppIcon name="chevron-left" />
        </button>
        <span class="small num normal-case">{{ dayShort(advice.date) }}</span>
        <button
          class="btn sm ghost icon-only"
          type="button"
          aria-label="Next day"
          :disabled="index >= dates.length - 1"
          @click="emit('select', dates[index + 1]!)"
        >
          <AppIcon name="chevron-right" />
        </button>
      </span>
    </div>

    <div class="row justify-between items-baseline">
      <strong class="text-lg" data-testid="advice-label">{{ label.text }}</strong>
      <span v-if="advice.indicator && !advice.noSuggestion" class="small" data-testid="advice-indicator">
        Charge indicator: <strong>{{ indicatorNames[advice.indicator] }}</strong>
      </span>
    </div>
    <div v-if="advice.confidence" class="small muted" data-testid="advice-confidence">
      Confidence: {{ confidenceLabel(advice.confidence) }}
    </div>
    <p class="my-3">{{ advice.summary }}</p>

    <dl class="kv small">
      <template v-if="advice.price">
        <dt>Price</dt>
        <dd>
          {{ priceText }}
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

    <div v-if="adj" class="banner tone-info small m-0 mt-3" data-testid="advice-adjustment">
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
    <p v-else-if="advice.dismissed" class="small muted m-0 mt-3">
      Dismissed; nothing was changed.
      <button class="btn sm ghost" type="button" @click="emit('dismiss', true)">Undo</button>
    </p>
    <p
      v-else-if="!advice.noSuggestion && advice.recommendation !== 'none' && advice.confidence < 2"
      class="small muted m-0 mt-3"
    >
      Low confidence: no plan action.
    </p>

    <div v-if="!adj && !advice.dismissed" class="row mt-3">
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

    <details class="mt-3">
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
      <p class="small muted m-0 mt-2">
        Advice is qualitative: the charge indicator does not prescribe an amount, and trip targets always stay
        protected.
      </p>
    </details>
  </section>
</template>
