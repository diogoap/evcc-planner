<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '../api/client'
import type { AdjustmentKind, Place, SendOption } from '../api/types'
import AdviceCard from '../components/AdviceCard.vue'
import AppIcon from '../components/AppIcon.vue'
import DaysTable from '../components/DaysTable.vue'
import EnergyCard from '../components/EnergyCard.vue'
import NextDepartureCard from '../components/NextDepartureCard.vue'
import PriceChart from '../components/PriceChart.vue'
import ProposalDialog from '../components/ProposalDialog.vue'
import SendPlanDialog from '../components/SendPlanDialog.vue'
import SocChart from '../components/SocChart.vue'
import TripEditor from '../components/TripEditor.vue'
import VehicleCard from '../components/VehicleCard.vue'
import { historyLink } from '../lib/activity'
import { dateTime } from '../lib/format'
import { aheadText, controlLabel } from '../lib/status'
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
const activeAdjustments = computed(
  () => plan.value?.advice.adjustments.filter((a) => a.status === 'active') ?? [],
)
const proposing = ref<AdjustmentKind | null>(null)
const control = computed(() =>
  plan.value ? controlLabel(plan.value.control, !!status.value?.connected) : null,
)
// Later trips the automatic target charges ahead, and the estimated slots.
const ahead = computed(() =>
  plan.value?.control.mode === 'automatic' && !plan.value.control.paused
    ? (plan.value.control.target?.ahead ?? [])
    : [],
)
const expected = computed(() => {
  const t = plan.value?.control.target
  return t && !t.covered ? t.charging : []
})

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

      <div class="grid-2">
        <VehicleCard :soc="plan.soc" :status="status" :now="plan.now" :target="plan.next?.targetSoc" />
        <EnergyCard :status="status" :now="plan.now" />
        <NextDepartureCard
          :block="plan.next"
          :now="plan.now"
          :can-send="canSend"
          :options="plan.sendOptions"
          @send="(option?: SendOption) => (sending = { option })"
        />
        <section class="card" aria-labelledby="control-title">
          <div id="control-title" class="card-title">Control</div>
          <dl class="kv">
            <dt>Trip plans</dt>
            <dd v-if="control">
              <span data-testid="trip-plans">{{ control.mode }}</span>
              <div
                v-if="control.detail"
                class="small"
                :class="`text-${control.tone}`"
                data-testid="control-detail"
              >
                {{ control.detail }}
              </div>
              <ul v-if="ahead.length" class="small muted list-indent mt-1" data-testid="ahead">
                <li v-for="a in ahead" :key="a.tripKey">{{ aheadText(a) }}</li>
              </ul>
              <button
                v-if="plan.control.paused"
                class="btn sm mt-2"
                type="button"
                data-testid="resume"
                @click="resumeAutomaticPlans"
              >
                Resume
              </button>
            </dd>
            <dt>EVCC plan</dt>
            <dd data-testid="evcc-plan">
              <template v-if="status?.plan">
                {{ Math.round(status.plan.soc) }}% by {{ dateTime(status.plan.time) }}
                <div class="small muted">
                  {{ status.plan.setByPlanner ? 'Set by planner' : 'Set in EVCC' }}
                </div>
              </template>
              <template v-else>None</template>
            </dd>
            <dt>Advice auto-apply</dt>
            <dd data-testid="auto-apply">{{ plan.advice.autoApply ? 'On' : 'Off' }}</dd>
            <template v-for="a in activeAdjustments" :key="a.id">
              <dt>{{ a.kind === 'extra_plan' ? 'Extra plan' : 'Postponed' }}</dt>
              <dd data-testid="active-adjustment">
                <template v-if="a.kind === 'extra_plan' && a.plan"
                  >{{ a.plan.soc }}% by {{ dateTime(a.plan.time) }}</template
                >
                <template v-else>Cheap charging paused</template>
                <div class="small muted">
                  Restores {{ dateTime(a.expiresAt) }}
                  <button class="btn sm ghost" type="button" @click="cancelAdjustment(a.id)">End now</button>
                </div>
              </dd>
            </template>
          </dl>
          <div class="row mt-3">
            <RouterLink class="btn" to="/settings">Settings</RouterLink>
            <RouterLink class="btn" :to="historyLink('plan', 'the EVCC plan')" data-testid="plan-history"
              >Plan history</RouterLink
            >
          </div>
        </section>
      </div>

      <section class="card mt-4" aria-labelledby="days-title">
        <div id="days-title" class="card-title">
          Next seven days
          <button class="btn sm" type="button" @click="openAdd"><AppIcon name="plus" />Add trip</button>
        </div>
        <DaysTable
          :days="plan.days"
          :advice="adviceDays"
          :selected="advice?.date"
          @select="(d) => router.push({ path: '/trips', hash: `#day-${d}` })"
          @advice="(d) => (selected = d)"
        />
        <p class="small muted m-0 mt-3">
          Plan is the SoC the day's departures need added, if each leaves with just its target; then the first
          departure and the SoC it needs.
          <template v-if="adviceDays.length">
            Daily advice compares the day's prices with later days; select it for details.
          </template>
        </p>
      </section>

      <AdviceCard
        v-if="advice"
        class="mt-4"
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

      <div class="grid-2 mt-4">
        <section class="card" aria-labelledby="soc-title">
          <div id="soc-title" class="card-title">Projected SoC</div>
          <SocChart
            v-if="plan.timeline.length"
            :timeline="plan.timeline"
            :blocks="plan.blocks"
            :from="plan.from"
            :to="plan.to"
            :reserve="plan.settings.arrivalReservePct"
          />
          <p v-else class="muted small">Shown once EVCC has read the SoC.</p>
          <p class="small muted m-0 mt-2">
            Minimum charging: each departure reaches its target, charging as soon as the car is home.
          </p>
        </section>
        <section class="card" aria-labelledby="price-title">
          <div id="price-title" class="card-title">Published prices <span>incl. VAT</span></div>
          <PriceChart
            v-if="status?.priceSlots.length"
            :slots="status.priceSlots"
            :planned="expected"
            planned-label="Expected charging (estimate)"
          />
          <p v-else class="muted small">No published prices from EVCC.</p>
          <p class="small muted m-0 mt-2">
            From EVCC; tomorrow's prices appear around 14:00.
            <template v-if="expected.length">
              Shaded: the planner's estimate of when the target charges (cheapest slots, solar first); EVCC
              picks the actual slots.
            </template>
          </p>
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
