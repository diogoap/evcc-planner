<script setup lang="ts">
import { computed } from 'vue'
import type { Day, DayAdvice } from '../api/types'
import {
  adviceLabel,
  basisNames,
  confidenceNames,
  indicatorLevel,
  indicatorNames,
  sunSourceNames,
  sunText,
} from '../lib/advice'
import AppIcon from './AppIcon.vue'

// A day's advice as compact badges for the seven-day grid: recommendation,
// confidence, charge indicator, price and its basis, local sun, and wind.
const props = defineProps<{ advice: DayAdvice; day?: Day }>()

const label = computed(() => adviceLabel(props.advice, props.day))
const level = computed(() =>
  props.advice.indicator && !props.advice.noSuggestion ? indicatorLevel[props.advice.indicator] : 0,
)
const price = computed(() => {
  const p = props.advice.price
  if (!p) return null
  const value = `€${p.value.toFixed(2)}`
  return {
    text: p.basis === 'published' ? value : `~${value}`,
    basis: p.basis,
    title: `${basisNames[p.basis]}: ${p.detail}`,
  }
})
const bft = computed(() => props.advice.weather?.windBft ?? null)
// EVCC's local kWh for today and tomorrow; KNMI's national % after that.
const sun = computed(() => {
  const a = props.advice
  if (a.sunSource === 'evcc' && a.solarKWh != null)
    return { text: `${a.solarKWh.toFixed(1)} kWh`, knmi: false }
  if (a.sunSource === 'knmi' && a.weather?.sunshinePct != null)
    return { text: `${a.weather.sunshinePct}%`, knmi: true }
  return null
})
</script>

<template>
  <span class="badges">
    <span class="pill" :class="`tone-${label.tone}`" :title="advice.summary" data-testid="badge-advice">
      <AppIcon :name="label.icon" />{{ label.short }}
    </span>
    <span
      v-if="advice.confidence"
      class="badge conf"
      :title="`Confidence ${advice.confidence} of 3 (${confidenceNames[advice.confidence]})`"
      data-testid="badge-confidence"
    >
      <span v-for="i in 3" :key="i" class="dot" :class="{ on: i <= advice.confidence }" aria-hidden="true" />
      {{ advice.confidence }}/3
    </span>
    <span
      v-if="level"
      class="badge bars"
      :title="`Charge indicator: ${indicatorNames[advice.indicator!]}`"
      role="img"
      :aria-label="`Charge indicator: ${indicatorNames[advice.indicator!]}`"
      data-testid="badge-indicator"
    >
      <span v-for="i in 3" :key="i" class="bar-step" :class="{ on: i <= level }" />
    </span>
    <span
      v-if="price"
      class="badge"
      :class="`basis-${price.basis}`"
      :title="price.title"
      data-testid="badge-price"
    >
      {{ price.text }}
    </span>
    <span
      v-if="sun"
      class="badge"
      :class="{ 'basis-weekly': sun.knmi }"
      :title="`Sun (${sunSourceNames[advice.sunSource]}): ${sunText(advice)}`"
      data-testid="badge-sun"
    >
      <AppIcon name="sun" :label="sun.knmi ? 'Sunshine (KNMI)' : 'Local solar (EVCC)'" />{{ sun.text }}
    </span>
    <span v-if="bft != null" class="badge" :title="`Wind Bft ${bft} (KNMI)`" data-testid="badge-wind">
      <AppIcon name="wind" label="Wind" />{{ bft }} Bft
    </span>
  </span>
</template>

<style scoped>
.badges {
  display: inline-flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 4px 6px;
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

.conf {
  gap: 2px;
}

.bars {
  align-items: flex-end;
  gap: 2px;
  padding-top: 3px;
  padding-bottom: 3px;
}

.bar-step {
  width: 4px;
  border-radius: 1px;
  background: var(--border);
}

.bar-step:nth-child(1) {
  height: 5px;
}

.bar-step:nth-child(2) {
  height: 8px;
}

.bar-step:nth-child(3) {
  height: 11px;
}

.bar-step.on {
  background: var(--accent-strong);
}
</style>
