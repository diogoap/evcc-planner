<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import type { Block } from '../api/types'
import { clock, localDate, relativeDay } from '../lib/format'
import { blockLabel } from '../lib/status'

const props = defineProps<{ block: Block | null; now: string; canSend: boolean }>()
const emit = defineEmits<{ send: [] }>()

const label = computed(() => (props.block ? blockLabel(props.block) : null))
const later = computed(() => props.block?.titles.slice(1) ?? [])
</script>

<template>
  <section class="card" aria-labelledby="next-title">
    <div id="next-title" class="card-title">Next departure</div>
    <template v-if="block">
      <div style="font-size: 1.25rem; font-weight: 650" data-testid="next-departure">
        {{ relativeDay(block.departAt, now) }}, leave {{ clock(block.departAt) }}
      </div>
      <div class="muted">
        {{ block.firstTitle }} · at {{ block.firstDestination }} by {{ clock(block.firstArriveAt) }}
      </div>
      <div v-if="later.length" class="muted small">Then: {{ later.join(', ') }}</div>
      <div class="row" style="margin: 12px 0 6px">
        <span>Required departure SoC</span>
        <span class="spacer" />
        <strong class="num" style="font-size: 1.3rem" data-testid="next-target"
          >{{ block.targetSoc }}%</strong
        >
      </div>
      <p v-if="label" class="small" :class="`text-${label.tone}`" style="margin: 0 0 12px">
        {{ label.text }}
      </p>
      <div class="row">
        <RouterLink class="btn" :to="{ path: '/trips', hash: `#day-${localDate(block.firstArriveAt)}` }"
          >View trips</RouterLink
        >
        <button class="btn primary" type="button" :disabled="!canSend" @click="emit('send')">
          Send plan to EVCC
        </button>
      </div>
    </template>
    <template v-else>
      <p class="muted">No upcoming trips in the next seven days.</p>
      <RouterLink class="btn" to="/trips">Add a trip</RouterLink>
    </template>
  </section>
</template>
