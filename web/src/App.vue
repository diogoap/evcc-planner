<script setup lang="ts">
import { onMounted, onUnmounted } from 'vue'
import { RouterLink, RouterView } from 'vue-router'
import { loadMeta, refresh, state } from './store'

let timer: number | undefined

onMounted(async () => {
  try {
    await loadMeta()
  } catch (e) {
    state.error = (e as Error).message
  }
  await refresh()
  // Keep EVCC status and the plan current while the page is open.
  timer = window.setInterval(() => {
    if (document.visibilityState === 'visible') refresh()
  }, 60_000)
})

onUnmounted(() => window.clearInterval(timer))
</script>

<template>
  <header class="topbar">
    <div class="topbar-inner">
      <div class="brand">
        <svg class="brand-mark" viewBox="0 0 32 32" aria-hidden="true">
          <rect width="32" height="32" rx="8" fill="currentColor" opacity="0.08" />
          <path d="M18 4 8 18h7l-2 10 11-15h-7z" fill="var(--accent)" />
        </svg>
        <span>EVCC Trip Planner</span>
        <span v-if="state.meta?.demo" class="pill tone-warn" title="Seeded demo data and stubbed integrations"
          >DEMO</span
        >
        <span
          v-else-if="state.meta?.stubs"
          class="pill tone-warn"
          title="Some integrations use stubs; see Settings → Connections"
          >STUB DATA</span
        >
      </div>
      <nav class="tabs" aria-label="Main">
        <RouterLink to="/">Overview</RouterLink>
        <RouterLink to="/trips">Trips</RouterLink>
        <RouterLink to="/recurring">Recurring trips</RouterLink>
        <RouterLink to="/settings">Settings</RouterLink>
      </nav>
    </div>
  </header>

  <div v-if="state.error" class="page" style="padding-bottom: 0">
    <div class="banner tone-bad" role="alert">{{ state.error }}</div>
  </div>

  <RouterView />

  <div class="toasts" aria-live="polite">
    <div v-for="t in state.toasts" :key="t.id" class="toast" :class="t.kind">{{ t.text }}</div>
  </div>
</template>
