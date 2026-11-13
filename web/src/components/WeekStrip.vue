<script setup lang="ts">
import { computed } from 'vue'
import type { Day, DayAdvice } from '../api/types'
import { adviceLabel, confidenceNames, priceBadge } from '../lib/advice'
import { dayShort, km, kwh } from '../lib/format'
import { dayLabel, dayTile, reviewText } from '../lib/status'
import AppIcon from './AppIcon.vue'

// The days at a glance: the charge each day's departures need and its
// advice. Phones show a word and an icon per day; wide screens add distance,
// energy, the first departure, and the price. Screen readers get all of it,
// in words, on every screen.
const props = defineProps<{ days: Day[]; advice: DayAdvice[]; selected?: string }>()
const emit = defineEmits<{ select: [date: string] }>()

const byDate = computed(() => new Map(props.advice.map((a) => [a.date, a])))

const tiles = computed(() =>
  props.days.map((d) => {
    const a = byDate.value.get(d.date)
    const label = dayLabel(d)
    const adv = a ? adviceLabel(a, d) : null
    const [weekday, date] = dayShort(d.date).split(' ')
    return {
      day: d,
      weekday,
      date,
      tile: dayTile(d),
      label: label.text,
      detail: label.detail,
      advice: adv,
      confidence: a?.confidence ? `, confidence ${confidenceNames[a.confidence].toLowerCase()}` : '',
      price: a ? priceBadge(a) : null,
    }
  }),
)
</script>

<template>
  <div class="week" role="group" :aria-label="`Next ${days.length} days`" :style="{ '--days': days.length }">
    <button
      v-for="t in tiles"
      :key="t.day.date"
      type="button"
      class="week-day"
      :aria-pressed="selected === t.day.date"
      :data-testid="`day-${t.day.date}`"
      @click="emit('select', t.day.date)"
    >
      <span class="week-date"
        ><span class="week-weekday">{{ t.weekday }}</span> <span class="num">{{ t.date }}</span></span
      >
      <span class="week-wide week-figures small muted num"
        ><span><AppIcon name="route" />{{ km(t.day.distanceKm) }}</span
        ><span class="sr-only">, </span><span><AppIcon name="bolt" />{{ kwh(t.day.energyKWh) }}</span></span
      >
      <span class="week-charge strong" :class="`text-${t.tile.tone}`"
        ><AppIcon v-if="t.tile.icon" :name="t.tile.icon" /><span aria-hidden="true">{{ t.tile.text }}</span
        ><span class="sr-only">{{ t.label }}</span></span
      >
      <span v-if="t.detail" class="week-wide small muted num"><AppIcon name="battery" /> {{ t.detail }}</span>
      <span
        v-if="t.advice"
        class="week-advice pill"
        :class="`tone-${t.advice.tone}`"
        :title="t.advice.text"
        data-testid="badge-advice"
        ><AppIcon :name="t.advice.icon" /><span class="week-wide">{{ t.advice.short }}</span></span
      >
      <span v-if="t.confidence" class="sr-only">{{ t.confidence }}</span>
      <span
        v-if="t.price"
        class="week-wide badge"
        :class="`basis-${t.price.basis}`"
        :title="t.price.title"
        data-testid="badge-price"
        ><AppIcon
          :name="t.price.lowest ? 'arrow-down' : 'tag'"
          :label="t.price.lowest ? 'Lowest' : undefined"
        />{{ t.price.text }}</span
      >
      <span v-if="t.day.reviewCount" class="week-review pill tone-warn" :title="reviewText(t.day.reviewCount)"
        ><span aria-hidden="true">!</span
        ><span class="week-wide">{{ reviewText(t.day.reviewCount) }}</span></span
      >
    </button>
  </div>
</template>

<style scoped>
.week {
  display: grid;
  grid-template-columns: repeat(var(--days, 7), minmax(0, 1fr));
  gap: var(--space-1);
}

.week-day {
  appearance: none;
  position: relative;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--space-1);
  min-width: 0;
  padding: var(--space-2) 2px;
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--surface);
  color: var(--text);
  font: inherit;
  cursor: pointer;
  box-shadow: var(--shadow);
}

.week-day:hover {
  background: var(--surface-2);
}

.week-day[aria-pressed='true'] {
  border-color: var(--accent-ui);
  box-shadow: inset 0 0 0 1px var(--accent-ui);
}

.week-day:focus-visible {
  outline: 2px solid var(--focus);
  outline-offset: 2px;
}

.week-date {
  display: flex;
  flex-direction: column;
  align-items: center;
  line-height: 1.15;
}

.week-weekday {
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  letter-spacing: 0.06em;
  text-transform: uppercase;
  color: var(--muted);
}

.week-date .num {
  font-size: var(--text-lg);
  font-weight: var(--weight-semibold);
}

.week-charge {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  font-size: var(--text-sm);
  white-space: nowrap;
}

.week-advice {
  padding: 2px 6px;
}

.week-figures {
  display: flex;
  flex-wrap: wrap;
  gap: 0 var(--space-2);
}

.week-review {
  position: absolute;
  top: -6px;
  right: -4px;
  padding: 0 6px;
}

@media (max-width: 719px) {
  .week-wide {
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip: rect(0 0 0 0);
  }
}

/* Wide screens: room for the figures, left-aligned like a small card. */
@media (min-width: 720px) {
  .week {
    gap: var(--space-2);
  }

  .week-day {
    align-items: flex-start;
    text-align: left;
    padding: var(--space-3);
  }

  .week-date {
    flex-direction: row;
    align-items: baseline;
    gap: var(--space-1);
  }

  .week-advice {
    gap: var(--space-1);
  }

  .week-review {
    position: static;
  }
}
</style>
