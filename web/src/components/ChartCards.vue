<script setup lang="ts">
import { computed } from 'vue'
import type { Plan, Status } from '../api/types'
import { dayRange } from '../lib/planchart'
import { expectedCharging } from '../lib/status'
import PlanChart from './PlanChart.vue'
import PriceChart from './PriceChart.vue'

// The week's charts: SoC over prices, and the published prices in detail.
// On the Overview, or on their own page on phones when they would not fit.
// The SoC and prices chart covers the first `shownDays` days (all seven by
// default).
const props = defineProps<{ plan: Plan; status: Status | null; shownDays?: number }>()
const emit = defineEmits<{ day: [date: string] }>()

const days = computed(() => props.plan.days.slice(0, props.shownDays ?? props.plan.days.length))
// The end of the last day shown.
const to = computed(() => {
  const last = days.value[days.value.length - 1]
  return last && days.value.length < props.plan.days.length
    ? new Date(dayRange(last.date).end).toISOString()
    : props.plan.to
})

const expected = computed(() => expectedCharging(props.plan))
const adviceDays = computed(() => (props.plan.advice.enabled ? props.plan.advice.days : []))
</script>

<template>
  <div class="stack">
    <section class="card" aria-labelledby="chart-title" data-testid="chart-card">
      <div id="chart-title" class="card-title">SoC and prices <span class="normal-case">incl. VAT</span></div>
      <PlanChart
        v-if="plan.timeline.length || status?.priceSlots.length"
        :timeline="plan.timeline"
        :blocks="plan.blocks"
        :slots="status?.priceSlots ?? []"
        :solar="status?.solar ?? []"
        :charging="expected"
        :advice="adviceDays"
        :dates="days.map((d) => d.date)"
        :days="days"
        :reserve="plan.settings.arrivalReservePct"
        :now="plan.now"
        :to="to"
        @day="(d) => emit('day', d)"
      />
      <p v-else class="muted small m-0">Shown once EVCC has read the SoC or published prices.</p>
      <p class="small muted m-0 mt-3">
        The projected SoC charges as soon as the car is home; each departure leaves with its target. Shaded
        prices are the planner's estimate of when the target charges (cheapest slots, solar first); EVCC picks
        the actual slots.
      </p>
    </section>

    <section v-if="status?.priceSlots.length" class="card" aria-labelledby="prices-title">
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
