<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import type { Day, DayAdvice } from '../api/types'
import { basisNames, cheapHint, sunny, windy } from '../lib/advice'
import { dayLong, dayShort, km, kwh } from '../lib/format'
import { dayLabel, reviewText } from '../lib/status'
import AdviceBadges from './AdviceBadges.vue'
import AppIcon from './AppIcon.vue'

// The days as a list, with everything about each day: distance, energy,
// the charge its departures need, the price (the lowest slot over the average
// on published days, else the average), the weather (sunshine and wind from
// KNMI's outlook, every day, plus EVCC's local solar forecast where it has
// one), and the advice. A row per day on wide screens; on phones each day
// stacks its figures, plan and weather, and advice.
//
// Wide screens (every column showing): a row selects its day for the advice
// card, and the button in the last column opens the day. On narrower
// screens the stacked row opens the day itself.
const props = defineProps<{ days: Day[]; advice: DayAdvice[]; selected?: string }>()
const emit = defineEmits<{ select: [date: string]; open: [date: string] }>()

// The same width as the wide layout below (720 px).
const media = typeof window !== 'undefined' ? window.matchMedia?.('(min-width: 720px)') : undefined
const wide = ref(!!media?.matches)
const update = () => (wide.value = !!media?.matches)
onMounted(() => media?.addEventListener('change', update))
onUnmounted(() => media?.removeEventListener('change', update))
const pick = (date: string) => (wide.value ? emit('select', date) : emit('open', date))

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
  <div class="card week-list" role="group" :aria-label="`Next ${days.length} days`">
    <div class="wl-head" aria-hidden="true">
      <span>Day</span>
      <span class="wl-num"><AppIcon name="route" />Driving</span>
      <span class="wl-num"><AppIcon name="bolt" />Energy</span>
      <span>Plan</span>
      <span v-if="advice.length"><AppIcon name="tag" />Price</span>
      <span v-if="advice.length">Weather</span>
      <span v-if="advice.length">Daily advice</span>
    </div>
    <div v-for="r in rows" :key="r.day.date" class="wl-item">
      <button
        type="button"
        class="wl-row"
        :aria-pressed="selected === r.day.date"
        :data-testid="`day-${r.day.date}`"
        @click="pick(r.day.date)"
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
            >{{ r.price.basis === 'published' ? '' : '~' }}€{{ r.price.value.toFixed(2) }} avg</span
          >
          <span v-else class="muted">–</span>
          <span v-if="r.price?.min != null" class="small muted num" data-testid="price-min"
            >€{{ r.price.min.toFixed(2) }} min</span
          >
        </span>
        <span v-if="r.advice" class="wl-weather">
          <span
            class="badge"
            :class="{ 'tone-ok': sunny(r.sunshine) }"
            :title="`Sunshine (KNMI outlook)${sunny(r.sunshine) ? `: sunny, ${cheapHint}` : ''}`"
            data-testid="badge-sunshine"
            ><AppIcon name="sun" label="Sunshine" />{{ r.sunshine != null ? `${r.sunshine}%` : '–' }}</span
          >
          <span
            class="badge"
            :class="{ 'tone-ok': windy(r.wind) }"
            :title="`Wind (KNMI outlook)${windy(r.wind) ? `: windy, ${cheapHint}` : ''}`"
            data-testid="badge-wind"
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
      <button
        type="button"
        class="btn sm icon-only wl-open"
        :aria-label="`Open ${dayLong(r.day.date)}`"
        title="Open the day"
        :data-testid="`open-${r.day.date}`"
        @click="emit('open', r.day.date)"
      >
        <AppIcon name="chevron-right" />
      </button>
    </div>
  </div>
</template>

<style scoped>
.week-list {
  padding: var(--space-2) var(--space-3);
}

.wl-head,
.wl-row {
  display: grid;
  grid-template-columns: 4.5rem 5rem 6rem 8.5rem 8.5rem 8.5rem minmax(0, 1fr) 2rem;
  grid-template-areas: 'day km kwh plan price weather advice open';
  gap: var(--space-3);
  align-items: center;
}

/* The open button sits over the row's last (empty) column. */
.wl-item {
  position: relative;
}

.wl-open {
  position: absolute;
  top: 50%;
  right: var(--space-2);
  transform: translateY(-50%);
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

.wl-item:hover .wl-row {
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

  .wl-head + .wl-item .wl-row {
    border-top: 0;
  }

  /* The stacked row opens the day itself. */
  .wl-open {
    display: none;
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
