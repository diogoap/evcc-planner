<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api } from '../api/client'
import type { ActivityFilter, ActivitySource, ActivityType, ActivityUsage, AuditEvent } from '../api/types'
import ActivityItem from '../components/ActivityItem.vue'
import AppIcon from '../components/AppIcon.vue'
import EmptyState from '../components/EmptyState.vue'
import ErrorBanner from '../components/ErrorBanner.vue'
import PageSkeleton from '../components/PageSkeleton.vue'
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
// Phones fold the filters away behind a button that counts the active ones.
const showFilters = ref(false)
const activeFilters = computed(
  () => [filter.type, filter.source, filter.from, filter.to].filter(Boolean).length,
)
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

// Retry after an error: the log and the usage table.
function reload() {
  load()
  loadUsage()
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
    <ErrorBanner v-if="error" :message="error" @retry="reload" />

    <section class="card" aria-labelledby="usage-title">
      <div id="usage-title" class="card-title">
        External API usage
        <input v-model="month" type="month" aria-label="Month" :max="today.slice(0, 7)" class="w-auto" />
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
              <div v-if="s.freeCalls" class="small mt-1">
                <div
                  class="bar usage-bar"
                  role="img"
                  :aria-label="`${s.calls} of ${s.freeCalls} free calls used`"
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
      <p class="small muted m-0 mt-3">
        Every request to an outside service is recorded (cached routes make no call). EVCC and Home Assistant
        on your network are not counted.
      </p>
    </section>

    <section class="card mt-4" aria-labelledby="log-title">
      <div id="log-title" class="card-title">
        Activity log
        <button
          class="btn sm filters-toggle normal-case"
          type="button"
          :aria-expanded="showFilters"
          aria-controls="activity-filters"
          @click="showFilters = !showFilters"
        >
          <AppIcon name="sliders" />Filters<template v-if="activeFilters"> ({{ activeFilters }})</template>
        </button>
      </div>
      <form
        id="activity-filters"
        class="filters"
        :class="{ folded: !showFilters }"
        @submit.prevent="apply"
        @change="apply"
      >
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
      <div v-if="filter.subject" class="banner tone-info small row m-0 mt-3" data-testid="subject-filter">
        <span>History of {{ label || filter.subject }}</span>
        <span class="spacer" />
        <button class="btn sm ghost" type="button" @click="clearSubject">Show all</button>
      </div>

      <div v-if="!loading && !events.length && !error" data-testid="activity-empty">
        <EmptyState
          v-if="filtered"
          icon="list"
          title="No activity matches these filters"
          text="Try another type, source, or dates."
        >
          <button class="btn" type="button" @click="clearAll">Clear filters</button>
        </EmptyState>
        <EmptyState
          v-else
          icon="list"
          title="No activity yet"
          text="Trip edits, plans sent to EVCC, settings changes, and calls to outside services appear here."
        />
      </div>
      <PageSkeleton v-if="loading && !events.length" kind="list" />
      <section v-for="g in groups" :key="g.date" class="activity-day">
        <h3 class="small muted">{{ dayLong(g.date) }}</h3>
        <ul class="m-0 p-0">
          <ActivityItem
            v-for="e in g.events"
            :key="e.id"
            :event="e"
            :show-history="!filter.subject"
            @history="openHistory"
          />
        </ul>
      </section>
      <div v-if="next" class="mt-3">
        <button class="btn" type="button" :disabled="loading" @click="load(true)">Load more</button>
      </div>
      <p class="small muted m-0 mt-4">
        Newest first. Entries are never edited; history older than {{ retentionDays }} days is deleted daily
        (Settings → Connections). Keys and secret calendar addresses are never recorded.
      </p>
    </section>
  </main>
</template>

<style scoped>
/* Wide screens always show the filters; phones fold them. */
.filters-toggle {
  display: none;
}

@media (max-width: 640px) {
  .filters-toggle {
    display: inline-flex;
  }

  .filters.folded {
    display: none;
  }
}

.usage-bar {
  max-width: 220px;
}

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
