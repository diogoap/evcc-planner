<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api } from '../api/client'
import type { AIRun, AIView } from '../api/types'
import { confidenceNames } from '../lib/advice'
import { recommendationText, tokensText, triggerNames } from '../lib/ai'
import { dateTime, dayShort } from '../lib/format'
import { notify, refresh } from '../store'

// AI mode's status: whether an AI plan applies, the latest run with its
// decision and what the planner corrected, Run now, and what was sent.
// active: AI mode is on; otherwise Run now is off.
const props = defineProps<{ active: boolean }>()
const view = ref<AIView | null>(null)
const error = ref('')
const running = ref(false)
const detail = ref<AIRun | null>(null)
const loadingDetail = ref(false)

async function load() {
  try {
    view.value = await api.ai()
    error.value = ''
  } catch (e) {
    error.value = (e as Error).message
  }
}
onMounted(load)
defineExpose({ load })

const run = computed(() => view.value?.latest ?? null)
const result = computed(() => run.value?.result ?? null)
// The prompt's size: the model's count, else an estimate.
const sentSize = computed(() => {
  const d = detail.value
  if (!d) return ''
  return d.promptTokens
    ? `${d.promptTokens.toLocaleString('en-GB')} tokens`
    : tokensText((d.prompt?.length ?? 0) / 4)
})
const statusTone = computed(() => {
  const v = view.value
  if (!v?.active) return 'muted'
  if (v.inUse) return 'ok'
  return 'warn'
})

async function runNow() {
  running.value = true
  detail.value = null
  try {
    view.value = await api.runAI()
    const latest = view.value.latest
    if (latest?.status === 'failed') notify(`AI plan failed: ${latest.error ?? ''}`, 'error')
    else notify(view.value.active ? 'AI plan made and applied' : 'AI plan made (AI mode is off)')
    refresh()
  } catch (e) {
    notify((e as Error).message, 'error')
  } finally {
    running.value = false
  }
}

async function showDetail(open: boolean) {
  if (!open || !run.value || detail.value?.id === run.value.id) return
  loadingDetail.value = true
  try {
    detail.value = await api.aiRun(run.value.id)
  } catch (e) {
    notify((e as Error).message, 'error')
  } finally {
    loadingDetail.value = false
  }
}
</script>

<template>
  <section class="card" aria-labelledby="ai-run-title" data-testid="ai-run">
    <div class="card-title">
      <span id="ai-run-title">AI plan</span>
      <button
        class="btn sm primary"
        type="button"
        :disabled="!props.active || running || view?.running || !view?.decider"
        data-testid="ai-run-now"
        @click="runNow"
      >
        {{ running || view?.running ? 'Running…' : 'Run now' }}
      </button>
    </div>
    <div v-if="error" class="banner tone-bad" role="alert">{{ error }}</div>
    <div v-if="view" class="stack gap-3">
      <div class="banner small" :class="`tone-${statusTone}`" role="status" data-testid="ai-status">
        {{ view.status }}
      </div>
      <p v-if="view.note" class="small text-warn m-0" data-testid="ai-note">{{ view.note }}</p>
      <dl class="kv kv-left small m-0">
        <dt>Decided by</dt>
        <dd data-testid="ai-decider">{{ view.decider || 'No AI model' }}</dd>
        <dt>Runs today</dt>
        <dd>
          {{ view.runsToday }} of {{ view.maxRuns }} automatic
          <span class="muted">(runs by hand are always allowed)</span>
        </dd>
      </dl>

      <template v-if="run">
        <div class="row small">
          <strong>Latest run</strong>
          <span
            class="pill"
            :class="run.status === 'ok' ? 'tone-ok' : 'tone-bad'"
            data-testid="ai-run-status"
            >{{ run.status === 'ok' ? 'OK' : 'Failed' }}</span
          >
          <span class="muted"
            >{{ triggerNames[run.trigger] }} · {{ dateTime(run.finishedAt) }} · {{ run.model }}
            <template v-if="run.promptTokens"
              >· {{ run.promptTokens.toLocaleString('en-GB') }} +
              {{ run.completionTokens.toLocaleString('en-GB') }} tokens</template
            ></span
          >
        </div>
        <p v-if="run.error" class="banner tone-bad small m-0">{{ run.error }}</p>
        <template v-if="result">
          <p class="m-0" data-testid="ai-summary">{{ result.summary }}</p>
          <div v-if="result.ahead" class="small">
            <strong>Next trip:</strong> {{ result.ahead.targetSoc }}%<template
              v-if="result.ahead.tripIds.length"
            >
              (also {{ result.ahead.tripIds.join(', ') }})</template
            >. <span class="muted">{{ result.ahead.reason }}</span>
          </div>
          <ul class="list-plain stack gap-2 small m-0" data-testid="ai-days">
            <li v-for="d in result.days" :key="d.date">
              <strong>{{ dayShort(d.date) }}:</strong> {{ recommendationText(d) }}
              <span class="muted">· {{ confidenceNames[d.confidence] }} confidence</span>
              <div class="muted">{{ d.summary }}</div>
            </li>
          </ul>
          <div v-if="result.important.length" class="small">
            <strong>What mattered</strong>
            <ul class="list-indent my-1">
              <li v-for="i in result.important" :key="i">{{ i }}</li>
            </ul>
          </div>
          <div v-if="result.warnings.length" class="banner tone-warn small" data-testid="ai-warnings">
            <strong>Corrected by the planner</strong>
            <ul class="list-indent my-1">
              <li v-for="w in result.warnings" :key="w">{{ w }}</li>
            </ul>
          </div>
        </template>
        <details @toggle="showDetail(($event.target as HTMLDetailsElement).open)">
          <summary class="small">What was sent and what came back</summary>
          <p v-if="loadingDetail" class="small muted">Loading…</p>
          <template v-else-if="detail">
            <div class="small muted mt-2">Prompt, with the data filled in ({{ sentSize }})</div>
            <pre class="ai-text" data-testid="ai-sent">{{ detail.prompt }}</pre>
            <div class="small muted">Answer</div>
            <pre class="ai-text">{{ detail.response || '—' }}</pre>
          </template>
        </details>
      </template>
      <p v-else class="small muted m-0">No AI plan made yet.</p>
    </div>
  </section>
</template>

<style scoped>
.ai-text {
  margin: var(--space-1) 0 var(--space-3);
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
