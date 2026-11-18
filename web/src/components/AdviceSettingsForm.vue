<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { api } from '../api/client'
import type { AdviceSettings, ExtraLevel, TripPlans } from '../api/types'
import { useUnsaved } from '../lib/unsaved'
import { notify, refresh } from '../store'
import SaveBar from './SaveBar.vue'
import ToggleSwitch from './ToggleSwitch.vue'

// mode: the saved trip plan mode, which decides what applies.
const props = defineProps<{ mode?: TripPlans }>()
const settings = ref<AdviceSettings | null>(null)
const emit = defineEmits<{ dirty: [dirty: boolean] }>()
const unsaved = useUnsaved(settings)
watch(unsaved.dirty, (d) => emit('dirty', d))
const saving = ref(false)
const error = ref('')

// Charge extra per indication of cheaper days ahead: the more the coming
// days look cheaper, the bigger the discount needed and the less it charges.
const levels: {
  key: 'extraLittle' | 'extraMedium' | 'extraHigh'
  name: string
  label: string
  hint: string
}[] = [
  {
    key: 'extraLittle',
    name: 'little',
    label: 'Little',
    hint: 'No or weak signs that the coming days are cheaper.',
  },
  {
    key: 'extraMedium',
    name: 'medium',
    label: 'Medium',
    hint: 'The outlook expects a cheaper day, or wind or sun ahead.',
  },
  { key: 'extraHigh', name: 'high', label: 'High', hint: 'A cheaper day ahead, with wind or sun as well.' },
]
const level = (key: (typeof levels)[number]['key']): ExtraLevel => settings.value![key]

onMounted(async () => {
  try {
    settings.value = await api.adviceSettings()
    unsaved.mark()
  } catch (e) {
    error.value = (e as Error).message
  }
})

async function save() {
  const s = settings.value
  if (!s) return
  saving.value = true
  error.value = ''
  try {
    settings.value = await api.saveAdviceSettings(s)
    unsaved.mark()
    notify('Advice settings saved')
    refresh()
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <section class="card">
    <div class="card-title">Daily advice</div>
    <div v-if="error" class="banner tone-bad" role="alert">{{ error }}</div>
    <p v-if="props.mode === 'ai'" class="banner tone-info small" role="status" data-testid="advice-ai-off">
      AI mode is on, so these settings don't apply: the AI decides the daily advice and how much to charge
      extra, and acting on it automatically is set under AI. They apply again in the other modes (and the
      steps to the automatic rules' advice shown until the first AI plan).
    </p>
    <form v-if="settings" class="stack gap-4" @submit.prevent="save">
      <fieldset class="stack fieldset-plain gap-4" :disabled="props.mode === 'ai'" data-testid="advice-form">
        <div class="row">
          <ToggleSwitch v-model="settings.enabled" label="Daily advice" data-testid="advice-enabled" />
          <strong>Daily advice</strong>
        </div>
        <p class="field-hint m-0 mt-n2">
          One recommendation per day with published prices (charge extra, defer, or nothing); later days show
          the weekly outlook. Charging has three layers: the next departure always, later trips when cheaper
          (Charge ahead, under Planning), and charge extra on top, set here. EVCC gets the higher target.
          Advice never changes EVCC by itself.
        </p>

        <fieldset class="stack panel gap-3" data-testid="extra-levels">
          <legend class="small muted">Charge extra</legend>
          <p class="field-hint m-0">
            Charges more when blocks at home are this much cheaper than tomorrow's lowest price (published;
            before that, what the weekly outlook expects, or the past 7 days' average lowest price), as far as
            those blocks can. Blocks at or below €0 count for every step. Which steps apply depends on how
            strongly the coming days look cheaper (weekly outlook, wind, sun).
          </p>
          <div v-for="l in levels" :key="l.key" class="stack gap-2" :data-testid="`extra-${l.name}`">
            <label class="row small">
              <input
                v-model="level(l.key).enabled"
                type="checkbox"
                :data-testid="`extra-${l.name}-enabled`"
              />
              <span
                ><strong>{{ l.label }}</strong> indication of cheaper days ahead
                <span class="field-hint block">{{ l.hint }}</span></span
              >
            </label>
            <div class="extra-steps" :class="{ muted: !level(l.key).enabled }">
              <div v-for="(st, i) in level(l.key).steps" :key="i" class="extra-step">
                <span class="unit">
                  <input
                    v-model.number="st.minDiscountPct"
                    type="number"
                    min="0"
                    max="100"
                    step="any"
                    :aria-label="`${l.label}, step ${i + 1}: at least this much cheaper`"
                    :data-testid="`extra-${l.name}-${i}-discount`"
                  />
                  <span>% cheaper →</span>
                </span>
                <span class="unit">
                  <input
                    v-model.number="st.targetSoc"
                    type="number"
                    min="10"
                    max="100"
                    :aria-label="`${l.label}, step ${i + 1}: charge to this SoC`"
                    :data-testid="`extra-${l.name}-${i}-target`"
                  />
                  <span>%</span>
                </span>
              </div>
            </div>
          </div>
        </fieldset>

        <fieldset
          class="stack panel gap-3"
          :disabled="props.mode === 'manual'"
          data-testid="advice-auto-settings"
        >
          <legend class="small muted">Act on the advice automatically</legend>
          <p
            v-if="props.mode === 'manual'"
            class="banner tone-info small m-0"
            data-testid="advice-auto-manual"
          >
            Not used in Manual mode: it never changes EVCC by itself, so the advice only recommends. Choose
            another mode under Planning to act on it automatically.
          </p>
          <div class="row">
            <ToggleSwitch
              v-model="settings.autoApply"
              label="Automatically create plans from daily advice"
              data-testid="advice-auto"
            />
            <strong>Automatically create plans from daily advice</strong>
          </div>
          <p class="field-hint m-0 mt-n2" data-testid="advice-auto-hint">
            Off by default: you review a proposal and confirm each change. On, it acts once a day on today's
            advice with enough confidence: an extra plan with charge extra's target (or a trip's, when
            higher), ready when its cheap hours end, after which the previous plan comes back. It skips plans
            you set in EVCC. The plan for the next trip and charge ahead come from the trip plan mode
            (Planning), not from here.
          </p>
          <div class="form-grid">
            <label class="field">
              Minimum confidence
              <select v-model.number="settings.autoMinConfidence">
                <option :value="3">3 — High</option>
                <option :value="2">2 — Medium</option>
              </select>
              <span class="field-hint"
                >3 acts only against tomorrow's published prices; 2 also against the weekly outlook or a week
                of price history. Low-confidence advice never acts.</span
              >
            </label>
          </div>
          <label class="row small">
            <input v-model="settings.autoPostpone" type="checkbox" />
            Also postpone cheap-grid charging when today's advice is to defer (until the cheaper day)
          </label>
        </fieldset>
      </fieldset>
      <SaveBar
        v-if="props.mode !== 'ai'"
        :dirty="unsaved.dirty.value"
        :saving="saving"
        label="Save advice settings"
        @discard="unsaved.discard"
      />
    </form>
  </section>
</template>

<style scoped>
.extra-steps {
  display: grid;
  gap: var(--space-2);
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 220px), 1fr));
}

.extra-step {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
}

.extra-step input {
  width: 5.5em;
}
</style>
