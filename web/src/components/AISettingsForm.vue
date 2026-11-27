<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { api } from '../api/client'
import type { AIPreview, AISettings, AISettingsView } from '../api/types'
import {
  insertMacro,
  macroText,
  modelLabel,
  monthlyText,
  perMillion,
  tokensText,
  unknownMacros,
  unusedMacros,
  usd,
} from '../lib/ai'
import { shortDate } from '../lib/format'
import { useUnsaved } from '../lib/unsaved'
import { notify, refresh } from '../store'
import SaveBar from './SaveBar.vue'
import ToggleSwitch from './ToggleSwitch.vue'

// AI mode's settings: when the AI plan runs, the model, and the prompt,
// whose placeholders ({{trips}}, …) are filled with the planner's data.
// active: AI mode is on. Otherwise the settings are shown greyed out, and
// only the preview works.
const props = defineProps<{ active: boolean }>()
const settings = ref<AISettings | null>(null)
const info = ref<AISettingsView | null>(null)
const emit = defineEmits<{ dirty: [dirty: boolean]; saved: [] }>()
const unsaved = useUnsaved(settings)
watch(unsaved.dirty, (d) => emit('dirty', d))
const saving = ref(false)
const error = ref('')
const prompt = ref<HTMLTextAreaElement | null>(null)

// The model: one of the models offered, or another typed in after choosing
// "Other model…" (also shown for a saved model that isn't offered).
const OTHER = 'other'
const otherChosen = ref(false)
const otherInput = ref<HTMLInputElement | null>(null)
const modelChoice = computed({
  get: () => {
    const m = settings.value?.model ?? ''
    return !otherChosen.value && info.value?.models.some((o) => o.id === m) ? m : OTHER
  },
  set: (v: string) => {
    otherChosen.value = v === OTHER
    if (v !== OTHER && settings.value) settings.value.model = v
    else nextTick(() => otherInput.value?.focus())
  },
})
// The chosen model's price; none for a model typed in.
const chosenModel = computed(() => info.value?.models.find((m) => m.id === modelChoice.value) ?? null)
const pricesDate = computed(() => (info.value ? shortDate(info.value.pricesDate, { year: true }) : ''))

// Discarding goes back to the saved model's own choice.
watch(unsaved.dirty, (d) => {
  if (!d) otherChosen.value = false
})
const preview = ref<AIPreview | null>(null)
const previewing = ref(false)

onMounted(async () => {
  try {
    info.value = await api.aiSettings()
    settings.value = info.value.settings
    unsaved.mark()
  } catch (e) {
    error.value = (e as Error).message
  }
})

const macros = computed(() => info.value?.macros ?? [])
const unknown = computed(() => (settings.value ? unknownMacros(settings.value.prompt, macros.value) : []))
const unused = computed(() => (settings.value ? unusedMacros(settings.value.prompt, macros.value) : []))
const isDefault = computed(() => settings.value?.prompt.trim() === info.value?.defaultPrompt.trim())
const goalsDefault = computed(() => settings.value?.goals.trim() === info.value?.defaultGoals.trim())

async function insert(name: string) {
  const s = settings.value
  const el = prompt.value
  if (!s || !el) return
  const { text, cursor } = insertMacro(s.prompt, el.selectionStart, el.selectionEnd, name)
  s.prompt = text
  await nextTick()
  el.focus()
  el.setSelectionRange(cursor, cursor)
}

function resetGoals() {
  if (!settings.value || !info.value) return
  if (!confirm('Put back the default goals? Your changes to the goals are lost once you save.')) return
  settings.value.goals = info.value.defaultGoals
}

function resetPrompt() {
  if (!settings.value || !info.value) return
  if (!confirm('Put back the default prompt? Your changes to the prompt are lost once you save.')) return
  settings.value.prompt = info.value.defaultPrompt
}

async function showPreview() {
  if (!settings.value) return
  previewing.value = true
  try {
    preview.value = await api.previewAI(settings.value.prompt, settings.value.goals)
  } catch (e) {
    notify((e as Error).message, 'error')
  } finally {
    previewing.value = false
  }
}

async function save() {
  const s = settings.value
  if (!s) return
  saving.value = true
  error.value = ''
  try {
    info.value = await api.saveAISettings(s)
    settings.value = info.value.settings
    unsaved.mark()
    notify('AI settings saved')
    emit('saved')
    refresh()
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <section class="card" aria-labelledby="ai-settings-title">
    <div id="ai-settings-title" class="card-title">AI mode</div>
    <div v-if="error" class="banner tone-bad" role="alert">{{ error }}</div>
    <form v-if="settings && info" class="stack gap-4" @submit.prevent="save">
      <p class="field-hint m-0">
        Choose AI mode under Planning. An AI model then decides charge ahead, charge extra, charging from
        solar, and when to wait for cheaper prices, from the data in the prompt below. The planner still
        charges every trip's own minimum by itself, keeps charge ahead at or below its cap, and checks every
        answer. Its daily advice always shows; extra plans and postponements are created automatically only
        with the switch below (Settings → Advice doesn't apply in AI mode).
      </p>

      <fieldset class="stack panel gap-3" :disabled="!props.active" data-testid="ai-runs">
        <legend class="small muted">When the AI plan runs</legend>
        <div class="row items-start nowrap">
          <ToggleSwitch
            v-model="settings.onPrices"
            label="After new prices"
            aria-describedby="hint-ai-prices"
            data-testid="ai-on-prices"
          />
          <div class="small">
            <strong>After new prices</strong>
            <div id="hint-ai-prices" class="field-hint">
              When EVCC publishes tomorrow's prices (around 14:00), and when no AI plan applies, such as right
              after choosing AI mode.
            </div>
          </div>
        </div>
        <div class="row items-start nowrap">
          <ToggleSwitch
            v-model="settings.onPlugIn"
            label="When the car is plugged in"
            aria-describedby="hint-ai-plugin"
            data-testid="ai-on-plugin"
          />
          <div class="small">
            <strong>When the car is plugged in</strong>
            <div id="hint-ai-plugin" class="field-hint">
              The plan for the next trip is sent right away; the new AI plan follows a few seconds later.
            </div>
          </div>
        </div>
        <div class="form-grid">
          <label class="field">
            Automatic runs per day
            <span class="unit">
              <input
                v-model.number="settings.maxRunsPerDay"
                type="number"
                min="0"
                max="10"
                step="1"
                required
                aria-describedby="hint-ai-runs"
                data-testid="ai-max-runs"
              />
              <span>runs</span>
            </span>
            <span id="hint-ai-runs" class="field-hint"
              >Every run counts, also those by hand, which are always allowed. The last one waits for
              tomorrow's prices until a plan has seen them. A failed run is tried again after an hour.</span
            >
          </label>
          <label class="field">
            Model
            <select v-model="modelChoice" aria-describedby="hint-ai-model" data-testid="ai-model-choice">
              <option v-for="m in info.models" :key="m.id" :value="m.id">
                {{ modelLabel(m, info.defaultModel) }}
              </option>
              <option :value="OTHER">Other model…</option>
            </select>
            <template v-if="chosenModel">
              <span class="small strong" data-testid="ai-model-month">{{
                monthlyText(chosenModel, settings.maxRunsPerDay)
              }}</span>
              <span class="field-hint" data-testid="ai-model-price"
                >≈ {{ usd(chosenModel.runUsd) }} a run: {{ perMillion(chosenModel.inputPerM) }} in,
                {{ perMillion(chosenModel.outputPerM) }} out per million tokens.</span
              >
            </template>
            <span id="hint-ai-model" class="field-hint"
              >An OpenRouter model with strict structured outputs. Costs are OpenRouter's prices on
              {{ pricesDate }} (they change) for a typical run of about 7,000 tokens in and 4,500 out. For
              another model, choose Other model… and type its ID, e.g. ~openai/gpt-luna-latest.</span
            >
          </label>
          <label v-if="modelChoice === OTHER" class="field">
            Other model
            <input
              ref="otherInput"
              v-model="settings.model"
              autocomplete="off"
              spellcheck="false"
              placeholder="e.g. ~openai/gpt-luna-latest"
              required
              data-testid="ai-model"
            />
          </label>
        </div>
        <p v-if="info.note" class="small text-warn m-0">{{ info.note }}</p>
      </fieldset>

      <fieldset class="stack panel gap-3" :disabled="!props.active" data-testid="ai-auto">
        <legend class="small muted">Act on the AI's advice automatically</legend>
        <div class="row">
          <ToggleSwitch
            v-model="settings.autoApply"
            label="Automatically create plans from the AI's advice"
            aria-describedby="hint-ai-auto"
            data-testid="ai-auto-apply"
          />
          <strong>Automatically create plans from the AI's advice</strong>
        </div>
        <p id="hint-ai-auto" class="field-hint m-0 mt-n2">
          Off: the AI's advice offers Create extra plan and Review postponement for you to confirm. On, it
          acts once a day on today's AI advice with enough confidence: an extra plan with the AI's target,
          ready by the next trip so EVCC picks the cheapest hours before it, after which the previous plan
          comes back. It skips plans you set in EVCC, and never acts on the automatic rules' advice shown
          until the first AI plan. The next trip's plan always follows the AI by itself.
          <template v-if="!settings.autoSet">
            Until you change these here, they follow the same switch under Automatic rules.</template
          >
        </p>
        <div class="form-grid">
          <label class="field">
            Minimum confidence
            <select v-model.number="settings.autoMinConfidence" data-testid="ai-auto-confidence">
              <option :value="3">3 — High</option>
              <option :value="2">2 — Medium</option>
            </select>
            <span class="field-hint"
              >The AI gives 3 for decisions on published prices, 2 on the outlook or a full price history.
              Low-confidence advice never acts.</span
            >
          </label>
        </div>
        <label class="row small">
          <input v-model="settings.autoPostpone" type="checkbox" data-testid="ai-auto-postpone" />
          Also postpone cheap-grid charging when the AI asks for it (today only, until the cheaper day)
        </label>
      </fieldset>

      <fieldset class="stack panel gap-3">
        <legend class="small muted">Goals</legend>
        <p id="hint-ai-goals" class="field-hint m-0">
          What <code>{{ macroText('goals') }}</code> holds in the prompt. By default, the planner's rules with
          your settings (arrival reserve, charge-ahead cap, EVCC's charge limit), which follow them when they
          change. Edit them to steer the AI, for example how much battery care matters against price. The
          planner's checks apply whatever you write here.
        </p>
        <label class="field">
          Goals
          <textarea
            v-model="settings.goals"
            class="prompt goals"
            rows="8"
            spellcheck="false"
            :readonly="!props.active"
            aria-describedby="hint-ai-goals"
            data-testid="ai-goals"
          />
        </label>
        <div class="row">
          <button
            class="btn ghost"
            type="button"
            :disabled="!props.active || goalsDefault"
            data-testid="ai-goals-reset"
            @click="resetGoals"
          >
            Default goals
          </button>
        </div>
      </fieldset>

      <fieldset class="stack panel gap-3">
        <legend class="small muted">Prompt</legend>
        <p class="field-hint m-0">
          Placeholders in double braces are filled with the planner's data when the prompt is sent. Trips go
          with their names, never their places. The answer's format is fixed, so the planner can check it. Tap
          a placeholder to put it in at the cursor.
        </p>
        <div class="row gap-2" data-testid="ai-macros">
          <button
            v-for="m in macros"
            :key="m.name"
            class="chip"
            type="button"
            :disabled="!props.active"
            :title="m.description"
            :aria-label="`Insert ${macroText(m.name)}: ${m.description}`"
            @click="insert(m.name)"
          >
            {{ macroText(m.name) }}
          </button>
        </div>
        <details class="small">
          <summary>What each placeholder holds</summary>
          <dl class="kv kv-left mt-2">
            <template v-for="m in macros" :key="m.name">
              <dt>
                <code>{{ macroText(m.name) }}</code>
              </dt>
              <dd>{{ m.description }}</dd>
            </template>
          </dl>
        </details>
        <label class="field">
          Prompt
          <textarea
            ref="prompt"
            v-model="settings.prompt"
            class="prompt"
            rows="18"
            spellcheck="false"
            :readonly="!props.active"
            required
            data-testid="ai-prompt"
          />
        </label>
        <p v-if="unknown.length" class="banner tone-bad small m-0" role="alert" data-testid="ai-unknown">
          Unknown placeholders: {{ unknown.map(macroText).join(', ') }}. Saving is only possible without them.
        </p>
        <p v-else-if="unused.length" class="small muted m-0" data-testid="ai-unused">
          Not used: {{ unused.map((m) => macroText(m.name)).join(', ') }}.
        </p>
        <div class="row">
          <button class="btn" type="button" :disabled="previewing || unknown.length > 0" @click="showPreview">
            {{ previewing ? 'Filling in…' : 'Preview with today’s data' }}
          </button>
          <button
            class="btn ghost"
            type="button"
            :disabled="!props.active || isDefault"
            data-testid="ai-reset"
            @click="resetPrompt"
          >
            Default prompt
          </button>
        </div>
        <div v-if="preview" class="stack gap-1" data-testid="ai-preview">
          <div class="small muted">
            What the model gets ({{ preview.chars.toLocaleString('en-GB') }} characters,
            {{ tokensText(preview.tokens) }}): the fixed instructions, then your prompt.
          </div>
          <pre class="ai-text">{{ preview.system }}</pre>
          <pre class="ai-text">{{ preview.text }}</pre>
        </div>
      </fieldset>

      <SaveBar
        v-if="props.active"
        :dirty="unsaved.dirty.value"
        :saving="saving || unknown.length > 0"
        label="Save AI settings"
        @discard="unsaved.discard"
      />
    </form>
  </section>
</template>

<style scoped>
.prompt {
  min-height: 18em;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: var(--text-sm);
  line-height: 1.45;
  resize: vertical;
}

.prompt.goals {
  min-height: 9em;
}

.ai-text {
  margin: 0;
  padding: var(--space-3);
  max-height: 24em;
  overflow: auto;
  border-radius: var(--radius-md);
  background: var(--surface-2);
  font-size: var(--text-xs);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
</style>
