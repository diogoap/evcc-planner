<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { api } from '../api/client'
import type { Place, Trip, TripInput } from '../api/types'
import { km } from '../lib/format'
import ModalDialog from './ModalDialog.vue'

const props = defineProps<{ trip?: Trip; date?: string; places: Place[]; defaultStayMin: number }>()
const emit = defineEmits<{ close: []; saved: [message: string] }>()

const editing = computed(() => !!props.trip)
const form = reactive({
  title: props.trip?.title ?? '',
  destination: props.trip?.destination ?? '',
  date: props.trip?.date ?? props.date ?? '',
  start: props.trip?.arriveLocal ?? '09:00',
  end: props.trip?.endLocal ?? '',
  enabled: props.trip?.enabled ?? true,
  distance: props.trip?.distanceOverrideKm != null ? String(props.trip.distanceOverrideKm) : '',
})
const error = ref('')
const saving = ref(false)

const calculated = computed(() =>
  props.trip && props.trip.distanceSource === 'route' ? km(props.trip.distanceKm) : null,
)

const scopeNote = computed(() => {
  if (props.trip?.source === 'recurring')
    return 'Changes apply to this date only. Edit the schedule under Recurring trips.'
  if (props.trip?.source === 'calendar')
    return 'Changes stay in the planner and survive calendar syncs; the calendar itself is not changed.'
  return ''
})

async function save() {
  error.value = ''
  const distance = form.distance.trim() === '' ? null : Number(form.distance)
  if (distance !== null && (!Number.isFinite(distance) || distance <= 0)) {
    error.value = 'Distance must be a positive number of kilometres.'
    return
  }
  const input: TripInput = {
    title: form.title,
    destination: form.destination,
    date: form.date,
    start: form.start,
    enabled: form.enabled,
    distanceOverrideKm: distance,
  }
  if (form.end) input.end = form.end
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
      <label class="field">
        Destination
        <input v-model="form.destination" list="places" required placeholder="Saved place or address" />
        <datalist id="places">
          <option v-for="p in places" :key="p.id" :value="p.name">{{ p.address }}</option>
        </datalist>
      </label>
      <label class="field">
        Title
        <input v-model="form.title" placeholder="Defaults to the destination" />
      </label>
      <div class="form-grid">
        <label class="field">
          Date
          <input v-model="form.date" type="date" required />
        </label>
        <label class="field">
          Arrive by
          <input v-model="form.start" type="time" required />
        </label>
        <label class="field">
          Until (return leaves)
          <input v-model="form.end" type="time" :placeholder="`${defaultStayMin} min stay`" />
        </label>
      </div>
      <label class="field">
        Round-trip distance (km)
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
      <p v-if="!editing && !form.end" class="small muted m-0">
        Without an end time the return leaves {{ defaultStayMin }} minutes after arrival.
      </p>
      <p v-if="scopeNote" class="small muted m-0">{{ scopeNote }}</p>
      <p v-if="error" class="small text-bad m-0" role="alert">{{ error }}</p>
      <div class="modal-actions">
        <button class="btn" type="button" @click="emit('close')">Cancel</button>
        <button class="btn primary" type="submit" :disabled="saving">
          {{ trip?.source === 'recurring' ? 'Save this date' : 'Save trip' }}
        </button>
      </div>
    </form>
  </ModalDialog>
</template>
