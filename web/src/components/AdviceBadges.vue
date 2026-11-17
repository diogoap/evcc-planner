<script setup lang="ts">
import { computed } from 'vue'
import type { Day, DayAdvice } from '../api/types'
import {
  adviceLabel,
  cheapHint,
  confidenceNames,
  indicationNames,
  priceBadge,
  sunny,
  sunSourceNames,
  sunText,
  windy,
} from '../lib/advice'
import AppIcon from './AppIcon.vue'

// A day's advice as compact badges for the seven-day grid: recommendation,
// confidence, charge extra's target, price and its basis, local sun, and
// wind.
// hidePrice and hideWeather leave out the price, or sun and wind, for a list
// that shows them in their own columns.
const props = defineProps<{ advice: DayAdvice; day?: Day; hidePrice?: boolean; hideWeather?: boolean }>()

const label = computed(() => adviceLabel(props.advice, props.day))
const target = computed(() =>
  props.advice.recommendation === 'charge_extra' && !props.advice.solar && !props.advice.noSuggestion
    ? props.advice.targetSoc
    : undefined,
)
const targetTitle = computed(() => {
  const a = props.advice
  const ind = a.indication
    ? ` (${indicationNames[a.indication].toLowerCase()} indication of cheaper days ahead)`
    : ''
  return `Charge extra to ${target.value}%${ind}`
})
const price = computed(() => priceBadge(props.advice))
const bft = computed(() => props.advice.weather?.windBft ?? null)
// EVCC's local kWh for today and tomorrow; KNMI's national % after that,
// highlighted when sunny.
const sun = computed(() => {
  const a = props.advice
  if (a.sunSource === 'evcc' && a.solarKWh != null)
    return { text: `${a.solarKWh.toFixed(1)} kWh`, knmi: false, sunny: false }
  if (a.sunSource === 'knmi' && a.weather?.sunshinePct != null)
    return { text: `${a.weather.sunshinePct}%`, knmi: true, sunny: sunny(a.weather.sunshinePct) }
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
    <span v-if="target" class="badge num" :title="targetTitle" data-testid="badge-target">
      <AppIcon name="battery" />{{ target }}%
    </span>
    <span
      v-if="price && !hidePrice"
      class="badge"
      :class="`basis-${price.basis}`"
      :title="price.title"
      data-testid="badge-price"
    >
      <AppIcon :name="price.lowest ? 'arrow-down' : 'tag'" :label="price.lowest ? 'Lowest' : undefined" />{{
        price.text
      }}
    </span>
    <span
      v-if="sun && !hideWeather"
      class="badge"
      :class="{ 'basis-weekly': sun.knmi && !sun.sunny, 'tone-ok': sun.sunny }"
      :title="`Sun (${sunSourceNames[advice.sunSource]}): ${sunText(advice)}${sun.sunny ? `, ${cheapHint}` : ''}`"
      data-testid="badge-sun"
    >
      <AppIcon name="sun" :label="sun.knmi ? 'Sunshine (KNMI)' : 'Local solar (EVCC)'" />{{ sun.text }}
    </span>
    <span
      v-if="bft != null && !hideWeather"
      class="badge"
      :class="{ 'tone-ok': windy(bft) }"
      :title="`Wind Bft ${bft} (KNMI)${windy(bft) ? `: windy, ${cheapHint}` : ''}`"
      data-testid="badge-wind"
    >
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
</style>
