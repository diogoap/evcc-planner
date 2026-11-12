<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api } from '../api/client'
import type { AdviceSettings } from '../api/types'
import { notify, refresh } from '../store'
import ToggleSwitch from './ToggleSwitch.vue'

const settings = ref<AdviceSettings | null>(null)
const saving = ref(false)
const error = ref('')

const targets: { key: 'targetLittle' | 'targetMedium' | 'targetHigh'; label: string }[] = [
  { key: 'targetLittle', label: 'Little' },
  { key: 'targetMedium', label: 'Medium' },
  { key: 'targetHigh', label: 'High (more)' },
]

// Automatic plans need every policy field; the server checks the same.
const policyComplete = computed(() => {
  const s = settings.value
  return (
    !!s &&
    s.targetLittle != null &&
    s.targetMedium != null &&
    s.targetHigh != null &&
    !!s.readyBy &&
    s.expiryHours != null
  )
})

onMounted(async () => {
  try {
    settings.value = await api.adviceSettings()
  } catch (e) {
    error.value = (e as Error).message
  }
})

// Empty number inputs become null rather than 0.
function num(v: unknown) {
  return v === '' || v == null ? null : Number(v)
}

async function save() {
  const s = settings.value
  if (!s) return
  saving.value = true
  error.value = ''
  try {
    settings.value = await api.saveAdviceSettings({
      ...s,
      targetLittle: num(s.targetLittle),
      targetMedium: num(s.targetMedium),
      targetHigh: num(s.targetHigh),
      expiryHours: num(s.expiryHours),
    })
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
    <form v-if="settings" class="stack gap-4" @submit.prevent="save">
      <div class="row">
        <ToggleSwitch v-model="settings.enabled" label="Daily advice" data-testid="advice-enabled" />
        <strong>Daily advice</strong>
      </div>
      <p class="field-hint m-0 mt-n2">
        One recommendation per day (charge ahead, defer, or nothing) from published prices, the weekly price
        outlook, KNMI weather, and your trips. It never changes EVCC by itself.
      </p>

      <div class="row">
        <ToggleSwitch
          v-model="settings.autoApply"
          label="Automatically create plans from daily advice"
          :disabled="!policyComplete && !settings.autoApply"
          data-testid="advice-auto"
        />
        <strong>Automatically create plans from daily advice</strong>
      </div>
      <p class="field-hint m-0 mt-n2" data-testid="advice-auto-hint">
        Off by default: you review a proposal and confirm each change. Automatic mode acts only on today's
        advice with enough confidence, keeps every trip target, skips plans you set in EVCC, and restores the
        previous setting at expiry.
        <template v-if="!policyComplete"
          >Set a target for each indicator, a ready-by time, and an expiry first.</template
        >
      </p>

      <fieldset class="stack panel gap-3">
        <legend class="small muted">Automatic plan policy</legend>
        <div class="form-grid">
          <label v-for="t in targets" :key="t.key" class="field">
            Target for “{{ t.label }}”
            <span class="unit">
              <input
                v-model="settings[t.key]"
                type="number"
                min="10"
                max="100"
                :data-testid="`advice-${t.key}`"
              />
              <span>%</span>
            </span>
          </label>
        </div>
        <span class="field-hint mt-n2">
          The indicator is qualitative; these SoC targets are yours to choose. A trip's target is used when it
          is higher.
        </span>
        <div class="form-grid">
          <label class="field">
            Ready by (on the advice day)
            <input v-model="settings.readyBy" type="time" data-testid="advice-readyBy" />
            <span class="field-hint">Moved earlier when the next departure is sooner.</span>
          </label>
          <label class="field">
            Expiry after ready-by
            <span class="unit">
              <input
                v-model="settings.expiryHours"
                type="number"
                min="0"
                max="48"
                data-testid="advice-expiryHours"
              />
              <span>h</span>
            </span>
            <span class="field-hint">Then the previous EVCC plan is restored.</span>
          </label>
          <label class="field">
            Minimum confidence
            <select v-model.number="settings.autoMinConfidence">
              <option :value="3">3 — High</option>
              <option :value="2">2 — Medium</option>
            </select>
            <span class="field-hint">Low-confidence advice never acts.</span>
          </label>
        </div>
        <label class="row small">
          <input v-model="settings.autoPostpone" type="checkbox" />
          Also postpone cheap-grid charging when today's advice is to defer (until the cheaper day)
        </label>
      </fieldset>
      <div><button class="btn primary" type="submit" :disabled="saving">Save advice settings</button></div>
    </form>
  </section>
</template>
