<script setup lang="ts">
import { RouterLink } from 'vue-router'
import type { HealthIssue } from '../api/types'
import { clock, dateTime, localDate } from '../lib/format'
import AppIcon from './AppIcon.vue'

// The connections that need attention, at the top of the Overview: each
// with what went wrong and when it last worked (and, for a rejected key,
// what to do), and a link to Settings → Connections. Shown only while
// there is something; in the app only.
const props = defineProps<{ issues: HealthIssue[]; now: string }>()

function lastWorked(at: string) {
  return localDate(at) === localDate(props.now) ? clock(at) : dateTime(at)
}
</script>

<template>
  <div v-if="issues.length" class="banner tone-warn row health" role="status" data-testid="health-line">
    <AppIcon name="warning" />
    <ul class="list-plain grow stack gap-1 m-0">
      <li v-for="i in issues" :key="i.name" :data-testid="`health-${i.name}`">
        <strong>{{ i.name }}</strong
        >: {{ i.detail
        }}<template v-if="i.lastSuccessAt"> · last worked {{ lastWorked(i.lastSuccessAt) }}</template>
        <div v-if="i.fix" class="strong">{{ i.fix }}</div>
      </li>
    </ul>
    <RouterLink class="btn sm" :to="{ name: 'settings', query: { tab: 'connections' } }"
      >Connections</RouterLink
    >
  </div>
</template>

<style scoped>
.health {
  align-items: flex-start;
}

/* Long error messages wrap instead of widening the page. */
.health li {
  overflow-wrap: anywhere;
}
</style>
