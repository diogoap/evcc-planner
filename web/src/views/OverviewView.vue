<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '../api/client'
import type { Place } from '../api/types'
import DaysTable from '../components/DaysTable.vue'
import EnergyCard from '../components/EnergyCard.vue'
import NextDepartureCard from '../components/NextDepartureCard.vue'
import PriceChart from '../components/PriceChart.vue'
import SendPlanDialog from '../components/SendPlanDialog.vue'
import SocChart from '../components/SocChart.vue'
import TripEditor from '../components/TripEditor.vue'
import VehicleCard from '../components/VehicleCard.vue'
import { dateTime } from '../lib/format'
import { notify, refresh, state } from '../store'

const router = useRouter()
const plan = computed(() => state.plan)
const status = computed(() => state.status)
const sending = ref(false)
const adding = ref(false)
const places = ref<Place[]>([])

// Sending works without a SoC reading: EVCC applies the plan once the car is plugged in.
const canSend = computed(() => !!plan.value?.next && !!status.value?.reachable)

async function openAdd() {
  places.value = await api.places().catch(() => [])
  adding.value = true
}

function onSaved(message: string) {
  adding.value = false
  sending.value = false
  notify(message)
  refresh()
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
        <NextDepartureCard :block="plan.next" :now="plan.now" :can-send="canSend" @send="sending = true" />
        <section class="card" aria-labelledby="control-title">
          <div id="control-title" class="card-title">Control</div>
          <dl class="kv">
            <dt>Trip plans</dt>
            <dd>Send manually</dd>
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
            <dd>Off <span class="muted small">(V2)</span></dd>
          </dl>
          <div class="row" style="margin-top: 12px">
            <RouterLink class="btn" to="/settings">Settings</RouterLink>
          </div>
        </section>
      </div>

      <section class="card" style="margin-top: 16px" aria-labelledby="days-title">
        <div id="days-title" class="card-title">
          Next seven days
          <button class="btn sm" type="button" @click="openAdd">+ Add trip</button>
        </div>
        <DaysTable :days="plan.days" @select="(d) => router.push({ path: '/trips', hash: `#day-${d}` })" />
        <p class="small muted" style="margin: 10px 0 0">Daily charging advice arrives in V2.</p>
      </section>

      <div class="grid-2" style="margin-top: 16px">
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
          <p class="small muted" style="margin: 8px 0 0">
            Minimum charging: each departure reaches its target, charging as soon as the car is home.
          </p>
        </section>
        <section class="card" aria-labelledby="price-title">
          <div id="price-title" class="card-title">Published prices <span>incl. VAT</span></div>
          <PriceChart v-if="status?.priceSlots.length" :slots="status.priceSlots" />
          <p v-else class="muted small">No published prices from EVCC.</p>
          <p class="small muted" style="margin: 8px 0 0">
            From EVCC; tomorrow's prices appear around 14:00. Estimates for later days arrive in V2.
          </p>
        </section>
      </div>
    </template>

    <SendPlanDialog
      v-if="sending && plan?.next"
      :block="plan.next"
      :settings="plan.settings"
      @close="sending = false"
      @sent="onSaved"
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
