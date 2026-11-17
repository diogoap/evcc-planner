<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { ConsumptionView, HASensor, Settings } from '../api/types'
import {
  bothUnits,
  consumptionRange,
  consumptionUnits,
  fromKWhPer100Km,
  plausible,
  toKWhPer100Km,
  unitOf,
} from '../lib/consumption'
import { ago } from '../lib/format'

// The car's consumption: set here in kWh/100 km or km/kWh (stored in
// kWh/100 km), or read from a Home Assistant sensor, whose last valid value
// the plan then uses. Each value is also shown in both units.
const settings = defineModel<Settings>({ required: true })
const props = defineProps<{
  /** Home Assistant's sensors to suggest (those in km/kWh or kWh/100 km). */
  sensors: HASensor[]
  /** What the plan uses now. */
  used: ConsumptionView | null
  now?: string
}>()

// The number in the input, in the unit chosen; the setting stays in kWh/100 km.
const entered = ref<number | ''>('')
const round = (v: number, places: number) => Math.round(v * 10 ** places) / 10 ** places
watch(
  () => [settings.value.consumptionKWhPer100Km, settings.value.consumptionUnit] as const,
  ([kwh, unit]) => {
    const typed = typeof entered.value === 'number' ? toKWhPer100Km(entered.value, unit) : NaN
    if (!(Math.abs(typed - kwh) < 0.001)) entered.value = round(fromKWhPer100Km(kwh, unit), 2)
  },
  { immediate: true },
)
function enter() {
  if (typeof entered.value === 'number' && entered.value > 0) {
    settings.value.consumptionKWhPer100Km = round(
      toKWhPer100Km(entered.value, settings.value.consumptionUnit),
      3,
    )
  }
}
const range = computed(() => consumptionRange(settings.value.consumptionUnit))

const entity = computed(() => settings.value.consumptionEntity.trim())
const sensor = computed(() => props.sensors.find((s) => s.entityId === entity.value))
// A sensor whose unit says what it reports sets the choice.
watch(entity, () => {
  const unit = sensor.value && unitOf(sensor.value.unit)
  if (unit) settings.value.consumptionEntityUnit = unit
})

const sensorText = computed(() => {
  if (!entity.value) return ''
  const unit = settings.value.consumptionEntityUnit
  const u = props.used
  if (u && u.entity === entity.value && u.unit === unit) {
    if (u.source === 'homeassistant') {
      const when = u.observedAt && props.now ? `, read ${ago(u.observedAt, props.now)}` : ''
      const kept = u.note ? ` The latest reading isn't used (${u.note}), so it keeps this value.` : ''
      return `The plan uses ${bothUnits(u.kwhPer100Km)} from this sensor${when}.${kept}`
    }
    return `Not used: ${u.note ?? 'no reading yet'}. Until it has a valid value, the plan uses the value set here.`
  }
  // Not saved yet: what the sensor reads now.
  const s = sensor.value
  if (!s) return 'Save to read this sensor.'
  const v = Number(s.state)
  if (!(v > 0)) return `Reads ${s.state || 'nothing'} now. Zero, unknown, or no reading is ignored.`
  const kwh = toKWhPer100Km(v, unit)
  const reads = `Reads ${[s.state, s.unit].filter(Boolean).join(' ')} now: ${bothUnits(kwh)}`
  return plausible(kwh)
    ? `${reads}. Save to use it.`
    : `${reads}, which is not plausible; check the unit it reports.`
})
</script>

<template>
  <fieldset class="stack panel gap-3" data-testid="consumption-setting">
    <legend class="small muted">Consumption</legend>
    <div class="form-grid">
      <label class="field">
        Set here
        <span class="unit">
          <input
            v-model.number="entered"
            type="number"
            step="any"
            :min="range[0]"
            :max="range[1]"
            required
            aria-describedby="hint-consumption"
            data-testid="setting-consumptionKWhPer100Km"
            @input="enter"
          />
          <select
            v-model="settings.consumptionUnit"
            class="unit-select"
            aria-label="Consumption unit"
            data-testid="setting-consumptionUnit"
          >
            <option v-for="u in consumptionUnits" :key="u.value" :value="u.value">{{ u.label }}</option>
          </select>
        </span>
        <span class="field-hint num" data-testid="consumption-both">{{
          bothUnits(settings.consumptionKWhPer100Km)
        }}</span>
        <span id="hint-consumption" class="field-hint">
          Average use including heating or cooling. Turns trip distance into energy; raise it in winter. Used
          unless the Home Assistant sensor has a value.
        </span>
      </label>
      <label class="field">
        Home Assistant sensor
        <input
          v-model="settings.consumptionEntity"
          list="ha-sensors-efficiency"
          placeholder="sensor.car_efficiency"
          aria-describedby="hint-consumption-entity"
          data-testid="setting-consumptionEntity"
        />
        <span id="hint-consumption-entity" class="field-hint">
          Optional: the car's consumption from Home Assistant. Its latest valid value replaces the one set
          here; a reading of zero, unknown, or out of range keeps the previous value.
        </span>
      </label>
      <label v-if="entity" class="field">
        The sensor reports
        <select v-model="settings.consumptionEntityUnit" data-testid="setting-consumptionEntityUnit">
          <option v-for="u in consumptionUnits" :key="u.value" :value="u.value">{{ u.label }}</option>
        </select>
      </label>
    </div>
    <p v-if="sensorText" class="small m-0" role="status" data-testid="consumption-sensor">{{ sensorText }}</p>
    <datalist id="ha-sensors-efficiency">
      <option
        v-for="s in sensors.filter((x) => x.kind === 'efficiency')"
        :key="s.entityId"
        :value="s.entityId"
      >
        {{ s.name || s.entityId }} ({{ s.state }} {{ s.unit }})
      </option>
    </datalist>
  </fieldset>
</template>

<style scoped>
.unit-select {
  width: auto;
  flex: none;
}
</style>
