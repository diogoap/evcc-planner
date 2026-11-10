<script setup lang="ts">
import { computed } from 'vue'
import type { Day, DayAdvice } from '../api/types'
import { dayShort, km, kwh } from '../lib/format'
import { dayLabel } from '../lib/status'
import AdviceBadges from './AdviceBadges.vue'

const props = defineProps<{ days: Day[]; advice?: DayAdvice[]; selected?: string }>()
const emit = defineEmits<{ select: [date: string]; advice: [date: string] }>()

const byDate = computed(() => new Map((props.advice ?? []).map((a) => [a.date, a])))
</script>

<template>
  <table class="table" aria-label="Next seven days">
    <thead>
      <tr>
        <th>Day</th>
        <th class="right">Driving</th>
        <th class="right hide-sm">Energy</th>
        <th>Plan</th>
        <th v-if="advice?.length">Daily advice</th>
      </tr>
    </thead>
    <tbody>
      <tr
        v-for="d in days"
        :key="d.date"
        class="clickable"
        tabindex="0"
        :data-testid="`day-${d.date}`"
        @click="emit('select', d.date)"
        @keydown.enter="emit('select', d.date)"
      >
        <td class="num">{{ dayShort(d.date) }}</td>
        <td class="right num">{{ km(d.distanceKm) }}</td>
        <td class="right num hide-sm">{{ kwh(d.energyKWh) }}</td>
        <td>
          <span class="pill" :class="`tone-${dayLabel(d).tone}`">{{ dayLabel(d).text }}</span>
          <span v-if="dayLabel(d).detail" class="small muted plan-detail">{{ dayLabel(d).detail }}</span>
          <span v-if="d.reviewCount" class="pill tone-warn" style="margin-left: 6px"
            >{{ d.reviewCount }} to review</span
          >
        </td>
        <td v-if="byDate.get(d.date)" class="advice-cell">
          <button
            type="button"
            class="advice-btn"
            :aria-pressed="selected === d.date"
            :data-testid="`advice-${d.date}`"
            :title="byDate.get(d.date)!.summary"
            @click.stop="emit('advice', d.date)"
            @keydown.enter.stop
          >
            <AdviceBadges :advice="byDate.get(d.date)!" :day="d" />
          </button>
        </td>
      </tr>
    </tbody>
  </table>
</template>

<style scoped>
.plan-detail {
  margin-left: 6px;
  white-space: nowrap;
}

.advice-btn {
  appearance: none;
  border: 1px solid transparent;
  background: none;
  color: inherit;
  font: inherit;
  padding: 2px 4px;
  margin: -3px -5px;
  border-radius: 10px;
  cursor: pointer;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  text-align: left;
}

.advice-btn[aria-pressed='true'] {
  border-color: var(--accent);
}

.advice-btn:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: 2px;
}
</style>
