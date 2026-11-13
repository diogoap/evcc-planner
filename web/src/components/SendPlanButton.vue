<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, useId } from 'vue'
import type { SendOption } from '../api/types'
import { km } from '../lib/format'
import { onDay, sendOptionLabel } from '../lib/status'
import AppIcon from './AppIcon.vue'

// "Send plan to EVCC" sends the next departure's own target; the arrow opens
// plans that cover one to three days of trips on one charge.
const props = defineProps<{ options: SendOption[]; now: string; disabled: boolean }>()
const emit = defineEmits<{ send: [option?: SendOption] }>()

const open = ref(false)
const root = ref<HTMLElement | null>(null)
const toggleBtn = ref<HTMLButtonElement | null>(null)
const panel = ref<HTMLElement | null>(null)
const panelId = useId()
const panelLeft = ref(0)

// An option that adds no trips to the previous one is pointless.
function addsNothing(i: number) {
  return i > 0 && props.options[i]!.tripKeys.length === props.options[i - 1]!.tripKeys.length
}

function detail(o: SendOption, i: number) {
  if (addsNothing(i)) return `No trips ${onDay(o.through, props.now)}`
  const n = o.tripKeys.length
  return `${n} ${n === 1 ? 'trip' : 'trips'} · ${km(o.distanceKm)}${o.exceeds ? ' · more than a full battery' : ''}`
}

async function toggle() {
  if (open.value) return close()
  open.value = true
  document.addEventListener('pointerdown', outside)
  place()
  await nextTick()
  panel.value?.querySelector<HTMLButtonElement>('button:not([disabled])')?.focus()
}

// Right-aligned under the arrow, but kept 16px inside the screen.
function place() {
  const box = root.value?.getBoundingClientRect()
  if (!box) return
  const vw = document.documentElement.clientWidth
  const width = Math.min(330, vw - 32)
  const left = Math.min(Math.max(box.right - width, 16), vw - 16 - width)
  panelLeft.value = left - box.left
}

function close(focus = true) {
  open.value = false
  document.removeEventListener('pointerdown', outside)
  if (focus) toggleBtn.value?.focus()
}

function outside(e: PointerEvent) {
  if (!root.value?.contains(e.target as Node)) close(false)
}

function choose(o: SendOption) {
  close(false)
  emit('send', o)
}

onBeforeUnmount(() => document.removeEventListener('pointerdown', outside))
</script>

<template>
  <div ref="root" class="split" @keydown.esc="open && close()">
    <button class="btn primary split-main" type="button" :disabled="disabled" @click="emit('send')">
      Send plan<span class="split-more"> to EVCC</span>
    </button>
    <button
      ref="toggleBtn"
      class="btn primary split-toggle"
      type="button"
      :disabled="disabled || !options.length"
      aria-label="More plans: cover more days"
      aria-haspopup="true"
      :aria-expanded="open"
      :aria-controls="panelId"
      data-testid="send-more"
      @click="toggle"
    >
      <AppIcon name="chevron" :class="{ flipped: open }" />
    </button>
    <div
      v-if="open"
      :id="panelId"
      ref="panel"
      class="split-panel"
      :style="{ left: `${panelLeft}px` }"
      role="group"
      aria-label="Plans covering more days"
    >
      <p class="small muted split-hint">
        Charge once by the next departure for every trip until the end of the chosen day, with no charging in
        between.
      </p>
      <button
        v-for="(o, i) in options"
        :key="o.days"
        class="split-option"
        type="button"
        :disabled="addsNothing(i)"
        :data-testid="`send-option-${o.days}`"
        @click="choose(o)"
      >
        <span class="split-label">{{ sendOptionLabel(o, now) }}</span>
        <span class="split-target">
          <span v-if="o.covered" class="pill tone-ok" data-testid="option-covered">Covered</span>
          <strong class="num" :class="{ 'text-warn': o.exceeds }">{{ o.targetSoc }}%</strong>
        </span>
        <span class="small muted split-detail">{{ detail(o, i) }}</span>
      </button>
    </div>
  </div>
</template>

<style scoped>
.split {
  position: relative;
  display: inline-flex;
}

.split-main {
  border-top-right-radius: 0;
  border-bottom-right-radius: 0;
}

/* Phones say "Send plan"; screen readers still hear "to EVCC". */
@media (max-width: 640px) {
  .split-more {
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip: rect(0 0 0 0);
  }
}

.split-toggle {
  border-top-left-radius: 0;
  border-bottom-left-radius: 0;
  border-left: 1px solid rgba(6, 33, 15, 0.25);
  padding: 7px 11px;
}

.split-toggle .icon {
  width: 16px;
  height: 16px;
}

.split-toggle .icon.flipped {
  transform: rotate(180deg);
}

.split-panel {
  position: absolute;
  top: calc(100% + 6px);
  z-index: 30;
  width: min(330px, calc(100vw - 32px));
  padding: 8px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 14px;
  box-shadow: var(--shadow-lg);
}

.split-hint {
  margin: 4px 8px 8px;
}

.split-option {
  appearance: none;
  display: grid;
  grid-template-columns: 1fr auto;
  gap: 2px 12px;
  width: 100%;
  padding: 10px;
  border: none;
  border-radius: 10px;
  background: none;
  color: var(--text);
  font: inherit;
  text-align: left;
  cursor: pointer;
}

.split-option:hover:not(:disabled),
.split-option:focus-visible {
  background: var(--surface-2);
  outline: none;
}

.split-option:focus-visible {
  box-shadow: inset 0 0 0 2px var(--focus);
}

.split-option:disabled {
  cursor: default;
  opacity: 0.55;
}

.split-target {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}

.split-label {
  font-weight: 600;
}

.split-detail {
  grid-column: 1 / -1;
}
</style>
