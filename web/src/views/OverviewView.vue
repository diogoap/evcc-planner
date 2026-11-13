<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api } from '../api/client'
import type { AdjustmentKind, Place, SendOption, Trip } from '../api/types'
import AdviceCard from '../components/AdviceCard.vue'
import AppIcon from '../components/AppIcon.vue'
import ChartCards from '../components/ChartCards.vue'
import DaysSelect from '../components/DaysSelect.vue'
import DayPanel from '../components/DayPanel.vue'
import ErrorBanner from '../components/ErrorBanner.vue'
import PageSkeleton from '../components/PageSkeleton.vue'
import ProposalDialog from '../components/ProposalDialog.vue'
import ReadyCard from '../components/ReadyCard.vue'
import SendPlanDialog from '../components/SendPlanDialog.vue'
import TripEditor from '../components/TripEditor.vue'
import WeekList from '../components/WeekList.vue'
import WeekStrip from '../components/WeekStrip.vue'
import { historyLink } from '../lib/activity'
import { dayShort } from '../lib/format'
import { chartsApart, layout, shownDays } from '../layout'
import { expectedCharging } from '../lib/status'
import { mutate, notify, refresh, refreshNow, resumeAutomaticPlans, state } from '../store'

const route = useRoute()
const router = useRouter()
const plan = computed(() => state.plan)
const status = computed(() => state.status)
// The plan being sent: the next departure's own target, or an option
// covering more days.
const sending = ref<{ option?: SendOption } | null>(null)
// A new trip (on a date) or one being edited.
const editor = ref<{ trip?: Trip; date?: string } | null>(null)
const places = ref<Place[]>([])

// Sending works without a SoC reading: EVCC applies the plan once the car is plugged in.
const canSend = computed(() => !!plan.value?.next && !!status.value?.reachable)

// The days the week shows (Days shown); the advice still covers all seven.
const days = computed(() => plan.value?.days.slice(0, shownDays.value) ?? [])
const adviceDays = computed(() => (plan.value?.advice.enabled ? plan.value.advice.days : []))
const selected = ref<string | null>(null)
// The selected day, else the first day with an action or a suggestion.
const advice = computed(
  () =>
    adviceDays.value.find((d) => d.date === selected.value) ??
    adviceDays.value.find((d) => d.action || d.adjustment) ??
    adviceDays.value.find((d) => !d.noSuggestion) ??
    adviceDays.value[0],
)
const proposing = ref<{ kind: AdjustmentKind; date: string } | null>(null)
// The planner's estimate of the charging slots for the automatic target.
const expected = computed(() => (plan.value ? expectedCharging(plan.value) : []))

// The page's height without its charts, and the charts' own height, decide
// whether the charts move to their own page on phones (see layout.ts).
const chartsBox = ref<HTMLElement | null>(null)
function measure() {
  if (!plan.value) return
  const charts = chartsBox.value ? chartsBox.value.offsetHeight + 16 : 0
  if (charts) layout.charts = charts
  layout.page = document.body.getBoundingClientRect().height - charts
}
let observer: ResizeObserver | undefined
onMounted(() => {
  observer = window.ResizeObserver ? new ResizeObserver(() => measure()) : undefined
  observer?.observe(document.body)
})
onUnmounted(() => observer?.disconnect())
watch(chartsApart, () => nextTick(measure))

// The week as columns (a tile per day) or as a list with every detail; the
// choice is remembered on this device.
type WeekView = 'columns' | 'list'
const viewKey = 'evcc-planner.week-view'
function savedView(): WeekView {
  try {
    return localStorage.getItem(viewKey) === 'list' ? 'list' : 'columns'
  } catch {
    return 'columns'
  }
}
const weekView = ref<WeekView>(savedView())
watch(weekView, (v) => {
  try {
    localStorage.setItem(viewKey, v)
  } catch {
    // Not stored (e.g. private browsing); the choice lasts until reload.
  }
})

// The day panel: its day lives in the address (?day=), so the back gesture
// closes it. Moving between days replaces that entry instead of adding one.
const panelDate = computed(() => {
  const d = route.query.day
  return typeof d === 'string' && plan.value?.days.some((x) => x.date === d) ? d : null
})
let pushed = false
// Opening a day also shows its advice in the advice card.
function openDay(date: string) {
  if (!plan.value?.days.some((d) => d.date === date)) return
  selected.value = date
  if (panelDate.value) {
    router.replace({ query: { ...route.query, day: date } })
    return
  }
  pushed = true
  router.push({ query: { ...route.query, day: date } })
}
function closeDay() {
  // Back to where the panel was opened from: this page or the Charts page.
  if (pushed || window.history.state?.back === '/charts') {
    pushed = false
    router.back()
    return
  }
  const rest = { ...route.query }
  delete rest.day
  router.replace({ query: rest })
}

async function openEditor(e: { trip?: Trip; date?: string }) {
  places.value = await api.places().catch(() => [])
  editor.value = e
}

function onSaved(message: string) {
  editor.value = null
  sending.value = null
  proposing.value = null
  notify(message)
  refresh()
}

function dismiss(date: string, undo: boolean) {
  mutate(() => api.dismiss(date, undo), undo ? 'Advice restored' : 'Advice dismissed; nothing was changed')
}

function tripHistory(t: Trip) {
  router.push(historyLink(`trip:${t.key}`, `${t.title}, ${dayShort(t.date)}`))
}

function cancelAdjustment(id: number) {
  mutate(() => api.cancelAdjustment(id), 'Ended; the previous EVCC setting is restored')
}
</script>

<template>
  <main class="page">
    <ErrorBanner
      v-if="!plan && state.error"
      :message="`Couldn't load the plan: ${state.error}`"
      @retry="refreshNow"
    />
    <PageSkeleton v-else-if="!plan" kind="overview" />
    <template v-else>
      <div v-for="w in plan.warnings" :key="w" class="banner tone-warn" role="status">{{ w }}</div>

      <div class="overview" :class="{ 'no-advice': !advice }">
        <ReadyCard
          class="ov-ready"
          :plan="plan"
          :status="status"
          :can-send="canSend"
          @send="(option?: SendOption) => (sending = { option })"
          @resume="resumeAutomaticPlans"
          @cancel="cancelAdjustment"
        />

        <section class="ov-week" aria-labelledby="week-title">
          <div class="card-title week-head">
            <h2 id="week-title" class="h-inherit">Next {{ days.length }} days</h2>
            <span class="row gap-2">
              <DaysSelect />
              <span class="segmented" role="group" aria-label="Week view">
                <button
                  type="button"
                  class="btn sm icon-only"
                  :aria-pressed="weekView === 'columns'"
                  aria-label="Columns"
                  title="Columns"
                  data-testid="week-view-columns"
                  @click="weekView = 'columns'"
                >
                  <AppIcon name="columns" />
                </button>
                <button
                  type="button"
                  class="btn sm icon-only"
                  :aria-pressed="weekView === 'list'"
                  aria-label="List"
                  title="List"
                  data-testid="week-view-list"
                  @click="weekView = 'list'"
                >
                  <AppIcon name="rows" />
                </button>
              </span>
              <button class="btn sm" type="button" @click="openEditor({ date: plan.days[1]?.date })">
                <AppIcon name="plus" />Add trip
              </button>
            </span>
          </div>
          <WeekStrip
            v-if="weekView === 'columns'"
            :days="days"
            :advice="adviceDays"
            :selected="panelDate ?? advice?.date"
            @select="openDay"
          />
          <WeekList
            v-else
            :days="days"
            :advice="adviceDays"
            :selected="panelDate ?? advice?.date"
            @select="openDay"
          />
          <p v-if="weekView === 'columns' && adviceDays.length" class="small muted m-0 mt-2 week-legend">
            <AppIcon name="bolt" /> charge ahead · <AppIcon name="clock" /> defer ·
            <AppIcon name="check" /> keep or no plan · <AppIcon name="dash" /> no data. Tap a day for its
            advice.
          </p>
        </section>

        <AdviceCard
          v-if="advice"
          class="ov-advice"
          :advice="advice"
          :day="plan.days.find((d) => d.date === advice!.date)"
          :sources="plan.advice.sources"
          :now="plan.now"
          :dates="adviceDays.map((d) => d.date)"
          @select="(d) => (selected = d)"
          @propose="(k) => (proposing = { kind: k, date: advice!.date })"
          @dismiss="(undo) => dismiss(advice!.date, undo)"
          @cancel="cancelAdjustment"
        />

        <!-- On phones the charts get their own page when they would not fit. -->
        <div v-if="!chartsApart" ref="chartsBox" class="ov-charts">
          <ChartCards :plan="plan" :status="status" :shown-days="shownDays" @day="openDay" />
        </div>
      </div>
    </template>

    <DayPanel
      v-if="panelDate && plan"
      :date="panelDate"
      :plan="plan"
      :status="status"
      :charging="expected"
      :can-send="canSend"
      @close="closeDay"
      @select="openDay"
      @add="(date) => openEditor({ date })"
      @edit="(trip) => openEditor({ trip })"
      @history="tripHistory"
      @send="(option) => (sending = { option })"
      @propose="(kind, date) => (proposing = { kind, date })"
      @dismiss="dismiss"
      @cancel="cancelAdjustment"
    />
    <SendPlanDialog
      v-if="sending && plan?.next"
      :block="plan.next"
      :settings="plan.settings"
      :option="sending.option"
      :now="plan.now"
      @close="sending = null"
      @sent="onSaved"
    />
    <ProposalDialog
      v-if="proposing"
      :date="proposing.date"
      :kind="proposing.kind"
      @close="proposing = null"
      @applied="onSaved"
    />
    <TripEditor
      v-if="editor && plan"
      :places="places"
      :trip="editor.trip"
      :date="editor.date"
      :default-stay-min="plan.settings.defaultStayMin"
      @close="editor = null"
      @saved="onSaved"
    />
  </main>
</template>

<style scoped>
/* Phones: the ready check, the week, its advice, the chart. Wide screens put
   the advice beside the ready check. */
.overview {
  display: grid;
  gap: var(--space-4);
  grid-template-columns: minmax(0, 1fr);
  grid-template-areas: 'ready' 'week' 'advice' 'charts';
}

.ov-ready {
  grid-area: ready;
}

.ov-week {
  grid-area: week;
  min-width: 0;
}

.ov-advice {
  grid-area: advice;
}

.ov-charts {
  grid-area: charts;
  min-width: 0;
}

@media (min-width: 900px) {
  .overview {
    grid-template-columns: minmax(0, 3fr) minmax(0, 2fr);
    grid-template-areas: 'ready advice' 'week week' 'charts charts';
  }

  .overview.no-advice {
    grid-template-areas: 'ready ready' 'week week' 'charts charts';
  }
}

@media (min-width: 720px) {
  .week-legend {
    display: none;
  }
}

/* Phones: the title on its own line, the controls in one row below it. */
@media (max-width: 640px) {
  .week-head {
    flex-wrap: wrap;
  }

  .week-head h2 {
    flex-basis: 100%;
  }
}
</style>
