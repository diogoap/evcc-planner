<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api } from '../api/client'
import type { Connection, ConnectionSettings, ConnectionSettingsView, EnvValue, HASensor } from '../api/types'
import { notify, refresh } from '../store'
import ToggleSwitch from './ToggleSwitch.vue'

const emit = defineEmits<{ saved: [connections: Connection[]] }>()

type Key = keyof ConnectionSettings
interface Field {
  key: Key
  label: string
  kind: 'text' | 'number' | 'time' | 'toggle'
  unit?: string
  hint: string
  /** Suggest Home Assistant sensors of this kind. */
  sensor?: HASensor['kind']
}
interface Group {
  title: string
  /** Environment variables shown with the group. */
  env: string[]
  fields: Field[]
}

const groups: Group[] = [
  {
    title: 'EVCC',
    env: [],
    fields: [
      {
        key: 'evccUrl',
        label: 'Address',
        kind: 'text',
        hint: 'EVCC as the planner reaches it, e.g. http://192.0.2.10:7070. EVCC_URL only sets the starting value; once saved here it is no longer needed.',
      },
      {
        key: 'evccLoadpoint',
        label: 'Loadpoint',
        kind: 'number',
        hint: "The charger's loadpoint in EVCC, from 1.",
      },
      {
        key: 'evccVehicle',
        label: 'Vehicle',
        kind: 'text',
        hint: "EVCC's vehicle name; empty uses the loadpoint's or the only vehicle.",
      },
      {
        key: 'evccPollIntervalSec',
        label: 'Poll interval',
        kind: 'number',
        unit: 's',
        hint: 'How often the planner reads EVCC (connection, SoC, prices).',
      },
      {
        key: 'evccTimeoutSec',
        label: 'Timeout',
        kind: 'number',
        unit: 's',
        hint: 'How long to wait for EVCC.',
      },
    ],
  },
  {
    title: 'Home Assistant (always-on SoC)',
    env: ['HA_TOKEN'],
    fields: [
      {
        key: 'haUrl',
        label: 'Address',
        kind: 'text',
        hint: 'Home Assistant as the planner reaches it, e.g. http://192.0.2.20:8123; HA_URL only sets the starting value. The access token is sent here, so keep the planner reachable only by people you trust.',
      },
      {
        key: 'haSocEntity',
        label: 'SoC sensor',
        kind: 'text',
        sensor: 'battery',
        hint: "The car's battery level sensor (%), e.g. sensor.leaf_battery_level. Empty: only EVCC's readings.",
      },
      {
        key: 'haRangeEntity',
        label: 'Range sensor',
        kind: 'text',
        sensor: 'range',
        hint: "Optional: the car's range sensor (km or mi).",
      },
      {
        key: 'haObservedAttribute',
        label: 'Reading-time attribute',
        kind: 'text',
        hint: "Optional: an attribute of the SoC sensor with the car's own reading time (e.g. updated_on). Otherwise the time Home Assistant received it.",
      },
      {
        key: 'haPollIntervalMin',
        label: 'Poll interval',
        kind: 'number',
        unit: 'min',
        hint: 'How often the planner reads the sensors.',
      },
      {
        key: 'haTimeoutSec',
        label: 'Timeout',
        kind: 'number',
        unit: 's',
        hint: 'How long to wait for Home Assistant.',
      },
    ],
  },
  {
    title: 'Google Calendar',
    env: ['GOOGLE_CALENDAR_ICAL_URL'],
    fields: [
      {
        key: 'calendarSyncIntervalMin',
        label: 'Sync interval',
        kind: 'number',
        unit: 'min',
        hint: 'How often the calendars are read; Sync calendar now reads them at once.',
      },
    ],
  },
  {
    title: 'Google Routes',
    env: ['GOOGLE_MAPS_API_KEY'],
    fields: [
      {
        key: 'routesTrafficAware',
        label: 'Traffic-aware routes',
        kind: 'toggle',
        hint: 'Travel times for the planned departure, billed as Compute Routes Pro.',
      },
      {
        key: 'routesCacheTtlHours',
        label: 'Route cache',
        kind: 'number',
        unit: 'h',
        hint: 'How long a route is reused per weekday and half hour before it is looked up again.',
      },
      {
        key: 'routesMonthlyFreeCalls',
        label: 'Free calls per month',
        kind: 'number',
        hint: 'Shown next to the calls made on the Activity page; 0 hides it.',
      },
    ],
  },
  {
    title: 'Daily forecasts',
    env: ['OPENROUTER_API_KEY', 'KNMI_API_KEY'],
    fields: [
      {
        key: 'tibberUrl',
        label: 'Tibber outlook page',
        kind: 'text',
        hint: 'The weekly price outlook article.',
      },
      {
        key: 'openRouterModel',
        label: 'OpenRouter model',
        kind: 'text',
        hint: 'Structures the outlook; it must support structured outputs (strict JSON Schema).',
      },
      {
        key: 'openRouterTimeoutSec',
        label: 'OpenRouter timeout',
        kind: 'number',
        unit: 's',
        hint: 'How long to wait for the model.',
      },
      { key: 'knmiDataset', label: 'KNMI dataset', kind: 'text', hint: "KNMI's seven-day outlook dataset." },
      { key: 'knmiVersion', label: 'KNMI dataset version', kind: 'text', hint: 'The dataset version.' },
      {
        key: 'forecastDailyAt',
        label: 'Daily fetch time',
        kind: 'time',
        hint: 'Local time; Tibber publishes its outlook on Mondays around 17:00.',
      },
      {
        key: 'forecastRetryIntervalMin',
        label: 'Retry interval',
        kind: 'number',
        unit: 'min',
        hint: 'How soon a failed fetch is tried again.',
      },
      {
        key: 'forecastVatPct',
        label: 'VAT',
        kind: 'number',
        unit: '%',
        hint: "Converts the outlook's prices excl. VAT; applies to the next fetch.",
      },
    ],
  },
  {
    title: 'History',
    env: [],
    fields: [
      {
        key: 'retentionDays',
        label: 'Keep history for',
        kind: 'number',
        unit: 'days',
        hint: 'Older activity, past trips, cached routes, and plan records are deleted daily.',
      },
    ],
  },
]

const view = ref<ConnectionSettingsView | null>(null)
const settings = ref<ConnectionSettings | null>(null)
const sensors = ref<HASensor[]>([])
const saving = ref(false)
const error = ref('')
const confirmLower = ref(false)

const env = computed(() => new Map((view.value?.env ?? []).map((e) => [e.env, e])))
const lowersRetention = computed(
  () => !!settings.value && !!view.value && settings.value.retentionDays < view.value.settings.retentionDays,
)

onMounted(async () => {
  try {
    view.value = await api.connectionSettings()
    settings.value = { ...view.value.settings }
  } catch (e) {
    error.value = (e as Error).message
  }
  // Suggestions only: without a Home Assistant token there are none.
  sensors.value = await api.homeAssistantSensors().catch(() => [])
})

function defaultText(f: Field) {
  const d = view.value?.defaults[f.key]
  if (d === undefined || d === '') return 'Default: none.'
  if (typeof d === 'boolean') return `Default: ${d ? 'on' : 'off'}.`
  return `Default: ${d}${f.unit ? ` ${f.unit}` : ''}.`
}

function envText(e: EnvValue | undefined, name: string) {
  if (!e) return `${name}: —`
  return `${e.env}: ${e.set ? 'set ✓' : 'not set'}`
}

function setField<K extends Key>(key: K, value: ConnectionSettings[K]) {
  if (settings.value) settings.value[key] = value
}

function resetToDefaults() {
  if (view.value) settings.value = { ...view.value.defaults }
}

async function save() {
  const s = settings.value
  if (!s) return
  if (lowersRetention.value && !confirmLower.value) {
    confirmLower.value = true
    return
  }
  saving.value = true
  error.value = ''
  try {
    const saved = await api.saveConnectionSettings(s)
    view.value = saved
    settings.value = { ...saved.settings }
    confirmLower.value = false
    emit('saved', saved.connections ?? [])
    notify('Connection settings saved')
    refresh()
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <section class="card" aria-labelledby="connection-settings-title">
    <div id="connection-settings-title" class="card-title">Connection settings</div>
    <div v-if="error" class="banner tone-bad" role="alert">{{ error }}</div>
    <form v-if="settings" class="stack" style="gap: 16px" @submit.prevent="save">
      <p class="small muted" style="margin: 0">
        Saved in the planner's database and applied at once. API keys, the Home Assistant token, and the
        calendar addresses stay in environment variables on the server.
      </p>
      <fieldset
        v-for="g in groups"
        :key="g.title"
        class="stack"
        style="gap: 12px; border: 1px solid var(--border); border-radius: 12px; padding: 12px"
        :data-testid="`connection-group-${g.title}`"
      >
        <legend class="small muted">{{ g.title }}</legend>
        <p v-if="g.env.length" class="small muted" style="margin: 0" data-testid="connection-env">
          <span v-for="(name, i) in g.env" :key="name"
            >{{ i ? ' · ' : '' }}{{ envText(env.get(name), name) }}</span
          >
        </p>
        <div class="form-grid">
          <template v-for="f in g.fields" :key="f.key">
            <div v-if="f.kind === 'toggle'" class="field">
              <span class="row" style="flex-wrap: nowrap">
                <ToggleSwitch
                  :model-value="settings[f.key] === true"
                  :label="f.label"
                  :aria-describedby="`hint-${f.key}`"
                  :data-testid="`connection-${f.key}`"
                  @update:model-value="setField(f.key, $event)"
                />
                {{ f.label }}
              </span>
              <span :id="`hint-${f.key}`" class="field-hint">{{ f.hint }} {{ defaultText(f) }}</span>
            </div>
            <label v-else class="field">
              {{ f.label }}
              <span v-if="f.kind === 'number'" class="unit">
                <input
                  v-model.number="settings[f.key]"
                  type="number"
                  step="any"
                  required
                  :aria-describedby="`hint-${f.key}`"
                  :data-testid="`connection-${f.key}`"
                />
                <span v-if="f.unit">{{ f.unit }}</span>
              </span>
              <input
                v-else
                v-model="settings[f.key]"
                :type="f.kind"
                :list="f.sensor ? `ha-sensors-${f.sensor}` : undefined"
                :aria-describedby="`hint-${f.key}`"
                :data-testid="`connection-${f.key}`"
              />
              <span :id="`hint-${f.key}`" class="field-hint">{{ f.hint }} {{ defaultText(f) }}</span>
            </label>
          </template>
        </div>
      </fieldset>
      <datalist v-for="kind in ['battery', 'range']" :id="`ha-sensors-${kind}`" :key="kind">
        <option v-for="s in sensors.filter((x) => x.kind === kind)" :key="s.entityId" :value="s.entityId">
          {{ s.name || s.entityId }} ({{ s.state }} {{ s.unit }})
        </option>
      </datalist>
      <div v-if="confirmLower" class="banner tone-warn" role="alert" data-testid="confirm-retention">
        Keeping history for {{ settings.retentionDays }} days deletes older entries at the next daily cleanup.
        Save again to confirm.
      </div>
      <div class="row">
        <button class="btn primary" type="submit" :disabled="saving" data-testid="save-connections">
          {{ confirmLower ? 'Delete older history and save' : 'Save connection settings' }}
        </button>
        <button class="btn" type="button" data-testid="reset-connections" @click="resetToDefaults">
          Reset to defaults
        </button>
      </div>
    </form>
  </section>
</template>
