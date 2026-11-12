<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api } from '../api/client'
import type { NotificationSettings, NotificationVerb, PhoneNotification } from '../api/types'
import { dateTime } from '../lib/format'
import { notify, refresh, state } from '../store'
import ToggleSwitch from './ToggleSwitch.vue'

const settings = ref<NotificationSettings | null>(null)
const phones = ref<string[]>([])
const phonesError = ref('')
const recent = ref<PhoneNotification[]>([])
const saving = ref(false)
const testing = ref(false)
const error = ref('')

const toggles: { key: 'problems' | 'questions' | 'ready'; label: string; hint: string }[] = [
  {
    key: 'problems',
    label: 'Charging problems',
    hint: 'Charging stopped, unplugged early, EVCC not in Smart mode while plugged in, or a trip that left below its target.',
  },
  {
    key: 'questions',
    label: 'Questions about calendar events',
    hint: 'Asks about events marked Needs review: Count it or Not a trip.',
  },
  {
    key: 'ready',
    label: 'Ready messages',
    hint: 'Tells you when the car reached its target before a departure.',
  },
]

const verbTitles: Record<NotificationVerb, string> = {
  snooze: 'Remind later',
  skip: 'Skip trip',
  view: 'View plan',
  count: 'Count it',
  notrip: 'Not a trip',
}

const stateText: Record<PhoneNotification['state'], string> = {
  sent: 'On the phone',
  snoozed: 'Snoozed',
  done: 'Done',
  failed: 'Failed',
}

async function loadRecent() {
  recent.value = await api.notifications().catch(() => [])
}

onMounted(async () => {
  try {
    settings.value = await api.notificationSettings()
  } catch (e) {
    error.value = (e as Error).message
  }
  try {
    phones.value = await api.phones()
  } catch (e) {
    phonesError.value = (e as Error).message
  }
  await loadRecent()
})

function togglePhone(p: string, on: boolean) {
  const s = settings.value
  if (!s) return
  s.phones = on ? [...s.phones.filter((x) => x !== p), p] : s.phones.filter((x) => x !== p)
}

async function save() {
  const s = settings.value
  if (!s) return
  saving.value = true
  error.value = ''
  try {
    settings.value = await api.saveNotificationSettings(s)
    notify('Notification settings saved')
    refresh()
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    saving.value = false
  }
}

async function sendTest() {
  testing.value = true
  try {
    await api.testNotification()
    notify('Test notification sent')
  } catch (e) {
    notify((e as Error).message, 'error')
  } finally {
    testing.value = false
    await loadRecent()
  }
}

async function press(n: PhoneNotification, verb: NotificationVerb) {
  try {
    await api.pressNotification(n.id, verb)
    notify(`${verbTitles[verb]} pressed`)
    await refresh()
  } catch (e) {
    notify((e as Error).message, 'error')
  }
  await loadRecent()
}
</script>

<template>
  <section class="card" aria-labelledby="notifications-title">
    <div id="notifications-title" class="card-title">Phone notifications</div>
    <div v-if="error" class="banner tone-bad" role="alert">{{ error }}</div>
    <form v-if="settings" class="stack gap-3" @submit.prevent="save">
      <div class="row items-start nowrap">
        <ToggleSwitch
          v-model="settings.enabled"
          label="Notifications"
          aria-describedby="hint-notify"
          data-testid="notify-enabled"
        />
        <div class="small">
          <strong>Notifications</strong>
          <div id="hint-notify" class="field-hint">
            Plug-in reminders and charging problems on your phone, through the Home Assistant Companion app.
            Buttons on them (Remind later, Skip trip) come back over Home Assistant's connection; no
            automation needed.
          </div>
        </div>
      </div>

      <fieldset class="stack fieldset-plain gap-2">
        <legend class="small mb-1"><strong>Phones</strong></legend>
        <p v-if="phonesError" class="small text-warn m-0">{{ phonesError }}</p>
        <p v-else-if="!phones.length" class="small muted m-0">
          Home Assistant has no mobile app to notify; install the Companion app and sign in.
        </p>
        <label v-for="p in phones" :key="p" class="row small">
          <input
            type="checkbox"
            :checked="settings.phones.includes(p)"
            :data-testid="`phone-${p}`"
            @change="togglePhone(p, ($event.target as HTMLInputElement).checked)"
          />
          notify.{{ p }}
        </label>
      </fieldset>

      <div class="form-grid">
        <label class="field">
          Link address
          <input
            v-model.trim="settings.linkUrl"
            placeholder="http://…:7201/"
            aria-describedby="hint-link"
            data-testid="notify-link"
          />
          <span id="hint-link" class="field-hint">
            The planner's address as your phone reaches it, for View plan. Empty leaves that button out.
          </span>
        </label>
        <label class="field">
          Car location sensor (optional)
          <input
            v-model.trim="settings.locationEntity"
            placeholder="device_tracker.car"
            aria-describedby="hint-location"
          />
          <span id="hint-location" class="field-hint">
            A Home Assistant entity that is “home” while the car is home; plug-in reminders wait while it is
            elsewhere.
          </span>
        </label>
      </div>

      <div class="row items-start nowrap">
        <ToggleSwitch v-model="settings.early" label="Early reminders" aria-describedby="hint-early" />
        <div class="small">
          <strong>Early reminders</strong>
          <div id="hint-early" class="field-hint">
            Before the cheapest charging window of the plan, when the car is unplugged. In quiet hours, just
            before they start.
          </div>
        </div>
      </div>
      <div class="form-grid">
        <label class="field">
          Early reminder
          <span class="unit"
            ><input v-model.number="settings.earlyLeadMin" type="number" min="0" max="240" required /><span
              >min before</span
            ></span
          >
        </label>
        <label class="field">
          Urgent reminder
          <span class="unit"
            ><input v-model.number="settings.urgentLeadMin" type="number" min="0" max="240" required /><span
              >min before</span
            ></span
          >
          <span class="field-hint">Before the latest plug-in time that still reaches the trip's target.</span>
        </label>
        <label class="field">
          Snooze
          <span class="unit"
            ><input v-model.number="settings.snoozeMin" type="number" min="5" max="240" required /><span
              >min</span
            ></span
          >
          <span class="field-hint">Remind later never goes past the latest plug-in time.</span>
        </label>
        <label class="field">
          Quiet hours from
          <input v-model="settings.quietFrom" type="time" required data-testid="notify-quiet-from" />
        </label>
        <label class="field">
          Quiet hours until
          <input v-model="settings.quietTo" type="time" required data-testid="notify-quiet-to" />
        </label>
      </div>
      <div class="row items-start nowrap">
        <ToggleSwitch v-model="settings.urgentInQuiet" label="Urgent reminders in quiet hours" />
        <div class="small">
          <strong>Urgent reminders in quiet hours</strong>
          <div class="field-hint">On: send them anyway. Off: wait until quiet hours end.</div>
        </div>
      </div>
      <div v-for="t in toggles" :key="t.key" class="row items-start nowrap">
        <ToggleSwitch v-model="settings[t.key]" :label="t.label" />
        <div class="small">
          <strong>{{ t.label }}</strong>
          <div class="field-hint">{{ t.hint }}</div>
        </div>
      </div>
      <div class="row">
        <button class="btn primary" type="submit" :disabled="saving">Save notification settings</button>
        <button
          class="btn"
          type="button"
          :disabled="testing || !settings.phones.length"
          data-testid="notify-test"
          @click="sendTest"
        >
          Send test notification
        </button>
      </div>
    </form>

    <div v-if="recent.length" class="mt-5">
      <div class="card-title mb-1">Recent notifications</div>
      <ul class="list-plain" data-testid="recent-notifications">
        <li
          v-for="n in recent"
          :key="n.id"
          class="stack gap-1 list-row"
          :data-testid="`notification-${n.kind}`"
        >
          <div class="row">
            <strong>{{ n.title }}</strong>
            <span
              class="pill"
              :class="{ 'tone-warn': n.state === 'snoozed', 'tone-bad': n.state === 'failed' }"
            >
              {{ stateText[n.state] }}
            </span>
            <span class="spacer" />
            <span class="small muted">{{ dateTime(n.sentAt ?? n.createdAt) }}</span>
          </div>
          <div class="small">{{ n.message }}</div>
          <div v-if="n.note" class="small muted">{{ n.note }}</div>
          <div v-if="state.meta?.demo && (n.state === 'sent' || n.state === 'snoozed')" class="row">
            <span class="small muted">Demo phone:</span>
            <button
              v-for="v in n.verbs.filter((v) => v !== 'view')"
              :key="v"
              class="btn sm"
              type="button"
              @click="press(n, v)"
            >
              {{ v === 'snooze' ? `Remind in ${settings?.snoozeMin ?? 30} min` : verbTitles[v] }}
            </button>
          </div>
        </li>
      </ul>
    </div>
  </section>
</template>
