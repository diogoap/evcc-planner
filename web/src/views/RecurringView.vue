<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { api } from '../api/client'
import type { Occurrence, Place, Template, TemplateView } from '../api/types'
import RecurringEditor from '../components/RecurringEditor.vue'
import { historyLink } from '../lib/activity'
import { dayShort, weekdays } from '../lib/format'
import { mutate, notify, refresh } from '../store'

const templates = ref<TemplateView[] | null>(null)
const places = ref<Place[]>([])
const error = ref('')
const editor = ref<{ template?: Template } | null>(null)

async function load() {
  try {
    ;[templates.value, places.value] = await Promise.all([api.templates(), api.places()])
    error.value = ''
  } catch (e) {
    error.value = (e as Error).message
  }
}
onMounted(load)

async function run(action: () => Promise<unknown>, message: string) {
  if (await mutate(action, message)) await load()
}

function strip(t: TemplateView): Template {
  return {
    id: t.id,
    title: t.title,
    destination: t.destination,
    weekdays: t.weekdays,
    start: t.start,
    end: t.end,
    enabled: t.enabled,
    exceptions: t.exceptions,
  }
}

function pause(t: TemplateView) {
  run(
    () => api.updateTemplate({ ...strip(t), enabled: !t.enabled }),
    t.enabled ? 'Schedule paused' : 'Schedule resumed',
  )
}

function remove(t: TemplateView) {
  if (!confirm(`Delete the "${t.title}" schedule and its per-date edits?`)) return
  run(() => api.deleteTemplate(t.id), 'Schedule deleted')
}

function toggleDate(t: TemplateView, o: Occurrence) {
  if (o.exception || !t.enabled) return
  run(
    () => api.updateTrip(o.key, { enabled: !o.enabled }),
    o.enabled ? `Skipped ${dayShort(o.date)}` : `Restored ${dayShort(o.date)}`,
  )
}

async function onSaved(message: string) {
  editor.value = null
  notify(message)
  await load()
  refresh()
}
</script>

<template>
  <main class="page">
    <div class="page-head">
      <h1>Recurring trips</h1>
      <button class="btn primary" type="button" @click="editor = {}">+ Add schedule</button>
    </div>
    <div v-if="error" class="banner tone-bad">{{ error }}</div>
    <div v-if="!templates" class="muted">Loading…</div>
    <p v-else-if="!templates.length" class="card muted">
      No schedules yet. Add one for regular trips such as school runs.
    </p>
    <div class="stack">
      <section v-for="t in templates" :key="t.id" class="card" :data-testid="`template-${t.id}`">
        <div class="row">
          <h2 style="font-size: 1.1rem">{{ t.title }}</h2>
          <span class="pill" :class="t.enabled ? 'tone-ok' : ''">{{ t.enabled ? 'Enabled' : 'Paused' }}</span>
        </div>
        <p class="muted" style="margin: 4px 0 10px">
          {{ weekdays(t.weekdays) }}, {{ t.start }}–{{ t.end }} · Home → {{ t.destination }} → Home
        </p>
        <p v-if="t.exceptions.length" class="small muted" style="margin: -4px 0 10px">
          Exceptions:
          {{
            t.exceptions
              .map(
                (e) =>
                  (e.to && e.to !== e.from ? `${e.from} to ${e.to}` : e.from) +
                  (e.note ? ` (${e.note})` : ''),
              )
              .join(', ')
          }}
        </p>
        <div class="small muted" style="margin-bottom: 6px">
          Next seven days (click to skip or restore a date)
        </div>
        <div class="row" style="margin-bottom: 12px">
          <button
            v-for="o in t.occurrences"
            :key="o.key"
            type="button"
            class="chip"
            :aria-pressed="o.enabled"
            :disabled="!!o.exception || !t.enabled"
            :title="o.exception || (o.overridden.length ? 'Edited for this date' : '')"
            @click="toggleDate(t, o)"
          >
            {{ dayShort(o.date) }}
            <span class="small">{{ o.exception ? `Off: ${o.exception}` : o.enabled ? 'On' : 'Off' }}</span>
            <span v-if="o.overridden.some((f) => f !== 'enabled')" aria-label="edited">✎</span>
          </button>
          <span v-if="!t.occurrences.length" class="small muted">None in the next seven days.</span>
        </div>
        <div class="row">
          <button class="btn sm" type="button" @click="editor = { template: strip(t) }">Edit schedule</button>
          <button class="btn sm" type="button" @click="pause(t)">
            {{ t.enabled ? 'Pause schedule' : 'Resume' }}
          </button>
          <button class="btn sm ghost danger" type="button" @click="remove(t)">Delete</button>
          <RouterLink class="btn sm ghost" :to="historyLink(`schedule:${t.id}`, t.title)">History</RouterLink>
        </div>
      </section>
    </div>
    <p class="small muted" style="margin-top: 16px">
      Dates appear as the seven-day window advances. Editing a schedule updates every date you have not
      changed; per-date edits are kept.
    </p>

    <RecurringEditor
      v-if="editor"
      :template="editor.template"
      :places="places"
      @close="editor = null"
      @saved="onSaved"
    />
  </main>
</template>
