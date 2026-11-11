<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api } from '../api/client'
import type { ActivityFilter, ActivitySource, ActivityType, ActivityUsage, AuditEvent } from '../api/types'
import ActivityItem from '../components/ActivityItem.vue'
import { groupByDay, historyLink, size, sourceLabels, typeLabels } from '../lib/activity'
import { dayLong, localDate } from '../lib/format'
import { state } from '../store'

// Filters live in the URL, so history links and reloads keep them.
const route = useRoute()
const router = useRouter()
const filter = reactive<Required<ActivityFilter>>({ type: '', source: '', from: '', to: '', subject: '' })
const label = computed(() => (typeof route.query.label === 'string' ? route.query.label : ''))
const events = ref<AuditEvent[]>([])
const next = ref<number | null>(null)
const retentionDays = ref(0)
const loading = ref(false)
const error = ref('')
const usage = ref<ActivityUsage | null>(null)
const month = ref('')

const groups = computed(() => groupByDay(events.value))
const filtered = computed(
  () => !!(filter.type || filter.source || filter.from || filter.to || filter.subject),
)
const exportFilter = computed<ActivityFilter>(() => ({ ...filter }))

function query(name: string) {
  const v = route.query[name]
  return typeof v === 'string' ? v : ''
}

async function load(more = false) {
  loading.value = true
  try {
    const page = await api.activity(exportFilter.value, more ? (next.value ?? undefined) : undefined)
    events.value = more ? [...events.value, ...page.events] : page.events
    next.value = page.next
    retentionDays.value = page.retentionDays
    error.value = ''
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    loading.value = false
  }
}

async function loadUsage() {
  try {
    usage.value = await api.activityUsage(month.value || undefined)
    if (!month.value) month.value = usage.value.month
  } catch (e) {
    error.value = (e as Error).message
  }
}

watch(
  () => route.query,
  () => {
    if (route.path !== '/activity') return
    filter.type = query('type') as ActivityType | ''
    filter.source = query('source') as ActivitySource | ''
    filter.from = query('from')
    filter.to = query('to')
    filter.subject = query('subject')
    load()
  },
  { immediate: true },
)
watch(month, (_, old) => {
  if (old) loadUsage()
})
loadUsage()

// Edits to the filter controls update the URL, which reloads the list.
function apply() {
  const q: Record<string, string> = {}
  for (const [k, v] of Object.entries(filter)) if (v) q[k] = v
  if (filter.subject && label.value) q.label = label.value
  router.replace({ path: '/activity', query: q })
}

function clearAll() {
  router.replace({ path: '/activity' })
}

function clearSubject() {
  filter.subject = ''
  apply()
}

function openHistory(subject: string, name: string) {
  router.push(historyLink(subject, name))
}

const typeOptions = Object.entries(typeLabels) as [ActivityType, string][]
const sourceOptions = Object.entries(sourceLabels) as [ActivitySource, string][]
const today = computed(() => (state.plan ? localDate(state.plan.now) : ''))
const number = (n: number) => n.toLocaleString('en-GB')
</script>

<template>
  <main class="page">
    <div class="page-head">
      <h1>Activity</h1>
      <div class="row">
        <a class="btn sm" :href="api.activityExportUrl('csv', exportFilter)" download data-testid="export-csv"
          >Export CSV</a
        >
        <a
          class="btn sm"
          :href="api.activityExportUrl('json', exportFilter)"
          download
          data-testid="export-json"
          >Export JSON</a
        >
      </div>
    </div>
    <div v-if="error" class="banner tone-bad" role="alert">{{ error }}</div>

    <section class="card" aria-labelledby="usage-title">
      <div id="usage-title" class="card-title">
        External API usage
        <input
          v-model="month"
          type="month"
          aria-label="Month"
          :max="today.slice(0, 7)"
          style="width: auto; padding: 4px 8px"
        />
      </div>
      <table v-if="usage" class="table usage">
        <thead>
          <tr>
            <th>Service</th>
            <th class="right">Calls</th>
            <th class="right">Failed</th>
            <th class="right hide-sm">Data</th>
            <th class="right hide-sm">Avg time</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="s in usage.services" :key="s.service" :data-testid="`usage-${s.service}`">
            <td>
              <strong>{{ s.name }}</strong>
              <div v-if="s.freeCalls" class="small" style="margin-top: 4px">
                <div
                  class="bar"
                  role="img"
                  :aria-label="`${s.calls} of ${s.freeCalls} free calls used`"
                  style="max-width: 220px"
                >
                  <div
                    class="bar-fill"
                    :class="{ over: s.calls > s.freeCalls }"
                    :style="{ width: `${Math.min(100, (s.calls / s.freeCalls) * 100)}%` }"
                  />
                </div>
                <span class="muted" data-testid="free-calls"
                  >{{ number(s.calls) }} of {{ number(s.freeCalls) }} free calls this month</span
                >
              </div>
              <div v-if="s.note" class="small muted">{{ s.note }}</div>
            </td>
            <td class="right num">{{ number(s.calls) }}</td>
            <td class="right num" :class="{ 'text-bad': s.failures }">{{ number(s.failures) }}</td>
            <td class="right num hide-sm">{{ size(s.bytes) }}</td>
            <td class="right num hide-sm">{{ s.calls ? `${s.avgMs} ms` : '—' }}</td>
          </tr>
        </tbody>
      </table>
      <p class="small muted" style="margin: 10px 0 0">
        Every request to an outside service is recorded (cached routes make no call). EVCC and Home Assistant
        on your network are not counted.
      </p>
    </section>

    <section class="card" style="margin-top: 16px" aria-labelledby="log-title">
      <div id="log-title" class="card-title">Activity log</div>
      <form class="filters" @submit.prevent="apply" @change="apply">
        <select v-model="filter.type" aria-label="Type">
          <option value="">All types</option>
          <option v-for="[v, l] in typeOptions" :key="v" :value="v">{{ l }}</option>
        </select>
        <select v-model="filter.source" aria-label="Source">
          <option value="">All sources</option>
          <option v-for="[v, l] in sourceOptions" :key="v" :value="v">{{ l }}</option>
        </select>
        <label class="unit small">
          <span>From</span>
          <input v-model="filter.from" type="date" :max="filter.to || today || undefined" />
        </label>
        <label class="unit small">
          <span>To</span>
          <input v-model="filter.to" type="date" :min="filter.from || undefined" :max="today || undefined" />
        </label>
        <button v-if="filtered" class="btn sm ghost" type="button" @click="clearAll">Clear filters</button>
      </form>
      <div
        v-if="filter.subject"
        class="banner tone-info small row"
        data-testid="subject-filter"
        style="margin: 12px 0 0"
      >
        <span>History of {{ label || filter.subject }}</span>
        <span class="spacer" />
        <button class="btn sm ghost" type="button" @click="clearSubject">Show all</button>
      </div>

      <p
        v-if="!loading && !events.length"
        class="muted"
        style="margin: 16px 0 0"
        data-testid="activity-empty"
      >
        No activity{{ filtered ? ' matches these filters' : ' yet' }}.
      </p>
      <section v-for="g in groups" :key="g.date" class="activity-day">
        <h3 class="small muted">{{ dayLong(g.date) }}</h3>
        <ul style="margin: 0; padding: 0">
          <ActivityItem
            v-for="e in g.events"
            :key="e.id"
            :event="e"
            :show-history="!filter.subject"
            @history="openHistory"
          />
        </ul>
      </section>
      <div v-if="next" style="margin-top: 12px">
        <button class="btn" type="button" :disabled="loading" @click="load(true)">Load more</button>
      </div>
      <p class="small muted" style="margin: 16px 0 0">
        Newest first. Entries are never edited; history older than {{ retentionDays }} days is deleted daily
        (Settings → Connections). Keys and secret calendar addresses are never recorded.
      </p>
    </section>
  </main>
</template>

<style scoped>
.filters {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
  gap: 8px;
  align-items: center;
}

.activity-day {
  margin-top: 16px;
}

.activity-day h3 {
  text-transform: uppercase;
  letter-spacing: 0.06em;
  font-weight: 650;
  margin-bottom: 2px;
}

.usage td {
  vertical-align: top;
}

.bar-fill.over {
  background: var(--bad);
}
</style>
