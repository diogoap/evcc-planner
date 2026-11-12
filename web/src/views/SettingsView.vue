<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { api } from '../api/client'
import type { Connection, Place, Settings, TripPlans } from '../api/types'
import AdviceSettingsForm from '../components/AdviceSettingsForm.vue'
import ConnectionSettingsForm from '../components/ConnectionSettingsForm.vue'
import ForecastDetails from '../components/ForecastDetails.vue'
import NotificationSettingsForm from '../components/NotificationSettingsForm.vue'
import ToggleSwitch from '../components/ToggleSwitch.vue'
import { ago } from '../lib/format'
import { controlLabel } from '../lib/status'
import { mutate, notify, refresh, resumeAutomaticPlans, state } from '../store'

type Tab = 'home' | 'planning' | 'places' | 'advice' | 'notifications' | 'connections'
const tab = ref<Tab>('home')
const settings = ref<Settings | null>(null)
const places = ref<Place[]>([])
const connections = ref<Connection[]>([])
const newPlace = reactive({ name: '', address: '' })
const editingPlace = ref<Place | null>(null)
const saving = ref(false)
const error = ref('')
const demo = reactive({ connected: false, soc: 45 })
const refreshing = ref(false)
const forecastDetails = ref<InstanceType<typeof ForecastDetails> | null>(null)

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
    key: 'consumptionKWhPer100Km',
    label: 'Consumption',
    unit: 'kWh/100 km',
    step: '0.1',
    hint: 'Average use including heating or cooling. Turns trip distance into energy; raise it in winter.',
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
    value: 'automatic',
    label: 'Automatic',
    hint: 'Keeps EVCC’s plan matched to your trips while the car is plugged in: after every EVCC check, trip or setting change, and calendar sync. A plan you choose yourself (in EVCC or with Send plan) pauses it while that plan covers the next departure; one with less is replaced.',
  },
  {
    value: 'plug_in',
    label: 'On plug-in',
    hint: 'Sends the next departure’s target once when the car is plugged in. A plan with the same or a higher target is kept; a lower one is replaced.',
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
    if (state.status) demo.connected = state.status.connected
  } catch (e) {
    error.value = (e as Error).message
  }
}
onMounted(load)

async function saveSettings() {
  if (!settings.value) return
  saving.value = true
  error.value = ''
  try {
    settings.value = await api.saveSettings(settings.value)
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

async function syncCalendar() {
  try {
    connections.value = await api.syncCalendar()
    notify('Calendar synced')
    refresh()
  } catch (e) {
    notify((e as Error).message, 'error')
  }
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
    <nav class="tabs m-0 mt-n2 mb-4" aria-label="Settings sections">
      <a href="#" :class="{ 'router-link-exact-active': tab === 'home' }" @click.prevent="tab = 'home'"
        >Home &amp; vehicle</a
      >
      <a
        href="#"
        :class="{ 'router-link-exact-active': tab === 'planning' }"
        @click.prevent="tab = 'planning'"
        >Planning</a
      >
      <a href="#" :class="{ 'router-link-exact-active': tab === 'places' }" @click.prevent="tab = 'places'"
        >Places</a
      >
      <a href="#" :class="{ 'router-link-exact-active': tab === 'advice' }" @click.prevent="tab = 'advice'"
        >Advice</a
      >
      <a
        href="#"
        :class="{ 'router-link-exact-active': tab === 'notifications' }"
        @click.prevent="tab = 'notifications'"
        >Notifications</a
      >
      <a
        href="#"
        :class="{ 'router-link-exact-active': tab === 'connections' }"
        @click.prevent="tab = 'connections'"
        >Connections</a
      >
    </nav>
    <div v-if="error" class="banner tone-bad" role="alert">{{ error }}</div>

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
        <p class="small muted m-0">
          Saved privately in the planner's database. Config files only seed the vehicle and buffer values on
          first use.
        </p>
        <div><button class="btn primary" type="submit" :disabled="saving">Save settings</button></div>
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
          v-if="control && state.plan?.control.mode === 'automatic'"
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
        </div>
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
              Automatic mode only. Within the published prices, adds later trips to the target when charging
              for them before the next departure is cheaper than at home just before them.
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
        <div><button class="btn primary" type="submit" :disabled="saving">Save settings</button></div>
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

    <AdviceSettingsForm v-if="tab === 'advice'" />

    <NotificationSettingsForm v-if="tab === 'notifications'" />

    <div v-if="tab === 'connections'" class="stack">
      <section class="card">
        <div class="card-title">
          Connections
          <span class="row">
            <button class="btn sm" type="button" @click="checkEVCC">Check EVCC</button>
            <button class="btn sm" type="button" @click="syncCalendar">Sync calendar now</button>
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

      <ConnectionSettingsForm @saved="(c) => (connections = c)" />

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
