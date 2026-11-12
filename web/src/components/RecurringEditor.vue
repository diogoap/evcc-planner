<script setup lang="ts">
import { reactive, ref } from 'vue'
import { api } from '../api/client'
import type { DateRange, Place, Template } from '../api/types'
import AppIcon from './AppIcon.vue'
import ModalDialog from './ModalDialog.vue'

const props = defineProps<{ template?: Template; places: Place[] }>()
const emit = defineEmits<{ close: []; saved: [message: string] }>()

const days = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun']
const form = reactive({
  title: props.template?.title ?? '',
  destination: props.template?.destination ?? '',
  weekdays: [...(props.template?.weekdays ?? [1, 2, 3, 4, 5])],
  start: props.template?.start ?? '08:15',
  end: props.template?.end ?? '08:30',
  enabled: props.template?.enabled ?? true,
  exceptions: (props.template?.exceptions ?? []).map((e) => ({ ...e })) as DateRange[],
})
const error = ref('')
const saving = ref(false)

function toggleDay(d: number) {
  const i = form.weekdays.indexOf(d)
  if (i >= 0) form.weekdays.splice(i, 1)
  else form.weekdays.push(d)
}

async function save() {
  error.value = ''
  saving.value = true
  const body = {
    title: form.title,
    destination: form.destination,
    weekdays: [...form.weekdays].sort(),
    start: form.start,
    end: form.end,
    enabled: form.enabled,
    exceptions: form.exceptions.filter((e) => e.from),
  }
  try {
    if (props.template) {
      await api.updateTemplate({ ...body, id: props.template.id })
      emit('saved', 'Schedule updated; dates you edited keep their changes')
    } else {
      await api.createTemplate(body)
      emit('saved', 'Schedule added')
    }
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <ModalDialog :title="template ? 'Edit schedule' : 'Add schedule'" @close="emit('close')">
    <form class="stack gap-3" @submit.prevent="save">
      <label class="field">
        Destination
        <input
          v-model="form.destination"
          list="schedule-places"
          required
          placeholder="Saved place or address"
        />
        <datalist id="schedule-places">
          <option v-for="p in places" :key="p.id" :value="p.name">{{ p.address }}</option>
        </datalist>
      </label>
      <label class="field">
        Title
        <input v-model="form.title" placeholder="e.g. School drop-off" />
      </label>
      <fieldset class="fieldset-plain">
        <legend class="small muted mb-2">Weekdays</legend>
        <div class="row">
          <button
            v-for="(d, i) in days"
            :key="d"
            type="button"
            class="chip"
            :aria-pressed="form.weekdays.includes(i + 1)"
            @click="toggleDay(i + 1)"
          >
            {{ d }}
          </button>
        </div>
      </fieldset>
      <div class="form-grid">
        <label class="field">
          Arrive by
          <input v-model="form.start" type="time" required />
        </label>
        <label class="field">
          Until (return leaves)
          <input v-model="form.end" type="time" required />
        </label>
      </div>
      <div>
        <div class="row small muted mb-2">
          Date exceptions <span class="spacer" />
          <button class="btn sm" type="button" @click="form.exceptions.push({ from: '', to: '', note: '' })">
            Add exception
          </button>
        </div>
        <p v-if="!form.exceptions.length" class="small muted m-0">
          None. Holidays on your calendar are usually enough; add dates here to skip the schedule.
        </p>
        <div v-for="(e, i) in form.exceptions" :key="i" class="row mb-2 nowrap">
          <input v-model="e.from" type="date" aria-label="From" required />
          <input v-model="e.to" type="date" aria-label="To" />
          <input v-model="e.note" aria-label="Note" placeholder="Note" />
          <button
            class="btn sm ghost danger icon-only"
            type="button"
            aria-label="Remove"
            @click="form.exceptions.splice(i, 1)"
          >
            <AppIcon name="close" />
          </button>
        </div>
      </div>
      <label class="row small">
        <input v-model="form.enabled" type="checkbox" />
        Schedule active
      </label>
      <p v-if="error" class="small text-bad m-0" role="alert">{{ error }}</p>
      <div class="modal-actions">
        <button class="btn" type="button" @click="emit('close')">Cancel</button>
        <button class="btn primary" type="submit" :disabled="saving">Save schedule</button>
      </div>
    </form>
  </ModalDialog>
</template>
