<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { api } from '../api/client'
import type { Place, Trip, TripSource } from '../api/types'
import TripEditor from '../components/TripEditor.vue'
import TripRow from '../components/TripRow.vue'
import { dateTime, dayLong, km, kwh } from '../lib/format'
import { mutate, notify, refresh, state } from '../store'

const route = useRoute()
const plan = computed(() => state.plan)
const source = ref<'' | TripSource>('')
const places = ref<Place[]>([])
const editor = ref<{ trip?: Trip; date?: string } | null>(null)

const days = computed(() => {
  const p = plan.value
  if (!p) return []
  return p.days.map((d) => ({
    day: d,
    trips: p.trips.filter((t) => t.date === d.date && (!source.value || t.source === source.value)),
  }))
})

onMounted(async () => {
  places.value = await api.places().catch(() => [])
})

// Scroll to a day after the plan has rendered (links use #day-YYYY-MM-DD).
watch(
  () => [route.hash, !!plan.value],
  async () => {
    if (!route.hash || !plan.value) return
    await nextTick()
    document.querySelector(route.hash)?.scrollIntoView({ block: 'start' })
  },
  { immediate: true },
)

// The SoC target belongs to a departure: show it on its first trip.
function departureTarget(t: Trip) {
  const b = t.blockIndex != null ? plan.value?.blocks[t.blockIndex] : undefined
  if (!b || b.tripKeys[0] !== t.key || b.status === 'done' || b.status === 'in_progress') return undefined
  return b.targetSoc
}

function toggle(t: Trip, enabled: boolean) {
  mutate(
    () => api.updateTrip(t.key, { enabled }),
    enabled ? `${t.title} enabled` : `${t.title} disabled for ${t.date}`,
  )
}

function review(t: Trip) {
  mutate(() => api.updateTrip(t.key, { reviewed: true }), 'Marked as a car trip')
}

function reset(t: Trip) {
  mutate(() => api.resetTrip(t.key), 'Local edits removed')
}

function remove(t: Trip) {
  if (!confirm(`Delete "${t.title}"?`)) return
  mutate(() => api.deleteTrip(t.key), 'Trip deleted')
}

function onSaved(message: string) {
  editor.value = null
  notify(message)
  refresh()
}
</script>

<template>
  <main class="page">
    <div class="page-head">
      <h1>Trips</h1>
      <div class="row">
        <select v-model="source" aria-label="Filter by source" style="width: auto">
          <option value="">All sources</option>
          <option value="calendar">Calendar</option>
          <option value="recurring">Recurring</option>
          <option value="manual">Manual</option>
        </select>
        <button class="btn primary" type="button" @click="editor = { date: plan?.days[1]?.date }">
          + Add trip
        </button>
      </div>
    </div>

    <div v-if="!plan" class="muted">Loading…</div>
    <template v-else>
      <div v-for="w in plan.warnings" :key="w" class="banner tone-warn" role="status">{{ w }}</div>
      <div class="stack">
        <section v-for="{ day, trips } in days" :id="`day-${day.date}`" :key="day.date" class="card">
          <div class="card-title">
            <span>{{ dayLong(day.date) }}</span>
            <span class="num" style="letter-spacing: 0"
              >{{ km(day.distanceKm) }} · {{ kwh(day.energyKWh) }}</span
            >
          </div>
          <ul v-if="trips.length" style="margin: 0; padding: 0">
            <TripRow
              v-for="t in trips"
              :key="t.key"
              :trip="t"
              :target="departureTarget(t)"
              @toggle="toggle(t, $event)"
              @edit="editor = { trip: t }"
              @review="review(t)"
              @reset="reset(t)"
              @remove="remove(t)"
            />
          </ul>
          <p v-else class="muted small" style="margin: 0">
            {{ day.doneCount && !source ? 'No more trips.' : 'No trips.' }}
          </p>
        </section>
      </div>

      <details v-if="plan.ignored.length" class="card" style="margin-top: 16px">
        <summary class="small">Calendar events not counted as trips ({{ plan.ignored.length }})</summary>
        <ul class="small" style="margin: 10px 0 0; padding-left: 18px">
          <li v-for="e in plan.ignored" :key="e.key">
            {{ dateTime(e.start) }} · {{ e.title }}
            <span class="muted">— {{ e.reason }}{{ e.location ? ` (${e.location})` : '' }}</span>
          </li>
        </ul>
      </details>
    </template>

    <TripEditor
      v-if="editor && plan"
      :trip="editor.trip"
      :date="editor.date"
      :places="places"
      :default-stay-min="plan.settings.defaultStayMin"
      @close="editor = null"
      @saved="onSaved"
    />
  </main>
</template>
