<script setup lang="ts">
import { nextTick, onMounted, onUnmounted, ref } from 'vue'

defineProps<{ title: string }>()
const emit = defineEmits<{ close: [] }>()
const root = ref<HTMLElement | null>(null)

function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape') emit('close')
}

onMounted(async () => {
  document.addEventListener('keydown', onKey)
  await nextTick()
  root.value?.querySelector<HTMLElement>('input, select, button')?.focus()
})
onUnmounted(() => document.removeEventListener('keydown', onKey))
</script>

<template>
  <div class="modal-backdrop" @mousedown.self="emit('close')">
    <div ref="root" class="modal" role="dialog" aria-modal="true" :aria-label="title">
      <h2>{{ title }}</h2>
      <slot />
    </div>
  </div>
</template>
