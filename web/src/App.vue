<script setup lang="ts">
import { onMounted, onUnmounted } from 'vue'
import { RouterLink, RouterView } from 'vue-router'
import AppIcon, { type IconName } from './components/AppIcon.vue'
import PullToRefresh from './components/PullToRefresh.vue'
import { chartsApart } from './layout'
import { clock } from './lib/format'
import { checkVersion, loadMeta, refresh, refreshNow, state } from './store'

// The main destinations: tabs at the top on wide screens, a bar at the bottom
// on phones. Charts has its own page only on phones whose screen would not
// fit the Overview with its charts.
const nav: { to: string; label: string; short?: string; icon: IconName; apart?: boolean }[] = [
  { to: '/', label: 'Overview', icon: 'home' },
  { to: '/charts', label: 'Charts', icon: 'chart', apart: true },
  { to: '/trips', label: 'Trips', icon: 'route' },
  { to: '/recurring', label: 'Recurring trips', short: 'Recurring', icon: 'repeat' },
  { to: '/activity', label: 'Activity', icon: 'list' },
  { to: '/settings', label: 'Settings', icon: 'sliders' },
]

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
      <div class="topbar-row">
        <div class="brand">
          <img class="brand-mark" src="/icon.svg" alt="" />
          <span><span class="brand-evcc">EVCC </span>Trip Planner</span>
          <span
            v-if="state.meta?.demo"
            class="pill tone-warn"
            title="Seeded demo data and stubbed integrations"
            >DEMO</span
          >
          <span
            v-else-if="state.meta?.stubs"
            class="pill tone-warn"
            title="Some integrations use stubs; see Settings → Connections"
            >STUB DATA</span
          >
        </div>
        <!-- When a refresh fails, the last data stays on screen with its time. -->
        <div class="updated" :title="state.error || undefined">
          <span
            v-if="state.plan"
            class="small num"
            :class="state.error ? 'text-warn' : 'muted'"
            data-testid="updated"
            ><AppIcon v-if="state.error" name="warning" />
            <span class="updated-label">{{ state.error ? 'Not updated ' : 'Updated ' }}</span
            >{{ clock(state.plan.now) }}</span
          >
          <button
            class="btn sm ghost icon-only"
            type="button"
            :aria-label="state.error ? 'Retry' : 'Refresh'"
            :disabled="state.loading"
            data-testid="refresh"
            @click="refreshNow"
          >
            <AppIcon name="refresh" :class="{ spinning: state.loading }" />
          </button>
        </div>
      </div>
      <nav class="tabs" aria-label="Main">
        <RouterLink v-for="n in nav.filter((n) => !n.apart)" :key="n.to" :to="n.to">{{ n.label }}</RouterLink>
      </nav>
    </div>
  </header>

  <div v-if="state.newVersion" class="page pb-0">
    <div class="banner tone-warn row" role="status" data-testid="new-version">
      <span class="grow">A new version of the planner is available.</span>
      <button class="btn sm" type="button" @click="reload">Reload</button>
    </div>
  </div>

  <RouterView />

  <nav class="bottom-nav" aria-label="Main" data-testid="bottom-nav">
    <RouterLink v-for="n in nav.filter((n) => !n.apart || chartsApart)" :key="n.to" :to="n.to">
      <AppIcon :name="n.icon" />
      <span>{{ n.short ?? n.label }}</span>
    </RouterLink>
  </nav>
  <PullToRefresh :refresh="refreshNow" />

  <div class="toasts" aria-live="polite">
    <div v-for="t in state.toasts" :key="t.id" class="toast" :class="t.kind">{{ t.text }}</div>
  </div>
</template>
