<script setup lang="ts">
import { computed } from 'vue'
import type { Trip } from '../api/types'
import { km, kwh } from '../lib/format'
import ToggleSwitch from './ToggleSwitch.vue'

// target: SoC needed when leaving, shown on the first trip of a departure.
const props = defineProps<{ trip: Trip; target?: number }>()
const emit = defineEmits<{
  toggle: [enabled: boolean]
  edit: []
  reset: []
  remove: []
  review: []
  history: []
}>()

const source = computed(() => {
  const t = props.trip
  if (t.source === 'recurring') return `Recurring: ${t.sourceLabel}`
  if (t.source === 'calendar') return `Calendar: ${t.sourceLabel}`
  return 'Manual'
})
// One line that wraps between words: "Leave 07:48 · until 08:20 · School · Recurring: …".
const details = computed(() => {
  const t = props.trip
  return [`Leave ${t.departLocal}`, `until ${t.endLocal}`, t.placeName || t.destination, source.value]
    .filter(Boolean)
    .join(' · ')
})
const tags = computed(() => {
  const t = props.trip
  return (
    t.distanceSource === 'manual' ||
    t.overridden.length > 0 ||
    t.needsReview ||
    t.routeStale ||
    t.state === 'in_progress' ||
    props.target != null ||
    !!t.disabledReason
  )
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
    <div class="trip-head">
      <span class="num trip-time">{{ trip.arriveLocal }}</span>
      <strong class="trip-title">{{ trip.title }}</strong>
    </div>
    <div class="trip-figures num">
      <span>{{ km(trip.distanceKm) }}</span>
      <span class="muted trip-energy">{{ kwh(trip.energyKWh) }}</span>
    </div>
    <div class="trip-sub small muted">{{ details }}</div>
    <div v-if="tags" class="trip-tags">
      <span v-if="trip.distanceSource === 'manual'" class="pill">Manual distance</span>
      <span v-if="trip.overridden.length" class="pill" title="Edited for this date">Edited</span>
      <span v-if="trip.needsReview" class="pill tone-warn">Needs review</span>
      <span v-if="trip.routeStale" class="pill tone-warn" title="Routing failed; using an older route"
        >Old route</span
      >
      <span v-if="trip.state === 'in_progress'" class="pill tone-info">On the road</span>
      <span v-if="target != null" class="pill tone-info" data-testid="departure-target"
        >Needs {{ target }}% at departure</span
      >
      <span v-if="trip.disabledReason" class="pill" data-testid="disabled-reason">{{
        trip.disabledReason
      }}</span>
    </div>
    <div v-if="trip.disabledReason" class="trip-note small muted">
      Off until it has a route: edit to add a destination or a distance.
    </div>
    <div v-else-if="trip.routeError" class="trip-note small text-warn">{{ trip.routeError }}</div>
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
      <button
        class="btn sm ghost"
        type="button"
        :aria-label="`History of ${trip.title} on ${trip.date}`"
        @click="emit('history')"
      >
        History
      </button>
    </div>
  </li>
</template>

<style scoped>
/* The switch, then the trip: time and title with distance and energy on the
   right, its details, tags, and actions below. */
.trip {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr) auto;
  grid-template-areas:
    'toggle head figures'
    '. sub figures'
    '. tags tags'
    '. note note'
    '. actions actions';
  column-gap: var(--space-3);
  row-gap: 2px;
  align-items: start;
  padding: var(--space-3) 0;
  border-top: 1px solid var(--border);
  list-style: none;
}

.trip:first-child {
  border-top: none;
}

.trip > :first-child {
  grid-area: toggle;
}

.trip-head {
  grid-area: head;
  display: flex;
  align-items: baseline;
  gap: var(--space-2);
  min-width: 0;
}

.trip-time {
  font-weight: var(--weight-semibold);
}

.trip-title {
  overflow-wrap: anywhere;
}

.trip-figures {
  grid-area: figures;
  display: grid;
  justify-items: end;
  line-height: 1.35;
}

.trip-sub {
  grid-area: sub;
}

.trip-tags {
  grid-area: tags;
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1) 6px;
  margin-top: var(--space-1);
}

.trip-note {
  grid-area: note;
}

.trip-actions {
  grid-area: actions;
  margin-top: var(--space-1);
}

/* Off and finished trips are greyed out, still readable. */
.trip.off .trip-head,
.trip.off .trip-figures,
.trip.done .trip-head,
.trip.done .trip-figures {
  color: var(--muted);
}

.trip.off .pill {
  background: var(--surface-2);
  color: var(--muted);
}

.trip.off .trip-title {
  text-decoration: line-through;
}
</style>
