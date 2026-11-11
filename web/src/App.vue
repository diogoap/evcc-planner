<script setup lang="ts">
import { onMounted, onUnmounted } from 'vue'
import { RouterLink, RouterView } from 'vue-router'
import { checkVersion, loadMeta, refresh, state } from './store'

let timer: number | undefined

onMounted(async () => {
  try {
    await loadMeta()
  } catch (e) {
    state.error = (e as Error).message
  }
  await refresh()
  // Keep EVCC status and the plan current while the page is open, and
  // notice a deploy (an older page would send outdated requests).
  timer = window.setInterval(() => {
    if (document.visibilityState === 'visible') {
      checkVersion()
      refresh()
    }
  }, 60_000)
  document.addEventListener('visibilitychange', resumed)
})

onUnmounted(() => {
  window.clearInterval(timer)
  document.removeEventListener('visibilitychange', resumed)
})

// Back to the page (e.g. the installed app): reload after a deploy, before
// anything is edited; otherwise just refresh.
async function resumed() {
  if (document.visibilityState !== 'visible') return
  if (await checkVersion()) {
    window.location.reload()
    return
  }
  refresh()
}

function reload() {
  window.location.reload()
}
</script>

<template>
  <header class="topbar">
    <div class="topbar-inner">
      <div class="brand">
        <img class="brand-mark" src="/icon.svg" alt="" />
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
        <RouterLink to="/activity">Activity</RouterLink>
      </nav>
    </div>
  </header>

  <div v-if="state.newVersion" class="page" style="padding-bottom: 0">
    <div class="banner tone-warn row" role="status" data-testid="new-version">
      <span style="flex: 1">A new version of the planner is available.</span>
      <button class="btn sm" type="button" @click="reload">Reload</button>
    </div>
  </div>
  <div v-if="state.error" class="page" style="padding-bottom: 0">
    <div class="banner tone-bad" role="alert">{{ state.error }}</div>
  </div>

  <RouterView />

  <div class="toasts" aria-live="polite">
    <div v-for="t in state.toasts" :key="t.id" class="toast" :class="t.kind">{{ t.text }}</div>
  </div>
</template>
