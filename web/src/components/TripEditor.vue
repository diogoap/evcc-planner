<script setup lang="ts">
import { computed, nextTick, reactive, ref } from 'vue'
import { api } from '../api/client'
import type { Place, Trip, TripInput } from '../api/types'
import { km } from '../lib/format'
import {
  HOME,
  MAX_POINTS,
  MIN_POINTS,
  isHome,
  legText,
  moved,
  newStopIndex,
  pointLabel,
  pointLetter,
  routeText,
} from '../lib/route'
import AppIcon from './AppIcon.vue'
import ModalDialog from './ModalDialog.vue'

const props = defineProps<{ trip?: Trip; date?: string; places: Place[]; defaultStayMin: number }>()
const emit = defineEmits<{ close: []; saved: [message: string] }>()

const editing = computed(() => !!props.trip)
// Recurring trips always go from home to their destination and back.
const recurring = computed(() => props.trip?.source === 'recurring')

// Each point keeps an id so moving it moves its row (and focus) along.
let nextId = 0
const point = (name: string) => ({ id: nextId++, name })
const initialRoute = props.trip?.points?.length ? props.trip.points.map((p) => p.name) : [HOME, '', HOME]

const form = reactive({
  title: props.trip?.title ?? '',
  destination: props.trip?.destination ?? '',
  points: initialRoute.map(point),
  date: props.trip?.date ?? props.date ?? '',
  start: props.trip?.arriveLocal ?? '09:00',
  end: props.trip?.endLocal ?? '',
  enabled: props.trip?.enabled ?? true,
  distance: props.trip?.distanceOverrideKm != null ? String(props.trip.distanceOverrideKm) : '',
})
const error = ref('')
const saving = ref(false)

const count = computed(() => form.points.length)
const oneWay = computed(() => !recurring.value && count.value === MIN_POINTS)
const stops = computed(() => (recurring.value ? 1 : Math.max(1, count.value - 2)))
const startsAway = computed(() => !recurring.value && !isHome(form.points[0].name))
const endsAway = computed(() => !recurring.value && !isHome(form.points[count.value - 1].name))

// The legs as last calculated, while the route is the one they were
// calculated for.
const legs = computed(() => {
  const t = props.trip
  if (!t?.legs?.length) return []
  const same =
    form.points.length === initialRoute.length &&
    form.points.every((p, i) => p.name.trim() === initialRoute[i])
  return same ? t.legs : []
})

const calculated = computed(() =>
  props.trip && props.trip.distanceSource === 'route' ? km(props.trip.distanceKm) : null,
)

const scopeNote = computed(() => {
  if (recurring.value) return 'Changes apply to this date only. Edit the schedule under Recurring trips.'
  if (props.trip?.source === 'calendar')
    return 'Changes stay in the planner and survive calendar syncs; the calendar itself is not changed.'
  return ''
})

// Focus stays with the point that changed: the first of the selectors that
// isn't disabled, else Add stop. Only a new stop focuses its field, as on a
// phone a field brings up the keyboard.
const routeList = ref<HTMLOListElement>()
const addButton = ref<HTMLButtonElement>()
function focusIn(id: number, ...selectors: string[]) {
  nextTick(() => {
    const row = routeList.value?.querySelector<HTMLElement>(`[data-point="${id}"]`)
    for (const selector of selectors) {
      const el = row?.querySelector<HTMLButtonElement>(selector)
      if (el && !el.disabled) return el.focus()
    }
    addButton.value?.focus()
  })
}

function addStop() {
  if (count.value >= MAX_POINTS) return
  const p = point('')
  form.points.splice(newStopIndex(count.value), 0, p)
  focusIn(p.id, 'input')
}

function remove(index: number) {
  if (count.value <= MIN_POINTS) return
  form.points.splice(index, 1)
  focusIn(form.points[Math.min(index, count.value - 1)].id, '[data-remove]')
}

function move(index: number, by: -1 | 1) {
  const id = form.points[index].id
  form.points = moved(form.points, index, index + by)
  const [same, other] =
    by < 0 ? ['[data-move=up]', '[data-move=down]'] : ['[data-move=down]', '[data-move=up]']
  focusIn(id, same, other)
}

async function save() {
  error.value = ''
  const distance = form.distance.trim() === '' ? null : Number(form.distance)
  if (distance !== null && (!Number.isFinite(distance) || distance <= 0)) {
    error.value = 'Distance must be a positive number of kilometres.'
    return
  }
  const input: TripInput = {
    title: form.title,
    date: form.date,
    start: form.start,
    enabled: form.enabled,
    distanceOverrideKm: distance,
  }
  if (recurring.value) input.destination = form.destination
  else input.points = form.points.map((p) => p.name)
  if (form.end && !oneWay.value) input.end = form.end
  saving.value = true
  try {
    if (props.trip) {
      await api.updateTrip(props.trip.key, input)
      emit('saved', 'Trip updated')
    } else {
      await api.createTrip(input)
      emit('saved', 'Trip added')
    }
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <ModalDialog :title="editing ? 'Edit trip' : 'Add trip'" @close="emit('close')">
    <form class="stack gap-3" @submit.prevent="save">
      <datalist id="places">
        <option :value="HOME">Your home address</option>
        <option v-for="p in places" :key="p.id" :value="p.name">{{ p.address }}</option>
      </datalist>
      <template v-if="recurring">
        <label class="field">
          Destination
          <input v-model="form.destination" list="places" required placeholder="Saved place or address" />
        </label>
        <p class="small muted m-0" data-testid="route-fixed">
          {{ trip ? routeText(trip.points) : '' }} · Recurring trips go from home and back.
        </p>
      </template>
      <fieldset v-else class="route">
        <legend>Route</legend>
        <ol ref="routeList" class="route-points" data-testid="route-points">
          <li v-for="(p, i) in form.points" :key="p.id" class="route-point" :data-point="p.id">
            <span class="route-letter num" aria-hidden="true">{{ pointLetter(i) }}</span>
            <label class="field">
              {{ pointLabel(i, count) }}
              <input
                v-model="p.name"
                list="places"
                required
                :placeholder="
                  i === 0 || i === count - 1 ? 'Home, saved place, or address' : 'Saved place or address'
                "
              />
            </label>
            <div class="route-actions">
              <button
                class="btn sm ghost icon-only"
                type="button"
                data-move="up"
                :disabled="i === 0"
                :aria-label="`Move ${pointLabel(i, count)} up`"
                @click="move(i, -1)"
              >
                <AppIcon name="arrow-up" />
              </button>
              <button
                class="btn sm ghost icon-only"
                type="button"
                data-move="down"
                :disabled="i === count - 1"
                :aria-label="`Move ${pointLabel(i, count)} down`"
                @click="move(i, 1)"
              >
                <AppIcon name="arrow-down" />
              </button>
              <button
                class="btn sm ghost icon-only"
                type="button"
                data-remove
                :disabled="count <= MIN_POINTS"
                :aria-label="`Remove ${pointLabel(i, count)}`"
                @click="remove(i)"
              >
                <AppIcon name="close" />
              </button>
            </div>
            <p v-if="legs[i]" class="route-leg small muted m-0" data-testid="route-leg">
              <AppIcon name="arrow-down" />{{ legText(legs[i]) }}
            </p>
          </li>
        </ol>
        <div class="row">
          <button
            ref="addButton"
            class="btn sm"
            type="button"
            :disabled="count >= MAX_POINTS"
            @click="addStop"
          >
            <AppIcon name="plus" />Add stop
          </button>
          <span class="small muted">{{ count }} of {{ MAX_POINTS }} points</span>
        </div>
      </fieldset>
      <label class="field">
        Title
        <input v-model="form.title" placeholder="Defaults to the first stop" />
      </label>
      <div class="form-grid">
        <label class="field">
          Date
          <input v-model="form.date" type="date" required />
        </label>
        <label class="field">
          Arrive by{{ stops > 1 ? ' (first stop)' : '' }}
          <input v-model="form.start" type="time" required />
        </label>
        <label v-if="!oneWay" class="field">
          {{ stops > 1 ? 'Until (leave the last stop)' : 'Until (return leaves)' }}
          <input v-model="form.end" type="time" :placeholder="`${defaultStayMin} min stay`" />
        </label>
      </div>
      <label class="field">
        Distance, whole trip (km)
        <input
          v-model="form.distance"
          inputmode="decimal"
          :placeholder="calculated ? `Calculated: ${calculated}` : 'Calculated from the route'"
        />
      </label>
      <label class="row small">
        <input v-model="form.enabled" type="checkbox" />
        Counts toward charging (enabled)
      </label>
      <p v-if="!editing && !form.end && !oneWay" class="small muted m-0">
        Without an end time the {{ stops > 1 ? 'last stop is left' : 'return leaves' }}
        {{ defaultStayMin }} minutes after arrival.
      </p>
      <p v-if="endsAway" class="small muted m-0" data-testid="route-ends-away">
        The car stays at the end until a later trip brings it home, so it can't charge in between.
      </p>
      <p v-if="startsAway" class="small muted m-0" data-testid="route-starts-away">
        The trip doesn't start at home: the car is away before it.
      </p>
      <p v-if="scopeNote" class="small muted m-0">{{ scopeNote }}</p>
      <p v-if="error" class="small text-bad m-0" role="alert">{{ error }}</p>
      <div class="modal-actions">
        <button class="btn" type="button" @click="emit('close')">Cancel</button>
        <button class="btn primary" type="submit" :disabled="saving">
          {{ recurring ? 'Save this date' : 'Save trip' }}
        </button>
      </div>
    </form>
  </ModalDialog>
</template>

<style scoped>
.route {
  border: 0;
  margin: 0;
  padding: 0;
  min-width: 0;
  display: grid;
  gap: var(--space-2);
}

.route legend {
  padding: 0;
  margin-bottom: var(--space-1);
  font-size: var(--text-sm);
  font-weight: var(--weight-medium);
}

.route-points {
  list-style: none;
  margin: 0;
  padding: 0;
  display: grid;
  gap: var(--space-2);
}

/* The point's letter, its field, and the buttons to move or remove it; the
   drive to the next point below. */
.route-point {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr) auto;
  column-gap: var(--space-2);
  align-items: end;
}

.route-letter {
  display: grid;
  place-items: center;
  width: 24px;
  height: 24px;
  margin-bottom: var(--space-2);
  border-radius: var(--radius-pill);
  background: var(--surface-2);
  font-size: var(--text-xs);
  font-weight: var(--weight-semibold);
}

.route-actions {
  display: flex;
}

.route-leg {
  grid-column: 2 / -1;
  display: flex;
  align-items: center;
  gap: var(--space-1);
  margin-top: var(--space-1);
}
</style>
