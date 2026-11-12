<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import type { Block, SendOption } from '../api/types'
import { clock, localDate, relativeDay } from '../lib/format'
import { blockLabel } from '../lib/status'
import SendPlanButton from './SendPlanButton.vue'

const props = defineProps<{ block: Block | null; now: string; canSend: boolean; options?: SendOption[] }>()
const emit = defineEmits<{ send: [option?: SendOption] }>()

const label = computed(() => (props.block ? blockLabel(props.block) : null))
const later = computed(() => props.block?.titles.slice(1) ?? [])
</script>

<template>
  <section class="card" aria-labelledby="next-title">
    <div id="next-title" class="card-title">Next departure</div>
    <template v-if="block">
      <div class="text-xl strong" data-testid="next-departure">
        {{ relativeDay(block.departAt, now) }}, leave {{ clock(block.departAt) }}
      </div>
      <div class="muted">
        {{ block.firstTitle }} · at {{ block.firstDestination }} by {{ clock(block.firstArriveAt) }}
      </div>
      <div v-if="later.length" class="muted small">Then: {{ later.join(', ') }}</div>
      <div class="row m-0 mt-3 mb-2">
        <span>Required departure SoC</span>
        <span class="spacer" />
        <strong class="num text-xl" data-testid="next-target">{{ block.targetSoc }}%</strong>
      </div>
      <p v-if="label" class="small m-0 mb-3" :class="`text-${label.tone}`">
        {{ label.text }}
      </p>
      <div class="row">
        <RouterLink class="btn" :to="{ path: '/trips', hash: `#day-${localDate(block.firstArriveAt)}` }"
          >View trips</RouterLink
        >
        <SendPlanButton
          :options="options ?? []"
          :now="now"
          :disabled="!canSend"
          @send="(o?: SendOption) => emit('send', o)"
        />
      </div>
    </template>
    <template v-else>
      <p class="muted">No upcoming trips in the next seven days.</p>
      <RouterLink class="btn" to="/trips">Add a trip</RouterLink>
    </template>
  </section>
</template>
