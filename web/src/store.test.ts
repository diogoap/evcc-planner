import { beforeEach, describe, expect, it, vi } from 'vitest'

const api = vi.hoisted(() => ({ meta: vi.fn() }))
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
