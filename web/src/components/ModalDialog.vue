<script setup lang="ts">
import { nextTick, onMounted, onUnmounted, ref } from 'vue'

defineProps<{ title: string }>()
const emit = defineEmits<{ close: [] }>()
const root = ref<HTMLElement | null>(null)
// Focus goes back here when the dialog closes.
let opener: HTMLElement | null = null

const focusable =
  'a[href], button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea, summary'

function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape') {
    emit('close')
    return
  }
  // Tab stays inside the dialog.
  if (e.key !== 'Tab' || !root.value) return
  const items = [...root.value.querySelectorAll<HTMLElement>(focusable)]
  const first = items[0]
  const last = items[items.length - 1]
  if (!first || !last) return
  const inside = root.value.contains(document.activeElement)
  if (e.shiftKey && (document.activeElement === first || !inside)) {
    e.preventDefault()
    last.focus()
  } else if (!e.shiftKey && (document.activeElement === last || !inside)) {
    e.preventDefault()
    first.focus()
  }
}

onMounted(async () => {
  opener = document.activeElement instanceof HTMLElement ? document.activeElement : null
  document.addEventListener('keydown', onKey)
  await nextTick()
  root.value?.querySelector<HTMLElement>('input, select, button')?.focus()
})
onUnmounted(() => {
  document.removeEventListener('keydown', onKey)
  if (opener?.isConnected) opener.focus()
})
</script>

<template>
  <div class="modal-backdrop" @mousedown.self="emit('close')">
    <div ref="root" class="modal" role="dialog" aria-modal="true" :aria-label="title">
      <h2>{{ title }}</h2>
      <slot />
    </div>
  </div>
</template>
