<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { api } from '../api/client'
import type { Block, Preview, SendOption, Settings } from '../api/types'
import { clock, dateTime, duration, km, kwh, localDate, zonedToISO } from '../lib/format'
import { dayName, sendOptionLabel } from '../lib/status'
import ModalDialog from './ModalDialog.vue'
import PriceChart from './PriceChart.vue'

// option: a target covering more days of trips instead of the next
// departure's own; it is ready by the same departure.
const props = defineProps<{ block: Block; settings: Settings; option?: SendOption; now?: string }>()
const emit = defineEmits<{ close: []; sent: [message: string] }>()

const form = reactive({
  soc: props.option?.targetSoc ?? props.block.targetSoc,
  date: localDate(props.option?.readyBy ?? props.block.departAt),
  time: clock(props.option?.readyBy ?? props.block.departAt),
})
const title = computed(() => {
  if (!props.option || !props.now) return `Send plan to EVCC — ${props.block.firstTitle}`
  const label = sendOptionLabel(props.option, props.now)
  return `Send plan to EVCC — ${label.charAt(0).toLowerCase()}${label.slice(1)}`
})
const through = computed(() => (props.option && props.now ? dayName(props.option.through, props.now) : ''))
const preview = ref<Preview | null>(null)
const loading = ref(false)
const sending = ref(false)
const error = ref('')

const readyBy = computed(() => (form.date && form.time ? zonedToISO(form.date, form.time) : ''))

// Planner estimate for the chosen target (battery energy, grid energy incl.
// losses, and time at the configured charging power).
const estimate = computed(() => {
  const s = props.settings
  const start = props.block.startSoc
  if (start == null) return null
  const kwhBattery = (Math.max(0, form.soc - start) / 100) * s.usableCapacityKWh
  const eff = s.chargingEfficiencyPct / 100
  return {
    battery: kwhBattery,
    grid: kwhBattery / eff,
    minutes: Math.ceil((kwhBattery / (s.chargePowerKW * eff)) * 60),
  }
})

let timer: number | undefined
async function load() {
  if (!readyBy.value || form.soc < 1 || form.soc > 100) return
  loading.value = true
  try {
    preview.value = await api.preview(form.soc, readyBy.value)
    error.value = ''
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    loading.value = false
  }
}

watch(
  () => [form.soc, readyBy.value],
  () => {
    window.clearTimeout(timer)
    timer = window.setTimeout(load, 300)
  },
)
onMounted(load)

async function send() {
  sending.value = true
  error.value = ''
  try {
    const o = props.option
    const p = await api.sendPlan(form.soc, readyBy.value, props.block.tripKeys[0] ?? '', o?.through)
    emit(
      'sent',
      `Plan sent to EVCC: ${p.soc}% by ${dateTime(p.time)}${o ? `, for trips through ${through.value}` : ''}`,
    )
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    sending.value = false
  }
}
</script>

<template>
  <ModalDialog :title="title" @close="emit('close')">
    <form class="stack" style="gap: 14px" @submit.prevent="send">
      <div class="form-grid">
        <label class="field">
          Target SoC
          <span class="unit"
            ><input
              v-model.number="form.soc"
              type="number"
              min="1"
              max="100"
              required
              data-testid="plan-soc"
            /><span>%</span></span
          >
        </label>
        <label class="field">
          Ready by (date)
          <input v-model="form.date" type="date" required />
        </label>
        <label class="field">
          Ready by (time)
          <input v-model="form.time" type="time" required />
        </label>
      </div>
      <template v-if="option">
        <p class="small muted" style="margin: 0" data-testid="plan-covers">
          Enough for {{ option.tripKeys.length }}
          {{ option.tripKeys.length === 1 ? 'trip' : 'trips' }} through {{ through }} ({{
            km(option.distanceKm)
          }}: {{ option.titles.join(', ') }}) with no charging in between, arriving home with a
          {{ settings.arrivalReservePct }}% reserve. Planner target: {{ option.targetSoc }}%; the next
          departure alone needs {{ block.targetSoc }}% if the car charges between trips.
        </p>
        <p v-if="option.exceeds" class="small text-warn" style="margin: 0">
          These trips need more than a full battery, so the target stops at 100%: the car still has to charge
          between them.
        </p>
      </template>
      <p v-else class="small muted" style="margin: 0">
        Covers {{ block.titles.join(', ') }} with a {{ settings.arrivalReservePct }}% arrival reserve. Planner
        target: {{ block.targetSoc }}%.
      </p>

      <dl class="kv small">
        <dt>Current EVCC plan</dt>
        <dd data-testid="current-plan">
          <template v-if="preview?.currentPlan">
            {{ Math.round(preview.currentPlan.soc) }}% by {{ dateTime(preview.currentPlan.time) }}
            <span class="muted">{{
              preview.currentPlan.setByPlanner ? '(set by planner)' : '(set in EVCC)'
            }}</span>
            — will be replaced
          </template>
          <template v-else>None</template>
        </dd>
        <template v-if="estimate">
          <dt>Planner estimate</dt>
          <dd>
            {{ kwh(estimate.battery) }} to battery · {{ kwh(estimate.grid) }} from grid ·
            {{ duration(estimate.minutes) }}
          </dd>
        </template>
      </dl>

      <div>
        <div class="card-title" style="margin-bottom: 6px">EVCC preview</div>
        <p v-if="loading && !preview" class="small muted">Asking EVCC…</p>
        <template v-else-if="preview?.available">
          <p class="small" style="margin: 0 0 6px">
            EVCC plans {{ duration(preview.durationMinutes) }} of charging in the highlighted slots.
          </p>
          <PriceChart :slots="preview.priceSlots" :planned="preview.slots" :height="180" />
        </template>
        <template v-else-if="preview">
          <p class="small text-warn" style="margin: 0 0 6px">{{ preview.reason }}</p>
          <PriceChart v-if="preview.priceSlots.length" :slots="preview.priceSlots" :height="160" />
        </template>
      </div>

      <p class="small muted" style="margin: 0">
        EVCC keeps one plan per vehicle. Limits and repeating plans stay unchanged; during planned slots EVCC
        charges past its charge limit when the target is higher.
      </p>
      <p v-if="error" class="small text-bad" role="alert" style="margin: 0">{{ error }}</p>
      <div class="modal-actions">
        <button class="btn" type="button" @click="emit('close')">Cancel</button>
        <button class="btn primary" type="submit" :disabled="sending || !readyBy">Confirm and send</button>
      </div>
    </form>
  </ModalDialog>
</template>
