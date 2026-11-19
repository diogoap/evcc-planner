<script setup lang="ts">
import { onBeforeUnmount, ref, useId } from 'vue'
import AppIcon from './AppIcon.vue'

// An info button whose details show while hovered, or after a click or tap
// until Escape, a second click, or a click elsewhere.
defineProps<{ label: string }>()

const root = ref<HTMLElement | null>(null)
const button = ref<HTMLButtonElement | null>(null)
const hovered = ref(false)
const pinned = ref(false)
const panelId = useId()
const panelLeft = ref(0)
const panelWidth = ref(360)
let leaveTimer: ReturnType<typeof setTimeout> | undefined

// Under the button, kept 16px inside the screen.
function place() {
  const box = root.value?.getBoundingClientRect()
  if (!box) return
  const vw = document.documentElement.clientWidth
  panelWidth.value = Math.min(360, vw - 32)
  const left = Math.min(Math.max(box.left, 16), vw - 16 - panelWidth.value)
  panelLeft.value = left - box.left
}

function enter() {
  clearTimeout(leaveTimer)
  if (!hovered.value) place()
  hovered.value = true
}

// A short delay lets the pointer cross the gap to the details.
function leave() {
  clearTimeout(leaveTimer)
  leaveTimer = setTimeout(() => (hovered.value = false), 150)
}

function toggle() {
  if (pinned.value) return close(false)
  place()
  pinned.value = true
  document.addEventListener('pointerdown', outside)
}

function close(focus: boolean) {
  pinned.value = hovered.value = false
  document.removeEventListener('pointerdown', outside)
  if (focus) button.value?.focus()
}

function outside(e: PointerEvent) {
  if (!root.value?.contains(e.target as Node)) close(false)
}

onBeforeUnmount(() => {
  clearTimeout(leaveTimer)
  document.removeEventListener('pointerdown', outside)
})
</script>

<template>
  <span
    ref="root"
    class="info-tip"
    @mouseenter="enter"
    @mouseleave="leave"
    @keydown.esc="(hovered || pinned) && close(true)"
  >
    <button
      ref="button"
      class="info-tip-btn"
      type="button"
      :aria-label="label"
      :aria-expanded="hovered || pinned"
      :aria-controls="panelId"
      @click="toggle"
    >
      <AppIcon name="info" />
    </button>
    <span
      v-show="hovered || pinned"
      :id="panelId"
      class="info-tip-panel"
      :style="{ left: `${panelLeft}px`, width: `${panelWidth}px` }"
    >
      <slot />
    </span>
  </span>
</template>

<style scoped>
.info-tip {
  position: relative;
  display: inline-block;
  vertical-align: middle;
}

.info-tip-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  margin: -6px 0;
  padding: 0;
  border: 0;
  border-radius: var(--radius-pill);
  background: transparent;
  color: var(--muted);
  cursor: pointer;
}

.info-tip-btn:hover,
.info-tip-btn[aria-expanded='true'] {
  color: var(--text);
  background: var(--surface-2);
}

.info-tip-btn:focus-visible {
  outline: 2px solid var(--focus);
  outline-offset: 2px;
}

.info-tip-btn .icon {
  width: 16px;
  height: 16px;
}

/* A fingertip-sized target that doesn't make the row taller. */
@media (pointer: coarse) {
  .info-tip-btn {
    width: var(--touch);
    height: var(--touch);
    margin: -12px -8px;
  }
}

.info-tip-panel {
  position: absolute;
  top: calc(100% + 6px);
  z-index: 30;
  display: block;
  padding: var(--space-3);
  background: var(--surface);
  color: var(--text);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  box-shadow: var(--shadow-lg);
  font-weight: normal;
  text-align: left;
  white-space: normal;
}
</style>
