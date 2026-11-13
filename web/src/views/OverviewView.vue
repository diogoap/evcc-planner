<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '../api/client'
import type { AdjustmentKind, Place, SendOption } from '../api/types'
import AdviceCard from '../components/AdviceCard.vue'
import AppIcon from '../components/AppIcon.vue'
import PlanChart from '../components/PlanChart.vue'
import PriceChart from '../components/PriceChart.vue'
import ProposalDialog from '../components/ProposalDialog.vue'
import ReadyCard from '../components/ReadyCard.vue'
import SendPlanDialog from '../components/SendPlanDialog.vue'
import TripEditor from '../components/TripEditor.vue'
import WeekList from '../components/WeekList.vue'
import WeekStrip from '../components/WeekStrip.vue'
import { mutate, notify, refresh, resumeAutomaticPlans, state } from '../store'

const router = useRouter()
const plan = computed(() => state.plan)
const status = computed(() => state.status)
// The plan being sent: the next departure's own target, or an option
// covering more days.
const sending = ref<{ option?: SendOption } | null>(null)
const adding = ref(false)
const places = ref<Place[]>([])

// Sending works without a SoC reading: EVCC applies the plan once the car is plugged in.
const canSend = computed(() => !!plan.value?.next && !!status.value?.reachable)

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
const proposing = ref<AdjustmentKind | null>(null)
// The planner's estimate of the charging slots for the automatic target.
const expected = computed(() => {
  const t = plan.value?.control.target
  return t && !t.covered ? t.charging : []
})

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

// A day shows its advice; without advice it opens in Trips.
function select(date: string) {
  if (adviceDays.value.some((d) => d.date === date)) selected.value = date
  else router.push({ path: '/trips', hash: `#day-${date}` })
}

async function openAdd() {
  places.value = await api.places().catch(() => [])
  adding.value = true
}

function onSaved(message: string) {
  adding.value = false
  sending.value = null
  proposing.value = null
  notify(message)
  refresh()
}

function dismiss(undo: boolean) {
  const a = advice.value
  if (!a) return
  mutate(() => api.dismiss(a.date, undo), undo ? 'Advice restored' : 'Advice dismissed; nothing was changed')
}

function cancelAdjustment(id: number) {
  mutate(() => api.cancelAdjustment(id), 'Ended; the previous EVCC setting is restored')
}
</script>

<template>
  <main class="page">
    <div v-if="!plan" class="muted">Loading the plan…</div>
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
          <div class="card-title">
            <h2 id="week-title" class="h-inherit">This week</h2>
            <span class="row gap-2">
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
              <button class="btn sm" type="button" @click="openAdd"><AppIcon name="plus" />Add trip</button>
            </span>
          </div>
          <WeekStrip
            v-if="weekView === 'columns'"
            :days="plan.days"
            :advice="adviceDays"
            :selected="advice?.date"
            @select="select"
          />
          <WeekList v-else :days="plan.days" :advice="adviceDays" :selected="advice?.date" @select="select" />
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
          @propose="(k) => (proposing = k)"
          @dismiss="dismiss"
          @cancel="cancelAdjustment"
        />

        <section class="card ov-chart" aria-labelledby="chart-title">
          <div id="chart-title" class="card-title">
            SoC and prices <span class="normal-case">incl. VAT</span>
          </div>
          <PlanChart
            v-if="plan.timeline.length || status?.priceSlots.length"
            :timeline="plan.timeline"
            :blocks="plan.blocks"
            :slots="status?.priceSlots ?? []"
            :solar="status?.solar ?? []"
            :charging="expected"
            :advice="adviceDays"
            :dates="plan.days.map((d) => d.date)"
            :reserve="plan.settings.arrivalReservePct"
            :now="plan.now"
            :to="plan.to"
          />
          <p v-else class="muted small m-0">Shown once EVCC has read the SoC or published prices.</p>
          <p class="small muted m-0 mt-3">
            The projected SoC charges as soon as the car is home; each departure leaves with its target.
            Shaded prices are the planner's estimate of when the target charges (cheapest slots, solar first);
            EVCC picks the actual slots.
          </p>
        </section>

        <section v-if="status?.priceSlots.length" class="card ov-prices" aria-labelledby="prices-title">
          <div id="prices-title" class="card-title">
            Published prices <span class="normal-case">incl. VAT</span>
          </div>
          <PriceChart
            :slots="status.priceSlots"
            :planned="expected"
            planned-label="Expected charging"
            :now="plan.now"
            :blocks="plan.blocks"
            :height="200"
          />
          <p class="small muted m-0 mt-2">From EVCC; tomorrow's prices appear around 14:00.</p>
        </section>
      </div>
    </template>

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
      v-if="proposing && advice"
      :date="advice.date"
      :kind="proposing"
      @close="proposing = null"
      @applied="onSaved"
    />
    <TripEditor
      v-if="adding && plan"
      :places="places"
      :date="plan.days[1]?.date"
      :default-stay-min="plan.settings.defaultStayMin"
      @close="adding = false"
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
  grid-template-areas: 'ready' 'week' 'advice' 'chart' 'prices';
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

.ov-chart {
  grid-area: chart;
}

.ov-prices {
  grid-area: prices;
}

@media (min-width: 900px) {
  .overview {
    grid-template-columns: minmax(0, 3fr) minmax(0, 2fr);
    grid-template-areas: 'ready advice' 'week week' 'chart chart' 'prices prices';
  }

  .overview.no-advice {
    grid-template-areas: 'ready ready' 'week week' 'chart chart' 'prices prices';
  }
}

@media (min-width: 720px) {
  .week-legend {
    display: none;
  }
}
</style>
