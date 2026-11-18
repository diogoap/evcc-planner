<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import { api } from '../api/client'
import type { Connection, HASensor, Place, Settings, TripPlans } from '../api/types'
import AIRunCard from '../components/AIRunCard.vue'
import AISettingsForm from '../components/AISettingsForm.vue'
import AdviceSettingsForm from '../components/AdviceSettingsForm.vue'
import ConnectionSettingsForm from '../components/ConnectionSettingsForm.vue'
import ConsumptionSetting from '../components/ConsumptionSetting.vue'
import ErrorBanner from '../components/ErrorBanner.vue'
import ForecastDetails from '../components/ForecastDetails.vue'
import NotificationSettingsForm from '../components/NotificationSettingsForm.vue'
import PageSkeleton from '../components/PageSkeleton.vue'
import SaveBar from '../components/SaveBar.vue'
import ToggleSwitch from '../components/ToggleSwitch.vue'
import { ago } from '../lib/format'
import { controlLabel, keepsPlan } from '../lib/status'
import { useUnsaved } from '../lib/unsaved'
import { mutate, notify, refresh, resumeAutomaticPlans, state, syncCalendar } from '../store'

type Tab = 'home' | 'planning' | 'advice' | 'places' | 'notifications' | 'connections'
const tabs: [Tab, string][] = [
  ['home', 'Home & vehicle'],
  ['planning', 'Planning'],
  ['advice', 'Advice'],
  ['places', 'Places'],
  ['notifications', 'Notifications'],
  ['connections', 'Connections'],
]
// Advice has two parts: the automatic rules' (Manual, On plug-in,
// Automatic) and AI mode's. The trip plan mode decides which applies; the
// other is greyed out, and the saved mode's opens first.
type AdviceTab = 'rules' | 'ai'
const adviceTabs: [AdviceTab, string][] = [
  ['rules', 'Automatic rules'],
  ['ai', 'AI mode'],
]
const tab = ref<Tab>('home')
const adviceTab = ref<AdviceTab>('rules')
const settings = ref<Settings | null>(null)
// Home & vehicle and Planning edit the same settings; the other tabs' forms
// report their own unsaved changes.
const unsaved = useUnsaved(settings)
const childDirty = ref(false)
const dirty = computed(() => unsaved.dirty.value || childDirty.value)
const leaveText = 'You have unsaved changes. Leave without saving?'

function go(next: Tab, sub?: AdviceTab) {
  const nextSub = next === 'advice' ? (sub ?? (savedMode.value === 'ai' ? 'ai' : 'rules')) : adviceTab.value
  if (next === tab.value) {
    // Between Advice's parts: each has its own form.
    if (next !== 'advice' || nextSub === adviceTab.value) return
    if (childDirty.value && !confirm(leaveText)) return
    childDirty.value = false
    adviceTab.value = nextSub
    return
  }
  const shared = (t: Tab) => t === 'home' || t === 'planning'
  const losing = shared(tab.value) && shared(next) ? childDirty.value : dirty.value
  if (losing && !confirm(leaveText)) return
  if (!(shared(tab.value) && shared(next))) unsaved.discard()
  childDirty.value = false
  tab.value = next
  adviceTab.value = nextSub
}

onBeforeRouteLeave(() => !dirty.value || confirm(leaveText))
// Closing or reloading the page asks too, while changes are unsaved.
function beforeUnload(e: BeforeUnloadEvent) {
  if (dirty.value) e.preventDefault()
}
window.addEventListener('beforeunload', beforeUnload)
onUnmounted(() => window.removeEventListener('beforeunload', beforeUnload))
const places = ref<Place[]>([])
const connections = ref<Connection[]>([])
// Home Assistant's sensors, to suggest a consumption sensor (none without Home Assistant).
const sensors = ref<HASensor[]>([])
const newPlace = reactive({ name: '', address: '' })
const editingPlace = ref<Place | null>(null)
const saving = ref(false)
const error = ref('')
const demo = reactive({ connected: false, soc: 45 })
const refreshing = ref(false)
const forecastDetails = ref<InstanceType<typeof ForecastDetails> | null>(null)
const aiRun = ref<InstanceType<typeof AIRunCard> | null>(null)

const fields: { key: keyof Settings; label: string; unit: string; step: string; hint: string }[] = [
  {
    key: 'usableCapacityKWh',
    label: 'Usable capacity',
    unit: 'kWh',
    step: '0.1',
    hint: 'Energy the battery can actually use, which is less than its gross size (a Leaf e+ uses about 59 of 62 kWh). Converts kWh into SoC %.',
  },
  {
    key: 'chargePowerKW',
    label: 'Charging power (at the wall)',
    unit: 'kW',
    step: '0.01',
    hint: 'What the charger delivers to the car, e.g. 3.68 kW at 16 A on one phase. Sets how long charging takes.',
  },
  {
    key: 'arrivalReservePct',
    label: 'Arrival reserve',
    unit: '%',
    step: '1',
    hint: 'SoC to have left when you get back home. Added to every departure target; keep it at or above EVCC’s minimum SoC.',
  },
  {
    key: 'chargingEfficiencyPct',
    label: 'Charging efficiency',
    unit: '%',
    step: '1',
    hint: 'Share of the energy from the wall that ends up in the battery (the rest is lost as heat). Lower values mean longer charging and more grid energy.',
  },
  {
    key: 'departureBufferMin',
    label: 'Departure buffer',
    unit: 'min',
    step: '1',
    hint: 'Extra time to leave before the trip, on top of the travel time: departure = arrive-by time − travel time − buffer.',
  },
  {
    key: 'chargeBufferMin',
    label: 'Charging time buffer',
    unit: 'min',
    step: '1',
    hint: 'Charging must be done this long before each departure, so the car is ready early. Shorter stays at home are not counted as charging time.',
  },
  {
    key: 'defaultStayMin',
    label: 'Default stay',
    unit: 'min',
    step: '1',
    hint: 'How long you stay when a new trip has no end time; the return leaves after this.',
  },
]

const modes: { value: TripPlans; label: string; hint: string }[] = [
  {
    value: 'ai',
    label: 'AI mode',
    hint: 'Like Automatic, but an AI model decides charge ahead, charge extra, charging from solar, and when to wait for cheaper prices, from the prices, trips, outlook, weather, and the past 30 days. It runs after new prices and when you plug in (Settings → Advice → AI mode). Every trip’s own minimum is still charged by itself, also when you plug in, and the automatic rules apply until the first AI plan.',
  },
  {
    value: 'automatic',
    label: 'Automatic',
    hint: 'Keeps EVCC’s plan matched to your trips while the car is plugged in: after every EVCC check, trip or setting change, and calendar sync. A plan you choose yourself (in EVCC or with Send plan) pauses it while that plan covers the next departure; one with less is replaced.',
  },
  {
    value: 'plug_in',
    label: 'On plug-in',
    hint: 'Sends the next departure’s target once when the car is plugged in, with later trips charged ahead when that is on (below). A plan in EVCC with at least that target is kept; a lower one is replaced.',
  },
  {
    value: 'manual',
    label: 'Manual',
    hint: 'Nothing is sent on its own; use Send plan to EVCC on the Overview.',
  },
]

const aheadFields: { key: keyof Settings; label: string; unit: string; step: string; hint: string }[] = [
  {
    key: 'chargeAheadMinSaving',
    label: 'Minimum saving',
    unit: '€/kWh',
    step: '0.01',
    hint: 'Charging before the next departure must be at least this much cheaper than at home before the later trip.',
  },
  {
    key: 'chargeAheadMaxSoc',
    label: 'Highest target when charging ahead',
    unit: '%',
    step: '1',
    hint: 'Charging ahead never raises the target above this. A trip’s own need can still go up to 100%.',
  },
]

// Solar at home: every mode uses these (advice, the chart's charging
// estimate, AI mode's data).
const solarFields: { key: keyof Settings; label: string; unit: string; step: string; hint: string }[] = [
  {
    key: 'householdKW',
    label: 'Household use during the day',
    unit: 'kW',
    step: '0.1',
    hint: 'Subtracted from the solar forecast to estimate the surplus the car can use.',
  },
  {
    key: 'solarTaxSaving',
    label: 'Tax not paid on own solar',
    unit: '€/kWh',
    step: '0.01',
    hint: 'Solar comes first: a solar kWh counts as the grid price at that moment minus this, the tax you don’t pay on it.',
  },
]

// Why charge ahead does or doesn't apply in the mode chosen in the form.
const aheadNote = computed(() => {
  switch (settings.value?.tripPlans) {
    case 'manual':
      return 'Not used in Manual mode: Send plan to EVCC covers the trips of one to three days you choose.'
    case 'plug_in':
      return 'Included in the plan sent when you plug in.'
    case 'ai':
      return 'In AI mode the AI decides charge ahead. These apply only while no AI plan applies (right after choosing AI mode, without an OpenRouter key, or when the last plan is more than a day old); the highest target also limits the AI.'
  }
  return ''
})
const savedMode = computed(() => state.plan?.settings.tripPlans)
// Advice opened before the plan loaded (right after a reload): the saved
// mode's part once it is known.
watch(savedMode, (m, old) => {
  if (!old && m && tab.value === 'advice' && !childDirty.value) adviceTab.value = m === 'ai' ? 'ai' : 'rules'
})

const control = computed(() =>
  state.plan ? controlLabel(state.plan.control, !!state.status?.connected) : null,
)

const statusTone: Record<Connection['status'], string> = {
  ok: 'tone-ok',
  error: 'tone-bad',
  stub: 'tone-warn',
  not_configured: 'tone-warn',
  planned: '',
}
const statusText: Record<Connection['status'], string> = {
  ok: 'Connected',
  error: 'Error',
  stub: 'Stub',
  not_configured: 'Not configured',
  planned: 'Later version',
}

async function load() {
  try {
    ;[settings.value, places.value, connections.value] = await Promise.all([
      api.settings(),
      api.places(),
      api.connections(),
    ])
    unsaved.mark()
    error.value = ''
    if (state.status) demo.connected = state.status.connected
  } catch (e) {
    error.value = (e as Error).message
  }
}
onMounted(() => {
  load()
  api
    .homeAssistantSensors()
    .then((list) => (sensors.value = list))
    .catch(() => {})
})

async function saveSettings() {
  if (!settings.value) return
  saving.value = true
  error.value = ''
  try {
    settings.value = await api.saveSettings(settings.value)
    unsaved.mark()
    notify('Settings saved')
    refresh()
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    saving.value = false
  }
}

async function addPlace() {
  if (await mutate(() => api.createPlace({ ...newPlace }), `Saved ${newPlace.name}`)) {
    newPlace.name = ''
    newPlace.address = ''
    places.value = await api.places()
  }
}

async function savePlace() {
  const p = editingPlace.value
  if (p && (await mutate(() => api.updatePlace(p), 'Place updated'))) {
    editingPlace.value = null
    places.value = await api.places()
  }
}

async function removePlace(p: Place) {
  if (!confirm(`Delete the saved place "${p.name}"? Trips using it will use the name as an address.`)) return
  if (await mutate(() => api.deletePlace(p.id), 'Place deleted')) places.value = await api.places()
}

async function syncCalendarNow() {
  connections.value = (await syncCalendar()) ?? connections.value
}

async function refreshForecasts() {
  refreshing.value = true
  try {
    connections.value = await api.refreshForecasts()
    notify('Forecasts refreshed')
    forecastDetails.value?.load()
    refresh()
  } catch (e) {
    notify((e as Error).message, 'error')
  } finally {
    refreshing.value = false
  }
}

async function checkEVCC() {
  await mutate(() => api.refreshEVCC(), 'EVCC checked')
  connections.value = await api.connections()
}

async function applyDemo() {
  await mutate(
    () => api.demoVehicle(demo.connected, demo.soc),
    demo.connected ? 'Demo car plugged in' : 'Demo car unplugged',
  )
}
</script>

<template>
  <main class="page">
    <div class="page-head"><h1>Settings</h1></div>
    <nav class="tabs settings-tabs m-0 mt-n2 mb-4" aria-label="Settings sections">
      <a
        v-for="[t, name] in tabs"
        :key="t"
        href="#"
        :class="{ 'router-link-exact-active': tab === t }"
        :aria-current="tab === t ? 'page' : undefined"
        @click.prevent="go(t)"
        >{{ name }}</a
      >
    </nav>
    <ErrorBanner v-if="error && !settings" :message="error" @retry="load" />
    <div v-else-if="error" class="banner tone-bad" role="alert">{{ error }}</div>
    <PageSkeleton v-if="!settings && !error && (tab === 'home' || tab === 'planning')" kind="form" />

    <section v-if="tab === 'home' && settings" class="card">
      <div class="card-title">Home &amp; vehicle</div>
      <form class="stack gap-3" @submit.prevent="saveSettings">
        <label class="field">
          Home address
          <input
            v-model="settings.homeAddress"
            placeholder="Street and number, town"
            aria-describedby="hint-home"
            data-testid="home-address"
          />
          <span id="hint-home" class="field-hint">
            Where every trip starts and ends; used to calculate route distances and travel times.
          </span>
        </label>
        <div class="form-grid">
          <label v-for="f in fields" :key="f.key" class="field">
            {{ f.label }}
            <span class="unit">
              <input
                v-model.number="settings[f.key]"
                type="number"
                :step="f.step"
                min="0"
                required
                :aria-describedby="`hint-${f.key}`"
                :data-testid="`setting-${f.key}`"
              />
              <span>{{ f.unit }}</span>
            </span>
            <span :id="`hint-${f.key}`" class="field-hint">{{ f.hint }}</span>
          </label>
        </div>
        <ConsumptionSetting
          v-model="settings"
          :sensors="sensors"
          :used="state.plan?.consumption ?? null"
          :now="state.plan?.now"
        />
        <fieldset class="stack panel gap-3">
          <legend class="small muted">Solar at home</legend>
          <div class="form-grid">
            <label v-for="f in solarFields" :key="f.key" class="field">
              {{ f.label }}
              <span class="unit">
                <input
                  v-model.number="settings[f.key]"
                  type="number"
                  :step="f.step"
                  min="0"
                  required
                  :aria-describedby="`hint-${f.key}`"
                  :data-testid="`setting-${f.key}`"
                />
                <span>{{ f.unit }}</span>
              </span>
              <span :id="`hint-${f.key}`" class="field-hint">{{ f.hint }}</span>
            </label>
          </div>
        </fieldset>
        <p class="small muted m-0">
          Saved privately in the planner's database. Config files only seed the vehicle and buffer values on
          first use.
        </p>
        <SaveBar
          :dirty="unsaved.dirty.value"
          :saving="saving"
          label="Save settings"
          @discard="unsaved.discard"
        />
      </form>
    </section>

    <section v-if="tab === 'planning' && settings" class="card" aria-labelledby="planning-title">
      <div id="planning-title" class="card-title">Trip plans</div>
      <form class="stack gap-3" @submit.prevent="saveSettings">
        <fieldset class="stack fieldset-plain gap-3">
          <legend class="small muted mb-2">How plans for your trips reach EVCC</legend>
          <label v-for="m in modes" :key="m.value" class="row items-start nowrap">
            <input
              v-model="settings.tripPlans"
              type="radio"
              name="tripPlans"
              :value="m.value"
              :data-testid="`trip-plans-${m.value}`"
              class="mt-1"
            />
            <span class="small">
              <strong>{{ m.label }}</strong>
              <span class="field-hint block">{{ m.hint }}</span>
            </span>
          </label>
        </fieldset>
        <div
          v-if="control && state.plan && keepsPlan(state.plan.control.mode)"
          class="banner"
          :class="`tone-${control.tone}`"
          role="status"
          data-testid="planning-status"
        >
          <strong>{{ control.mode }}</strong> · {{ control.detail }}
          <button
            v-if="state.plan.control.paused"
            class="btn sm ml-2"
            type="button"
            @click="resumeAutomaticPlans"
          >
            Resume
          </button>
          <div v-if="state.plan.ai" class="mt-1" data-testid="planning-ai">
            {{ state.plan.ai.inUse ? 'AI plan in use.' : state.plan.ai.note }}
            <a href="#" @click.prevent="go('advice', 'ai')">AI settings and the latest run</a>
          </div>
        </div>
        <fieldset
          class="stack panel gap-3"
          :disabled="settings.tripPlans === 'manual'"
          data-testid="ahead-settings"
        >
          <legend class="small muted">Charge ahead</legend>
          <p v-if="aheadNote" class="banner tone-info small m-0" data-testid="ahead-note">{{ aheadNote }}</p>
          <div class="row items-start nowrap">
            <ToggleSwitch
              v-model="settings.chargeAhead"
              label="Charge ahead for later trips"
              aria-describedby="hint-ahead"
              data-testid="setting-chargeAhead"
            />
            <div class="small">
              <strong>Charge ahead for later trips</strong>
              <div id="hint-ahead" class="field-hint">
                Within the published prices, adds later trips to the target when charging for them before the
                next departure is cheaper than at home just before them. Charge extra (under Advice) comes on
                top; EVCC gets the higher target.
              </div>
            </div>
          </div>
          <div class="form-grid">
            <label v-for="f in aheadFields" :key="f.key" class="field">
              {{ f.label }}
              <span class="unit">
                <input
                  v-model.number="settings[f.key]"
                  type="number"
                  :step="f.step"
                  min="0"
                  required
                  :aria-describedby="`hint-${f.key}`"
                  :data-testid="`setting-${f.key}`"
                />
                <span>{{ f.unit }}</span>
              </span>
              <span :id="`hint-${f.key}`" class="field-hint">{{ f.hint }}</span>
            </label>
          </div>
        </fieldset>
        <SaveBar
          :dirty="unsaved.dirty.value"
          :saving="saving"
          label="Save settings"
          @discard="unsaved.discard"
        />
      </form>
    </section>

    <section v-if="tab === 'places'" class="card">
      <div class="card-title">Saved places</div>
      <p class="small muted mt-0">
        Use a place name as a trip destination or calendar location; the planner routes to its address.
      </p>
      <ul class="list-plain mb-4">
        <li v-for="p in places" :key="p.id" class="row list-row">
          <template v-if="editingPlace?.id === p.id">
            <input v-model="editingPlace.name" aria-label="Name" class="place-name" />
            <input v-model="editingPlace.address" aria-label="Address" class="grow" />
            <button class="btn sm primary" type="button" @click="savePlace">Save</button>
            <button class="btn sm" type="button" @click="editingPlace = null">Cancel</button>
          </template>
          <template v-else>
            <strong class="place-label">{{ p.name }}</strong>
            <span class="muted grow">{{ p.address }}</span>
            <button class="btn sm" type="button" @click="editingPlace = { ...p }">Edit</button>
            <button class="btn sm ghost danger" type="button" @click="removePlace(p)">Delete</button>
          </template>
        </li>
      </ul>
      <form class="row" @submit.prevent="addPlace">
        <input
          v-model="newPlace.name"
          placeholder="Name, e.g. School"
          aria-label="New place name"
          required
          class="place-name"
        />
        <input
          v-model="newPlace.address"
          placeholder="Address"
          aria-label="New place address"
          required
          class="place-address"
        />
        <button class="btn" type="submit">Add place</button>
      </form>
    </section>

    <div v-if="tab === 'advice'" class="stack">
      <nav class="tabs m-0" aria-label="Advice sections" data-testid="advice-tabs">
        <a
          v-for="[t, name] in adviceTabs"
          :key="t"
          href="#"
          :class="{ 'router-link-exact-active': adviceTab === t }"
          :aria-current="adviceTab === t ? 'page' : undefined"
          :data-testid="`advice-tab-${t}`"
          @click.prevent="go('advice', t)"
          >{{ name }}</a
        >
      </nav>
      <AdviceSettingsForm v-if="adviceTab === 'rules'" :mode="savedMode" @dirty="(d) => (childDirty = d)" />
      <template v-else>
        <p v-if="savedMode !== 'ai'" class="banner tone-info m-0" role="status" data-testid="ai-off">
          AI mode is off, so these settings don't apply. Choose AI mode under
          <a href="#" @click.prevent="go('planning')">Planning</a> to use them. You can still preview the
          prompt.
        </p>
        <AIRunCard ref="aiRun" :active="savedMode === 'ai'" />
        <AISettingsForm
          :active="savedMode === 'ai'"
          @dirty="(d) => (childDirty = d)"
          @saved="aiRun?.load()"
        />
      </template>
    </div>

    <NotificationSettingsForm v-if="tab === 'notifications'" @dirty="(d) => (childDirty = d)" />

    <div v-if="tab === 'connections'" class="stack">
      <section class="card">
        <div class="card-title">
          Connections
          <span class="row">
            <button class="btn sm" type="button" @click="checkEVCC">Check EVCC</button>
            <button class="btn sm" type="button" @click="syncCalendarNow">Sync calendar now</button>
            <button class="btn sm" type="button" :disabled="refreshing" @click="refreshForecasts">
              Refresh forecasts
            </button>
          </span>
        </div>
        <table class="table">
          <tbody>
            <tr v-for="c in connections" :key="c.name" :data-testid="`connection-${c.name}`">
              <td>
                <strong>{{ c.name }}</strong>
              </td>
              <td>
                <span class="pill" :class="statusTone[c.status]">{{ statusText[c.status] }}</span>
              </td>
              <td class="small">
                {{ c.detail }}
                <div v-if="c.lastSuccessAt && state.plan" class="muted">
                  Last success {{ ago(c.lastSuccessAt, state.plan.now) }}
                </div>
              </td>
            </tr>
          </tbody>
        </table>
        <p class="small muted m-0 mt-3">
          Edit the settings below. API keys, calendar addresses, and the Home Assistant address and token stay
          in environment variables on the server and are never shown here. Forecasts are fetched once a day.
        </p>
        <div class="mt-5"><ForecastDetails ref="forecastDetails" /></div>
      </section>

      <ConnectionSettingsForm @saved="(c) => (connections = c)" @dirty="(d) => (childDirty = d)" />

      <section v-if="state.meta?.demo" class="card">
        <div>
          <div class="card-title">Demo car</div>
          <form class="row" @submit.prevent="applyDemo">
            <label class="row small"><input v-model="demo.connected" type="checkbox" /> Plugged in</label>
            <label class="unit small demo-soc">
              SoC <input v-model.number="demo.soc" type="number" min="1" max="100" data-testid="demo-soc" />
              <span>%</span>
            </label>
            <button class="btn sm" type="submit">Apply</button>
          </form>
          <p class="small muted m-0 mt-2">
            EVCC reads this SoC only while plugged in; the Home Assistant stub reports it always, 20 minutes
            behind.
          </p>
        </div>
      </section>
    </div>
  </main>
</template>

<style scoped>
/* Phones: the sections as a grid of buttons instead of a row that scrolls. */
@media (max-width: 640px) {
  .settings-tabs {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 6px;
    overflow: visible;
  }

  .settings-tabs a {
    justify-content: center;
    padding: var(--space-2) var(--space-1);
    border: 1px solid var(--border);
    border-radius: var(--radius-pill);
    background: var(--surface);
    font-size: var(--text-sm);
    text-align: center;
  }

  .settings-tabs a.router-link-exact-active {
    border-color: transparent;
    background: var(--accent-soft);
    color: var(--accent-strong);
  }
}

.place-name {
  max-width: 200px;
}

.place-label {
  min-width: 120px;
}

.place-address {
  flex: 1;
  min-width: 200px;
}

.demo-soc {
  max-width: 160px;
}
</style>
