import { beforeEach, describe, expect, it, vi } from 'vitest'

const api = vi.hoisted(() => ({ meta: vi.fn(), syncCalendar: vi.fn(), plan: vi.fn(), status: vi.fn() }))
vi.mock('./api/client', () => ({ api }))

const meta = (build: string) => ({
  demo: false,
  stubs: false,
  timezone: 'Europe/Amsterdam',
  now: '2026-11-11T13:00:00Z',
  windowDays: 7,
  build,
})

describe('checkVersion', () => {
  beforeEach(() => {
    vi.resetModules()
    api.meta.mockReset()
  })

  it('notices a deploy since the page loaded', async () => {
    const { checkVersion, loadMeta, state } = await import('./store')
    api.meta.mockResolvedValueOnce(meta('aaa111'))
    await loadMeta()
    api.meta.mockResolvedValueOnce(meta('aaa111'))
    expect(await checkVersion()).toBe(false)
    api.meta.mockResolvedValueOnce(meta('bbb222'))
    expect(await checkVersion()).toBe(true)
    expect(state.newVersion).toBe(true)
  })

  it('ignores development (no build) and unreachable servers', async () => {
    const { checkVersion, loadMeta } = await import('./store')
    api.meta.mockResolvedValueOnce(meta(''))
    await loadMeta()
    api.meta.mockResolvedValueOnce(meta('bbb222'))
    expect(await checkVersion()).toBe(false)
    api.meta.mockRejectedValueOnce(new Error('offline'))
    expect(await checkVersion()).toBe(false)
  })
})

describe('syncCalendar', () => {
  beforeEach(() => {
    vi.resetModules()
    vi.resetAllMocks()
    api.plan.mockResolvedValue({})
    api.status.mockResolvedValue({})
  })

  const calendar = (status: string, detail: string) => [
    { name: 'EVCC', status: 'ok', detail: '' },
    { name: 'Google Calendar', status, detail },
  ]

  it('says the calendar synced and reloads the plan', async () => {
    const { state, syncCalendar } = await import('./store')
    api.syncCalendar.mockResolvedValue(calendar('ok', 'Family: 12 events'))
    expect(await syncCalendar()).toHaveLength(2)
    expect(state.toasts.map((t) => [t.kind, t.text])).toEqual([['ok', 'Calendar synced']])
    expect(api.plan).toHaveBeenCalled()
  })

  it("says why a calendar didn't sync", async () => {
    const { state, syncCalendar } = await import('./store')
    api.syncCalendar.mockResolvedValue(calendar('error', 'Family: feed returned 404'))
    await syncCalendar()
    expect(state.toasts.map((t) => [t.kind, t.text])).toEqual([
      ['error', 'Calendar not synced: Family: feed returned 404'],
    ])
  })

  it('reports an unreachable planner and changes nothing', async () => {
    const { state, syncCalendar } = await import('./store')
    api.syncCalendar.mockRejectedValue(new Error('The planner service is not reachable.'))
    expect(await syncCalendar()).toBeUndefined()
    expect(state.toasts.map((t) => t.text)).toEqual(['The planner service is not reachable.'])
    expect(api.plan).not.toHaveBeenCalled()
  })
})
