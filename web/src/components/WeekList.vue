<script setup lang="ts">
import { computed } from 'vue'
import type { Day, DayAdvice } from '../api/types'
import { basisNames } from '../lib/advice'
import { dayShort, km, kwh } from '../lib/format'
import { dayLabel, reviewText } from '../lib/status'
import AdviceBadges from './AdviceBadges.vue'
import AppIcon from './AppIcon.vue'

// The seven days as a list, with everything about each day: distance, energy,
// the charge its departures need, the price (the lowest slot over the average
// on published days, else the average), the weather (sunshine and wind from
// KNMI's outlook, every day, plus EVCC's local solar forecast where it has
// one), and the advice. A row per day on wide screens; on phones each day
// stacks its figures, plan and weather, and advice.
const props = defineProps<{ days: Day[]; advice: DayAdvice[]; selected?: string }>()
const emit = defineEmits<{ select: [date: string] }>()

const rows = computed(() => {
  const byDate = new Map(props.advice.map((a) => [a.date, a]))
  return props.days.map((d) => {
    const a = byDate.get(d.date)
    return {
      day: d,
      label: dayLabel(d),
      advice: a,
      sunshine: a?.weather?.sunshinePct ?? null,
      wind: a?.weather?.windBft ?? null,
      solar: a?.sunSource === 'evcc' && a.solarKWh != null ? a.solarKWh : null,
      price: a?.price ?? null,
    }
  })
})
</script>

<template>
  <div class="card week-list" role="group" aria-label="Next seven days">
    <div class="wl-head" aria-hidden="true">
      <span>Day</span>
      <span class="wl-num"><AppIcon name="route" />Driving</span>
      <span class="wl-num"><AppIcon name="bolt" />Energy</span>
      <span>Plan</span>
      <span v-if="advice.length"><AppIcon name="tag" />Price</span>
      <span v-if="advice.length">Weather</span>
      <span v-if="advice.length">Daily advice</span>
    </div>
    <button
      v-for="r in rows"
      :key="r.day.date"
      type="button"
      class="wl-row"
      :aria-pressed="selected === r.day.date"
      :data-testid="`day-${r.day.date}`"
      @click="emit('select', r.day.date)"
    >
      <span class="wl-day strong num">{{ dayShort(r.day.date) }}</span>
      <span class="wl-km wl-num num"
        ><AppIcon name="route" class="wl-row-icon" />{{ km(r.day.distanceKm) }}</span
      >
      <span class="wl-kwh wl-num num"
        ><AppIcon name="bolt" class="wl-row-icon" />{{ kwh(r.day.energyKWh) }}</span
      >
      <span class="wl-plan">
        <span class="pill" :class="`tone-${r.label.tone}`">{{ r.label.text }}</span>
        <span v-if="r.label.detail" class="small muted num"
          ><AppIcon name="battery" /> {{ r.label.detail }}</span
        >
      </span>
      <span v-if="r.advice" class="wl-price">
        <span
          v-if="r.price"
          class="badge"
          :class="`basis-${r.price.basis}`"
          :title="`${basisNames[r.price.basis]}: ${r.price.detail}`"
          data-testid="badge-price"
          ><template v-if="r.price.min != null"
            ><AppIcon name="arrow-down" label="Lowest" />€{{ r.price.min.toFixed(2) }}</template
          ><template v-else
            >{{ r.price.basis === 'published' ? '' : '~' }}€{{ r.price.value.toFixed(2) }} avg</template
          ></span
        >
        <span v-else class="muted">–</span>
        <span v-if="r.price?.min != null" class="small muted num" data-testid="price-avg"
          >€{{ r.price.value.toFixed(2) }} avg</span
        >
      </span>
      <span v-if="r.advice" class="wl-weather">
        <span class="badge" title="Sunshine (KNMI outlook)" data-testid="badge-sunshine"
          ><AppIcon name="sun" label="Sunshine" />{{ r.sunshine != null ? `${r.sunshine}%` : '–' }}</span
        >
        <span class="badge" title="Wind (KNMI outlook)" data-testid="badge-wind"
          ><AppIcon name="wind" label="Wind" />{{ r.wind != null ? `${r.wind} Bft` : '–' }}</span
        >
        <span
          v-if="r.solar != null"
          class="wl-solar wl-icon-text small muted num"
          title="EVCC solar forecast (local)"
          data-testid="solar"
          ><AppIcon name="solar-panel" label="Solar forecast" />{{ r.solar.toFixed(1) }} kWh</span
        >
      </span>
      <span v-if="r.advice || r.day.reviewCount" class="wl-advice">
        <AdviceBadges v-if="r.advice" :advice="r.advice" :day="r.day" hide-price hide-weather />
        <span v-if="r.day.reviewCount" class="pill tone-warn" data-testid="review-count">{{
          reviewText(r.day.reviewCount)
        }}</span>
      </span>
    </button>
  </div>
</template>

<style scoped>
.week-list {
  padding: var(--space-2) var(--space-3);
}

.wl-head,
.wl-row {
  display: grid;
  grid-template-columns: 4.5rem 5rem 6rem 8.5rem 8.5rem 8.5rem minmax(0, 1fr);
  grid-template-areas: 'day km kwh plan price weather advice';
  gap: var(--space-3);
  align-items: center;
}

.wl-head {
  padding: 6px var(--space-2);
  font-size: var(--text-xs);
  font-weight: var(--weight-semibold);
  letter-spacing: 0.06em;
  text-transform: uppercase;
  color: var(--muted);
}

.wl-row {
  appearance: none;
  width: 100%;
  padding: 10px var(--space-2);
  border: 0;
  border-top: 1px solid var(--border);
  border-radius: 0;
  background: none;
  color: var(--text);
  font: inherit;
  text-align: left;
  cursor: pointer;
}

.wl-row:hover {
  background: var(--surface-2);
}

.wl-row[aria-pressed='true'] {
  background: var(--surface-2);
  box-shadow: inset 3px 0 0 var(--accent-ui);
}

.wl-row:focus-visible {
  outline: 2px solid var(--focus);
  outline-offset: -2px;
}

.wl-day {
  grid-area: day;
}

.wl-km {
  grid-area: km;
}

.wl-kwh {
  grid-area: kwh;
}

.wl-num {
  display: inline-flex;
  align-items: center;
  justify-content: flex-end;
  gap: var(--space-1);
}

.wl-num .icon {
  color: var(--muted);
}

/* The column headers carry these icons; rows repeat them only on phones. */
.wl-row-icon {
  display: none;
}

/* The charge on one line, the first departure's target below it. */
.wl-plan {
  grid-area: plan;
  display: grid;
  justify-items: start;
  gap: 2px;
}

.wl-advice {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-1) 6px;
  min-width: 0;
}

.wl-advice {
  grid-area: advice;
}

.wl-price {
  grid-area: price;
  display: grid;
  justify-items: start;
  gap: 2px;
}

.wl-head .icon {
  margin-right: var(--space-1);
  vertical-align: -0.15em;
}

.wl-weather {
  grid-area: weather;
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-1);
}

.wl-solar {
  flex-basis: 100%;
}

/* A small icon in front of a figure, e.g. the lowest price or solar. */
.wl-icon-text {
  display: inline-flex;
  align-items: center;
  gap: 3px;
}

/* Phones: the day and its figures on one line, then the plan, then the
   advice badges wrapping across the full width. */
@media (max-width: 719px) {
  .wl-head {
    display: none;
  }

  .wl-row {
    grid-template-columns: 1fr auto auto;
    grid-template-areas: 'day km kwh' 'plan plan plan' 'price price price' 'weather weather weather' 'advice advice advice';
    gap: 6px var(--space-3);
    padding: var(--space-3) var(--space-1);
  }

  .wl-row:first-of-type {
    border-top: 0;
  }

  .wl-solar {
    flex-basis: auto;
  }

  .wl-row-icon {
    display: inline;
  }

  .wl-plan,
  .wl-price {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--space-1) var(--space-2);
  }
}
</style>
