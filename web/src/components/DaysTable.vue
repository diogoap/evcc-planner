<script setup lang="ts">
import type { Day } from '../api/types'
import { dayShort, km, kwh } from '../lib/format'
import { dayLabel } from '../lib/status'

defineProps<{ days: Day[] }>()
const emit = defineEmits<{ select: [date: string] }>()
</script>

<template>
  <table class="table" aria-label="Next seven days">
    <thead>
      <tr>
        <th>Day</th>
        <th class="right">Driving</th>
        <th class="right">Energy</th>
        <th>Plan</th>
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
        <td class="right num">{{ kwh(d.energyKWh) }}</td>
        <td>
          <span class="pill" :class="`tone-${dayLabel(d).tone}`">{{ dayLabel(d).text }}</span>
          <span v-if="d.reviewCount" class="pill tone-warn" style="margin-left: 6px"
            >{{ d.reviewCount }} to review</span
          >
        </td>
      </tr>
    </tbody>
  </table>
</template>
