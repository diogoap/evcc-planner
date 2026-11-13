<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api } from '../api/client'
import type { Place, Trip, TripSource } from '../api/types'
import AppIcon from '../components/AppIcon.vue'
import TripEditor from '../components/TripEditor.vue'
import TripRow from '../components/TripRow.vue'
import { historyLink } from '../lib/activity'
import { dateTime, dayLong, dayShort, km, kwh } from '../lib/format'
import { departureTarget } from '../lib/status'
import { confirmTrip, deleteTrip, notify, refresh, resetTrip, setTripEnabled, state } from '../store'

const route = useRoute()
const router = useRouter()
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

function history(t: Trip) {
  router.push(historyLink(`trip:${t.key}`, `${t.title}, ${dayShort(t.date)}`))
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
        <select v-model="source" aria-label="Filter by source" class="w-auto">
          <option value="">All sources</option>
          <option value="calendar">Calendar</option>
          <option value="recurring">Recurring</option>
          <option value="manual">Manual</option>
        </select>
        <button class="btn primary" type="button" @click="editor = { date: plan?.days[1]?.date }">
          <AppIcon name="plus" />Add trip
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
            <span class="num normal-case">{{ km(day.distanceKm) }} · {{ kwh(day.energyKWh) }}</span>
          </div>
          <ul v-if="trips.length" class="m-0 p-0">
            <TripRow
              v-for="t in trips"
              :key="t.key"
              :trip="t"
              :target="plan && departureTarget(plan, t)"
              @toggle="setTripEnabled(t, $event)"
              @edit="editor = { trip: t }"
              @review="confirmTrip(t)"
              @reset="resetTrip(t)"
              @remove="deleteTrip(t)"
              @history="history(t)"
            />
          </ul>
          <p v-else class="muted small m-0">
            {{ day.doneCount && !source ? 'No more trips.' : 'No trips.' }}
          </p>
        </section>
      </div>

      <details v-if="plan.ignored.length" class="card mt-4">
        <summary class="small">Calendar events not counted as trips ({{ plan.ignored.length }})</summary>
        <ul class="small list-indent mt-3">
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
