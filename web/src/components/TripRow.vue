<script setup lang="ts">
import { computed } from 'vue'
import type { Trip } from '../api/types'
import { km, kwh } from '../lib/format'
import ToggleSwitch from './ToggleSwitch.vue'

const props = defineProps<{ trip: Trip }>()
const emit = defineEmits<{
  toggle: [enabled: boolean]
  edit: []
  reset: []
  remove: []
  review: []
}>()

const source = computed(() => {
  const t = props.trip
  if (t.source === 'recurring') return `Recurring: ${t.sourceLabel}`
  if (t.source === 'calendar') return `Calendar: ${t.sourceLabel}`
  return 'Manual'
})
</script>

<template>
  <li
    class="trip"
    :class="{ off: !trip.enabled, done: trip.state === 'done' }"
    :data-testid="`trip-${trip.key}`"
  >
    <ToggleSwitch
      :model-value="trip.enabled"
      :disabled="!!trip.disabledReason"
      :label="
        trip.disabledReason
          ? `${trip.title} on ${trip.date} is off: ${trip.disabledReason}`
          : `${trip.enabled ? 'Disable' : 'Enable'} ${trip.title} on ${trip.date}`
      "
      @update:model-value="emit('toggle', $event)"
    />
    <div class="trip-main">
      <div class="trip-line">
        <span class="num trip-time">{{ trip.arriveLocal }}</span>
        <strong class="trip-title">{{ trip.title }}</strong>
        <span class="spacer" />
        <span class="num">{{ km(trip.distanceKm) }}</span>
        <span class="num muted trip-energy">{{ kwh(trip.energyKWh) }}</span>
      </div>
      <div class="trip-sub small muted">
        <span>Leave {{ trip.departLocal }} · until {{ trip.endLocal }}</span>
        <span v-if="trip.placeName || trip.destination">· {{ trip.placeName || trip.destination }}</span>
        <span>· {{ source }}</span>
        <span v-if="trip.distanceSource === 'manual'" class="pill">Manual distance</span>
        <span v-if="trip.overridden.length" class="pill" title="Edited for this date">Edited</span>
        <span v-if="trip.needsReview" class="pill tone-warn">Needs review</span>
        <span v-if="trip.routeStale" class="pill tone-warn" title="Routing failed; using an older route"
          >Old route</span
        >
        <span v-if="trip.state === 'in_progress'" class="pill tone-info">On the road</span>
        <span v-if="trip.disabledReason" class="pill" data-testid="disabled-reason">{{
          trip.disabledReason
        }}</span>
      </div>
      <div v-if="trip.disabledReason" class="small muted">
        Off until it has a route: edit to add a destination or a distance.
      </div>
      <div v-else-if="trip.routeError" class="small text-warn">{{ trip.routeError }}</div>
      <div class="row trip-actions">
        <button v-if="trip.needsReview" class="btn sm" type="button" @click="emit('review')">
          Confirm car trip
        </button>
        <button class="btn sm" type="button" @click="emit('edit')">Edit</button>
        <button
          v-if="trip.overridden.length && trip.source !== 'manual'"
          class="btn sm ghost"
          type="button"
          @click="emit('reset')"
        >
          Reset
        </button>
        <button
          v-if="trip.source === 'manual'"
          class="btn sm ghost danger"
          type="button"
          @click="emit('remove')"
        >
          Delete
        </button>
      </div>
    </div>
  </li>
</template>

<style scoped>
.trip {
  display: flex;
  gap: 12px;
  align-items: flex-start;
  padding: 12px 0;
  border-top: 1px solid var(--border);
  list-style: none;
}

.trip:first-child {
  border-top: none;
}

.trip-main {
  flex: 1;
  min-width: 0;
}

.trip-line {
  display: flex;
  gap: 10px;
  align-items: baseline;
  flex-wrap: wrap;
}

.trip-time {
  font-weight: 650;
}

.trip-energy {
  min-width: 64px;
  text-align: right;
}

.trip-sub {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 6px;
  align-items: center;
  margin-top: 2px;
}

.trip-actions {
  margin-top: 6px;
}

.trip.off .trip-line,
.trip.off .trip-sub {
  opacity: 0.55;
}

.trip.off .trip-title {
  text-decoration: line-through;
}

.trip.done .trip-line {
  opacity: 0.7;
}
</style>
