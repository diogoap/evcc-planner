<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { api } from '../api/client'
import type { AdjustmentKind, Preview, Proposal } from '../api/types'
import { confidenceLabel, indicatorNames } from '../lib/advice'
import { clock, dateTime, dayLong, duration, localDate, zonedToISO } from '../lib/format'
import ModalDialog from './ModalDialog.vue'
import PriceChart from './PriceChart.vue'

const props = defineProps<{ date: string; kind: AdjustmentKind }>()
const emit = defineEmits<{ close: []; applied: [message: string] }>()

const proposal = ref<Proposal | null>(null)
const preview = ref<Preview | null>(null)
const error = ref('')
const saving = ref(false)
const form = reactive({
  unit: '%' as '%' | 'kWh',
  soc: 0,
  kwh: 0,
  readyDate: '',
  readyTime: '',
  expDate: '',
  expTime: '',
})

const title = computed(
  () =>
    `${props.kind === 'extra_plan' ? 'Create extra plan' : 'Review postponement'} — ${dayLong(props.date)}`,
)
const p = computed(() => proposal.value)

// The target sent to EVCC is always a SoC; kWh are converted with the
// usable capacity from the projected start.
const targetSoc = computed(() => {
  const pr = p.value
  if (form.unit === '%' || !pr || pr.startSoc == null) return form.soc
  return Math.min(100, Math.ceil(pr.startSoc + (form.kwh / pr.capacityKWh) * 100 - 1e-6))
})
const readyBy = computed(() =>
  form.readyDate && form.readyTime ? zonedToISO(form.readyDate, form.readyTime) : '',
)
const expiresAt = computed(() => (form.expDate && form.expTime ? zonedToISO(form.expDate, form.expTime) : ''))

function setTime(iso: string | undefined, which: 'ready' | 'exp') {
  if (!iso) return
  if (which === 'ready') {
    form.readyDate = localDate(iso)
    form.readyTime = clock(iso)
  } else {
    form.expDate = localDate(iso)
    form.expTime = clock(iso)
  }
}

onMounted(async () => {
  try {
    const pr = await api.proposal(props.date, props.kind)
    proposal.value = pr
    form.soc = pr.soc ?? 0
    if (pr.startSoc != null && pr.soc) {
      form.kwh = Math.round(((pr.soc - pr.startSoc) / 100) * pr.capacityKWh * 10) / 10
    }
    setTime(pr.readyBy, 'ready')
    setTime(pr.expiresAt, 'exp')
  } catch (e) {
    error.value = (e as Error).message
  }
})

let timer: number | undefined
watch(
  () => [targetSoc.value, readyBy.value],
  () => {
    if (props.kind !== 'extra_plan' || !p.value?.available) return
    window.clearTimeout(timer)
    timer = window.setTimeout(async () => {
      if (!readyBy.value || targetSoc.value < 1 || targetSoc.value > 100) return
      try {
        preview.value = await api.preview(targetSoc.value, readyBy.value)
      } catch {
        preview.value = null
      }
    }, 300)
  },
)

async function confirm() {
  saving.value = true
  error.value = ''
  try {
    const a = await api.applyAdjustment({
      kind: props.kind,
      date: props.date,
      soc: props.kind === 'extra_plan' ? targetSoc.value : undefined,
      readyBy: props.kind === 'extra_plan' ? readyBy.value : undefined,
      expiresAt: expiresAt.value,
    })
    emit(
      'applied',
      a.kind === 'extra_plan'
        ? `Extra plan sent to EVCC: ${a.plan?.soc}% by ${dateTime(a.plan!.time)}`
        : `Cheap-grid charging paused until ${dateTime(a.expiresAt)}`,
    )
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <ModalDialog :title="title" @close="emit('close')">
    <p v-if="!p && !error" class="muted small">Loading the proposal…</p>
    <template v-if="p">
      <p v-if="p.advice" class="small m-0 mb-3">
        <template v-if="p.advice.indicator"
          >Charge indicator: <strong>{{ indicatorNames[p.advice.indicator] }}</strong> ·
        </template>
        Confidence: {{ confidenceLabel(p.advice.confidence) }}
      </p>
      <p v-if="!p.available" class="banner tone-warn small" data-testid="proposal-unavailable">
        {{ p.reason }}
      </p>

      <form v-else class="stack gap-3" @submit.prevent="confirm">
        <template v-if="kind === 'extra_plan'">
          <div class="form-grid">
            <label class="field">
              Target
              <span class="unit">
                <input
                  v-if="form.unit === '%'"
                  v-model.number="form.soc"
                  type="number"
                  :min="p.minSoc || 1"
                  max="100"
                  required
                  data-testid="proposal-soc"
                />
                <input
                  v-else
                  v-model.number="form.kwh"
                  type="number"
                  step="0.1"
                  min="0"
                  required
                  data-testid="proposal-kwh"
                />
                <select
                  v-model="form.unit"
                  aria-label="Target unit"
                  class="w-auto"
                  :disabled="p.startSoc == null"
                >
                  <option value="%">% SoC</option>
                  <option value="kWh">kWh to add</option>
                </select>
              </span>
              <span v-if="form.unit === 'kWh'" class="field-hint">≈ {{ targetSoc }}% SoC</span>
            </label>
            <label class="field">
              Ready by (date)
              <input v-model="form.readyDate" type="date" required />
            </label>
            <label class="field">
              Ready by (time)
              <input v-model="form.readyTime" type="time" required data-testid="proposal-ready" />
            </label>
            <label class="field">
              Expires (date)
              <input v-model="form.expDate" type="date" required />
            </label>
            <label class="field">
              Expires (time)
              <input v-model="form.expTime" type="time" required />
            </label>
          </div>
          <p v-if="p.protected" class="small muted m-0" data-testid="proposal-protected">
            {{ p.protected.title }} leaves {{ dateTime(p.protected.departAt) }} and needs
            {{ p.protected.targetSoc }}%: the target stays at least that, ready before it leaves.
          </p>
          <dl class="kv small">
            <dt>Current EVCC plan</dt>
            <dd>
              <template v-if="p.currentPlan"
                >{{ Math.round(p.currentPlan.soc) }}% by {{ dateTime(p.currentPlan.time) }} — replaced until
                expiry</template
              >
              <template v-else>None</template>
            </dd>
            <template v-if="p.startSoc != null">
              <dt>Projected start</dt>
              <dd>{{ Math.round(p.startSoc) }}%</dd>
            </template>
          </dl>
          <div>
            <div class="card-title mb-2">EVCC preview</div>
            <template v-if="preview?.available">
              <p class="small m-0 mb-2">
                EVCC plans {{ duration(preview.durationMinutes) }} of charging in the highlighted slots.
              </p>
              <PriceChart :slots="preview.priceSlots" :planned="preview.slots" :height="170" />
            </template>
            <p v-else-if="preview" class="small text-warn m-0">{{ preview.reason }}</p>
            <p v-else class="small muted m-0">Asking EVCC…</p>
          </div>
        </template>

        <template v-else>
          <dl class="kv small">
            <dt>Cheap-grid charging</dt>
            <dd>At or below €{{ p.smartCostLimit?.toFixed(2) }}/kWh → paused</dd>
          </dl>
          <div class="form-grid">
            <label class="field">
              Until (date)
              <input v-model="form.expDate" type="date" required />
            </label>
            <label class="field">
              Until (time)
              <input v-model="form.expTime" type="time" required data-testid="proposal-until" />
            </label>
          </div>
        </template>

        <ul class="small muted list-indent">
          <li v-for="n in p.notes" :key="n">{{ n }}</li>
        </ul>
        <p v-if="error" class="small text-bad m-0" role="alert">{{ error }}</p>
        <div class="modal-actions">
          <button class="btn" type="button" @click="emit('close')">Cancel</button>
          <button class="btn primary" type="submit" :disabled="saving || !expiresAt">
            {{ kind === 'extra_plan' ? 'Confirm and create plan' : 'Confirm postponement' }}
          </button>
        </div>
      </form>
    </template>
    <p v-if="error && !p?.available" class="small text-bad" role="alert">{{ error }}</p>
    <div v-if="!p?.available" class="modal-actions">
      <button class="btn" type="button" @click="emit('close')">Close</button>
    </div>
  </ModalDialog>
</template>
