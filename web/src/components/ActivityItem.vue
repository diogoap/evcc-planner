<script setup lang="ts">
import { computed } from 'vue'
import type { AuditEvent } from '../api/types'
import { callLine, changeRows, detailRows, eventHistory, sourceLabels, subjectLabel } from '../lib/activity'
import { clock } from '../lib/format'

// showHistory: offer a link to the event's subject history (off while
// that history is already shown).
const props = defineProps<{ event: AuditEvent; showHistory?: boolean }>()
const emit = defineEmits<{ history: [subject: string, label: string] }>()

const rows = computed(() => changeRows(props.event))
const details = computed(() => detailRows(props.event))
const history = computed(() => (props.showHistory ? eventHistory(props.event) : undefined))
const failed = computed(() => props.event.outcome === 'failure')
const isCall = computed(() => props.event.type === 'api_call')
</script>

<template>
  <li class="activity" :class="{ failed }" :data-testid="`event-${event.id}`">
    <span class="num muted activity-time">{{ clock(event.at) }}</span>
    <div class="activity-main">
      <div class="activity-summary">{{ event.summary }}</div>
      <div class="activity-tags small">
        <span class="pill">{{ subjectLabel(event) }}</span>
        <span class="pill" :class="event.source === 'automation' ? 'tone-info' : ''">{{
          sourceLabels[event.source] ?? event.source
        }}</span>
        <span v-if="failed" class="pill tone-bad">Failed</span>
        <span v-if="isCall" class="muted">{{ callLine(event) }}</span>
        <button
          v-if="history"
          class="history-link"
          type="button"
          :aria-label="`History of ${history.label}`"
          @click="emit('history', history.subject, history.label)"
        >
          History
        </button>
      </div>
      <div v-if="event.error" class="small text-bad" data-testid="event-error">{{ event.error }}</div>
      <table v-if="rows.length" class="changes small" data-testid="event-changes">
        <tbody>
          <tr v-for="r in rows" :key="r.key">
            <th scope="row">{{ r.label }}</th>
            <td v-if="r.before !== undefined && r.after !== undefined">
              <span class="muted">{{ r.before }}</span> <span aria-label="changed to">→</span> {{ r.after }}
            </td>
            <td v-else-if="r.after !== undefined">{{ r.after }}</td>
            <td v-else>
              <span class="muted">{{ r.before }}</span> <span class="small muted">(removed)</span>
            </td>
          </tr>
        </tbody>
      </table>
      <p
        v-else-if="event.type === 'action' && (event.before || event.after)"
        class="small muted changes-none"
      >
        No values changed.
      </p>
      <details v-if="details.length" class="small activity-details">
        <summary class="muted">Details</summary>
        <dl class="kv">
          <template v-for="d in details" :key="d.key">
            <dt>{{ d.label }}</dt>
            <dd>{{ d.value }}</dd>
          </template>
        </dl>
      </details>
    </div>
  </li>
</template>

<style scoped>
.activity {
  display: flex;
  gap: 12px;
  padding: 12px 0;
  border-top: 1px solid var(--border);
  list-style: none;
}

.activity:first-child {
  border-top: none;
}

.activity-time {
  min-width: 44px;
  padding-top: 1px;
}

.activity-main {
  flex: 1;
  min-width: 0;
}

.activity-summary {
  font-weight: 550;
  overflow-wrap: anywhere;
}

.activity.failed .activity-summary {
  color: var(--bad);
}

.activity-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 6px;
  align-items: center;
  margin-top: 4px;
}

.changes {
  margin-top: 6px;
  border-collapse: collapse;
}

.changes th {
  padding: 2px 12px 2px 0;
  font-weight: 500;
  color: var(--muted);
  text-align: left;
  vertical-align: top;
  white-space: nowrap;
}

.changes td {
  padding: 2px 0;
  overflow-wrap: anywhere;
}

.changes-none {
  margin: 6px 0 0;
}

.history-link {
  padding: 2px 4px;
  border: none;
  background: none;
  color: var(--info);
  font: inherit;
  font-weight: 550;
  cursor: pointer;
}

.history-link:hover {
  text-decoration: underline;
}

.activity-details {
  margin-top: 4px;
}

.activity-details dd {
  overflow-wrap: anywhere;
}
</style>
