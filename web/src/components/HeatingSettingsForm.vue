<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { api } from '../api/client'
import type { HASensor, HeatingSettings, HeatingView, NotificationCopy } from '../api/types'
import { dateTime, clock } from '../lib/format'
import { outcomeText, outcomeTone, temp } from '../lib/heating'
import { useUnsaved } from '../lib/unsaved'
import { notify, refresh, state } from '../store'
import InfoTip from './InfoTip.vue'
import NotificationCopyList from './NotificationCopyList.vue'
import SaveBar from './SaveBar.vue'
import ToggleSwitch from './ToggleSwitch.vue'

const settings = ref<HeatingSettings | null>(null)
const emit = defineEmits<{ dirty: [dirty: boolean] }>()
const unsaved = useUnsaved(settings)
watch(unsaved.dirty, (d) => emit('dirty', d))
const view = ref<HeatingView | null>(null)
const sensors = ref<HASensor[]>([])
const copy = ref<NotificationCopy[]>([])
const saving = ref(false)
const error = ref('')
// Demo mode: the outside temperature to set.
const demoOutside = ref(1)

const climates = computed(() => sensors.value.filter((s) => s.kind === 'climate'))
const temperatures = computed(() => sensors.value.filter((s) => s.kind === 'temperature'))

const outsideNow = computed(() => {
  const o = view.value?.outside
  if (!o || o.c == null) return 'Unknown'
  if (o.source === 'forecast') return `${temp(o.c)}, the forecast's low today`
  return `${temp(o.c)}${o.at ? `, read ${clock(o.at)}` : ''}`
})

const climateNow = computed(() => {
  const c = view.value?.climate
  if (!c || (!c.state && !c.error)) return ''
  if (c.error) return c.error
  const parts = [c.state === 'off' ? 'Off' : `On (${c.state})`]
  if (c.targetC != null) parts.push(`set to ${temp(c.targetC)}`)
  if (c.minC && c.maxC) parts.push(`${temp(c.minC)}–${temp(c.maxC)}`)
  return parts.join(' · ')
})

async function loadView() {
  try {
    view.value = await api.heating()
  } catch (e) {
    error.value = (e as Error).message
  }
}

onMounted(async () => {
  try {
    settings.value = await api.heatingSettings()
    unsaved.mark()
  } catch (e) {
    error.value = (e as Error).message
    return
  }
  const [, s, c] = await Promise.allSettled([loadView(), api.homeAssistantSensors(), api.notificationCopy()])
  if (s.status === 'fulfilled') sensors.value = s.value
  if (c.status === 'fulfilled')
    copy.value = c.value.filter((x) => x.setting === 'heating' || x.setting === 'heatingOn')
})

async function save() {
  const s = settings.value
  if (!s) return
  saving.value = true
  error.value = ''
  try {
    settings.value = await api.saveHeatingSettings(s)
    unsaved.mark()
    notify('Car heating settings saved')
    await loadView()
    refresh()
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    saving.value = false
  }
}

async function demo(input: { outsideC?: number; unavailable?: boolean; climateOff?: boolean }) {
  error.value = ''
  try {
    view.value = await api.demoClimate(input)
    refresh()
  } catch (e) {
    error.value = (e as Error).message
  }
}
</script>

<template>
  <section class="card" aria-labelledby="heating-title">
    <div id="heating-title" class="card-title">Car heating</div>
    <div v-if="error" class="banner tone-bad" role="alert">{{ error }}</div>
    <form v-if="settings" class="stack gap-3" @submit.prevent="save">
      <div class="row items-start nowrap">
        <ToggleSwitch
          v-model="settings.enabled"
          label="Car heating"
          aria-describedby="hint-heating"
          data-testid="heating-enabled"
        />
        <div class="small">
          <strong>Heat the car before departures</strong>
          <InfoTip v-if="copy.length" label="What car heating's messages say">
            <NotificationCopyList :copies="copy" :snooze-min="30" />
          </InfoTip>
          <div id="hint-heating" class="field-hint">
            When it's cold outside, turns on the car's climate in Home Assistant before each trip that leaves
            from home, by itself or after you confirm on the phone. Once per departure; the car's own timer
            turns it off.
          </div>
        </div>
      </div>

      <div class="form-grid">
        <label class="field">
          Heat below
          <span class="unit">
            <input
              v-model.number="settings.belowC"
              type="number"
              step="0.5"
              min="-20"
              max="15"
              required
              aria-describedby="hint-belowC"
              data-testid="heating-belowC"
            />
            °C
          </span>
          <span id="hint-belowC" class="field-hint">The outside temperature it compares with.</span>
        </label>
        <label class="field">
          Before departure
          <span class="unit">
            <input
              v-model.number="settings.leadMin"
              type="number"
              step="1"
              min="5"
              max="60"
              required
              aria-describedby="hint-leadMin"
              data-testid="heating-leadMin"
            />
            min
          </span>
          <span id="hint-leadMin" class="field-hint">When it turns on (or asks), before the car leaves.</span>
        </label>
        <label class="field">
          Target temperature
          <span class="unit">
            <input
              v-model.number="settings.targetC"
              type="number"
              step="0.5"
              min="10"
              max="30"
              required
              aria-describedby="hint-targetC"
              data-testid="heating-targetC"
            />
            °C
          </span>
          <span id="hint-targetC" class="field-hint">Kept within what the car's climate accepts.</span>
        </label>
        <label class="field">
          Car climate
          <input
            v-model.trim="settings.climateEntity"
            list="ha-sensors-climate"
            placeholder="climate.car_climate"
            aria-describedby="hint-climateEntity"
            data-testid="heating-climateEntity"
          />
          <span id="hint-climateEntity" class="field-hint">
            The car's Home Assistant climate entity. It's turned on in its heating mode (heat, else heat/cool,
            else auto).
          </span>
        </label>
        <label class="field">
          Outside temperature sensor
          <input
            v-model.trim="settings.outsideEntity"
            list="ha-sensors-temperature"
            placeholder="sensor.outside_temperature"
            aria-describedby="hint-outsideEntity"
            data-testid="heating-outsideEntity"
          />
          <span id="hint-outsideEntity" class="field-hint">
            Without a reading from the last 2 hours (or without a sensor), the weather forecast's lowest
            temperature for the day is used.
          </span>
        </label>
      </div>

      <fieldset class="stack fieldset-plain gap-2">
        <legend class="small mb-1"><strong>Ask on the phone first</strong></legend>
        <label class="row small nowrap items-start">
          <input v-model="settings.askPlugged" type="checkbox" data-testid="heating-askPlugged" />
          While the car is plugged in
        </label>
        <label class="row small nowrap items-start">
          <input v-model="settings.askUnplugged" type="checkbox" data-testid="heating-askUnplugged" />
          While the car is unplugged (it then heats from the battery)
        </label>
        <span class="field-hint">
          A question with a Heat now button, also in quiet hours, cleared after 30 minutes without an answer.
          Otherwise it turns on by itself. Needs phone notifications (Settings → Notifications), where
          <strong>Car heating on</strong> also tells you each time it turns on.
        </span>
      </fieldset>

      <SaveBar
        :dirty="unsaved.dirty.value"
        :saving="saving"
        label="Save car heating settings"
        @discard="unsaved.discard"
      />
    </form>

    <div v-if="view" class="stack gap-2 mt-5" data-testid="heating-now">
      <div class="card-title mb-1">Now</div>
      <p v-for="n in view.notes" :key="n" class="banner tone-info small m-0" role="status">{{ n }}</p>
      <dl class="kv kv-left small">
        <dt>Outside</dt>
        <dd data-testid="heating-outside">
          {{ outsideNow }}
          <span v-if="view.outside.note" class="muted"> · sensor not used: {{ view.outside.note }}</span>
        </dd>
        <template v-if="climateNow">
          <dt>Car climate</dt>
          <dd data-testid="heating-climate" :class="{ 'text-bad': view.climate.error }">{{ climateNow }}</dd>
        </template>
        <template v-if="view.climate.mode && view.climate.sendC != null">
          <dt>Turns on</dt>
          <dd>{{ view.climate.mode }} at {{ temp(view.climate.sendC) }}</dd>
        </template>
      </dl>
    </div>

    <div v-if="view?.recent.length" class="mt-5">
      <div class="card-title mb-1">Recent decisions</div>
      <ul class="list-plain" data-testid="heating-recent">
        <li v-for="r in view.recent" :key="r.id" class="stack gap-1 list-row">
          <div class="row">
            <strong>{{ r.title }}</strong>
            <span class="spacer" />
            <span class="small muted">leaves {{ dateTime(r.departAt) }}</span>
          </div>
          <div
            class="small"
            :class="{ 'text-bad': outcomeTone(r) === 'bad', 'text-warn': outcomeTone(r) === 'warn' }"
          >
            {{ outcomeText(r) }}
          </div>
        </li>
      </ul>
    </div>

    <div v-if="state.meta?.demo && view" class="stack gap-2 mt-5" data-testid="heating-demo">
      <div class="card-title mb-1">Demo car</div>
      <div class="row">
        <label class="field">
          Outside
          <span class="unit">
            <input v-model.number="demoOutside" type="number" step="0.5" data-testid="heating-demo-outside" />
            °C
          </span>
        </label>
        <button
          class="btn"
          type="button"
          data-testid="heating-demo-set"
          @click="demo({ outsideC: demoOutside })"
        >
          Set outside temperature
        </button>
        <button class="btn" type="button" @click="demo({ unavailable: true })">Make it unavailable</button>
        <button class="btn" type="button" @click="demo({ climateOff: true })">Turn the climate off</button>
      </div>
      <span class="field-hint">Each checks car heating right away, as its job does every minute.</span>
    </div>

    <datalist id="ha-sensors-climate">
      <option v-for="s in climates" :key="s.entityId" :value="s.entityId">
        {{ s.name || s.entityId }} ({{ s.state }})
      </option>
    </datalist>
    <datalist id="ha-sensors-temperature">
      <option v-for="s in temperatures" :key="s.entityId" :value="s.entityId">
        {{ s.name || s.entityId }} ({{ s.state }} {{ s.unit }})
      </option>
    </datalist>
  </section>
</template>
