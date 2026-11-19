<script setup lang="ts">
import type { NotificationCopy, NotificationVerb } from '../api/types'
import { copySegments } from '../lib/copy'

// What notifications say: title, message, and buttons, with the parts
// filled in when sent marked.
const props = defineProps<{ copies: NotificationCopy[]; snoozeMin: number }>()

const verbTitles: Record<NotificationVerb, string> = {
  snooze: '',
  skip: 'Skip trip',
  view: 'View plan',
  count: 'Count it',
  notrip: 'Not a trip',
}

function buttons(c: NotificationCopy) {
  return c.buttons
    .map((v) => (v === 'snooze' ? `Remind in ${props.snoozeMin} min` : verbTitles[v]))
    .join(' · ')
}
</script>

<template>
  <span class="stack gap-3 small">
    <span v-for="(c, i) in copies" :key="i" class="stack gap-1" data-testid="notification-copy">
      <strong>
        {{ c.titlePrefix }}
        <template v-for="(s, j) in copySegments(c.title)" :key="j">
          <span :class="`copy-${s.kind}`">{{ s.text }}</span>
        </template>
        <template v-if="c.aiSuffix">
          {{ ' ' }}<span class="copy-bracket">[</span> {{ c.aiSuffix }}<span class="copy-bracket">]</span>
        </template>
      </strong>
      <span v-for="(m, j) in c.messages" :key="j" class="copy-message">
        <template v-for="(s, k) in copySegments(m)" :key="k">
          <span :class="`copy-${s.kind}`">{{ s.text }}</span>
        </template>
      </span>
      <span v-if="c.messages.length > 1" class="muted">One of these, as it applies.</span>
      <span v-if="c.note" class="muted">{{ c.note }}</span>
      <span class="muted">Buttons: {{ buttons(c) }}</span>
    </span>
    <span class="muted copy-legend">
      <span class="copy-slot">{…}</span> is filled in when it is sent;
      <span class="copy-bracket">[</span>…<span class="copy-bracket">]</span>
      only when it applies.
      <template v-if="copies.some((c) => c.aiSuffix)">
        In AI mode, the title ends with “{{ copies.find((c) => c.aiSuffix)?.aiSuffix }}”.
      </template>
    </span>
  </span>
</template>

<style scoped>
.copy-message {
  display: block;
  padding-left: var(--space-2);
  border-left: 2px solid var(--border);
}

.copy-slot {
  color: var(--info);
  font-weight: var(--weight-medium);
}

.copy-bracket {
  color: var(--muted);
  font-weight: var(--weight-bold);
}

.copy-legend {
  padding-top: var(--space-2);
  border-top: 1px solid var(--border);
}
</style>
