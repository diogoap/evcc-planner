import { reactive } from 'vue'
import { api } from './api/client'
import type { Meta, Plan, Status } from './api/types'
import { setTimeZone } from './lib/format'

interface Toast {
  id: number
  text: string
  kind: 'ok' | 'error'
}

// Shared app state: the plan and EVCC status are loaded once and refreshed
// after edits so every page shows the same numbers.
export const state = reactive({
  meta: null as Meta | null,
  plan: null as Plan | null,
  status: null as Status | null,
  loading: false,
  error: '',
  toasts: [] as Toast[],
  /** The server serves a newer UI than this page (a deploy since it loaded). */
  newVersion: false,
})

// The UI build this page was loaded with.
let build: string | undefined

let toastId = 0

export function notify(text: string, kind: Toast['kind'] = 'ok') {
  const id = ++toastId
  state.toasts.push({ id, text, kind })
  setTimeout(() => {
    const i = state.toasts.findIndex((t) => t.id === id)
    if (i >= 0) state.toasts.splice(i, 1)
  }, 4000)
}

export async function loadMeta() {
  state.meta = await api.meta()
  build ??= state.meta.build
  setTimeZone(state.meta.timezone)
}

/** Checks whether the server now serves a newer UI than this page. */
export async function checkVersion() {
  try {
    await loadMeta()
  } catch {
    return false
  }
  state.newVersion = !!build && !!state.meta?.build && state.meta.build !== build
  return state.newVersion
}

export async function refresh() {
  state.loading = true
  try {
    const [plan, status] = await Promise.all([api.plan(), api.status()])
    state.plan = plan
    state.status = status
    state.error = ''
  } catch (e) {
    state.error = (e as Error).message
  } finally {
    state.loading = false
  }
}

/** Runs a mutation, reports its outcome, and reloads the plan. */
export async function mutate(action: () => Promise<unknown>, success?: string) {
  try {
    await action()
    if (success) notify(success)
    await refresh()
    return true
  } catch (e) {
    notify((e as Error).message, 'error')
    return false
  }
}
