import { createRouter, createWebHistory } from 'vue-router'
import OverviewView from './views/OverviewView.vue'

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', name: 'overview', component: OverviewView, meta: { title: 'Overview' } },
    {
      path: '/trips',
      name: 'trips',
      component: () => import('./views/TripsView.vue'),
      meta: { title: 'Trips' },
    },
    {
      path: '/recurring',
      name: 'recurring',
      component: () => import('./views/RecurringView.vue'),
      meta: { title: 'Recurring trips' },
    },
    {
      path: '/settings',
      name: 'settings',
      component: () => import('./views/SettingsView.vue'),
      meta: { title: 'Settings' },
    },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
  scrollBehavior(to) {
    if (to.hash) return { el: to.hash, top: 12 }
    return { top: 0 }
  },
})

router.afterEach((to) => {
  document.title = `${to.meta.title ?? 'Overview'} · EVCC Trip Planner`
})
