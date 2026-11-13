<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { isStandalone, pullDistance, pullThreshold } from '../lib/pull'
import AppIcon from './AppIcon.vue'

// Pull to refresh in the installed app; a browser tab keeps its own. The
// Refresh button in the top bar does the same for everyone.
const props = defineProps<{ refresh: () => Promise<unknown>; enabled?: boolean }>()

const distance = ref(0)
const refreshing = ref(false)
let startY: number | null = null
const enabled = props.enabled ?? isStandalone()

function onStart(e: TouchEvent) {
  const touch = e.touches[0]
  if (!touch || e.touches.length > 1 || refreshing.value || window.scrollY > 0) return
  // Not while a dialog is open over the page.
  if (document.documentElement.classList.contains('modal-open')) return
  startY = touch.clientY
}

function onMove(e: TouchEvent) {
  const touch = e.touches[0]
  if (startY === null || !touch) return
  distance.value = pullDistance(touch.clientY - startY)
}

async function onEnd() {
  if (startY === null) return
  startY = null
  if (distance.value < pullThreshold) {
    distance.value = 0
    return
  }
  refreshing.value = true
  distance.value = pullThreshold
  try {
    await props.refresh()
  } finally {
    refreshing.value = false
    distance.value = 0
  }
}

function onCancel() {
  startY = null
  if (!refreshing.value) distance.value = 0
}

onMounted(() => {
  if (!enabled) return
  window.addEventListener('touchstart', onStart, { passive: true })
  window.addEventListener('touchmove', onMove, { passive: true })
  window.addEventListener('touchend', onEnd)
  window.addEventListener('touchcancel', onCancel)
})
onUnmounted(() => {
  window.removeEventListener('touchstart', onStart)
  window.removeEventListener('touchmove', onMove)
  window.removeEventListener('touchend', onEnd)
  window.removeEventListener('touchcancel', onCancel)
})

const indicator = computed(() => ({
  transform: `translate(-50%, ${distance.value}px)`,
  opacity: Math.min(1, distance.value / pullThreshold),
}))
</script>

<template>
  <div
    v-if="distance > 0 || refreshing"
    class="pull"
    :class="{ ready: distance >= pullThreshold }"
    :style="indicator"
    aria-hidden="true"
    data-testid="pull"
  >
    <AppIcon
      name="refresh"
      :class="{ spinning: refreshing }"
      :style="refreshing ? undefined : { transform: `rotate(${distance * 3}deg)` }"
    />
  </div>
</template>
