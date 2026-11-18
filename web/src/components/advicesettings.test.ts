import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { AdviceSettings } from '../api/types'

const api = vi.hoisted(() => ({ adviceSettings: vi.fn(), saveAdviceSettings: vi.fn() }))
vi.mock('../api/client', () => ({ api }))
vi.mock('../store', () => ({ notify: vi.fn(), refresh: vi.fn() }))

const { default: AdviceSettingsForm } = await import('./AdviceSettingsForm.vue')

const level = (enabled: boolean) => ({
  enabled,
  steps: [
    { minDiscountPct: 20, targetSoc: 50 },
    { minDiscountPct: 30, targetSoc: 80 },
    { minDiscountPct: 50, targetSoc: 100 },
  ],
})
const defaults: AdviceSettings = {
  enabled: true,
  autoApply: false,
  autoMinConfidence: 3,
  autoPostpone: false,
  extraLittle: level(true),
  extraMedium: level(true),
  extraHigh: level(false),
}

describe('AdviceSettingsForm', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    api.adviceSettings.mockResolvedValue(structuredClone(defaults))
  })

  it('greys out acting on the advice in Manual mode', async () => {
    const w = mount(AdviceSettingsForm, { props: { mode: 'manual' } })
    await flushPromises()
    expect(w.get('[data-testid="advice-auto-settings"]').attributes('disabled')).toBeDefined()
    expect(w.get('[data-testid="advice-auto-manual"]').text()).toContain('Not used in Manual mode')
    expect(w.get('[data-testid="advice-form"]').attributes('disabled')).toBeUndefined()
    expect(w.find('[data-testid="advice-ai-off"]').exists()).toBe(false)
  })

  it('acts on the advice in On plug-in and Automatic mode', async () => {
    const w = mount(AdviceSettingsForm, { props: { mode: 'plug_in' } })
    await flushPromises()
    expect(w.get('[data-testid="advice-auto-settings"]').attributes('disabled')).toBeUndefined()
    expect(w.find('[data-testid="save-bar"]').exists()).toBe(true)
  })

  it('is greyed out as a whole in AI mode', async () => {
    const w = mount(AdviceSettingsForm, { props: { mode: 'ai' } })
    await flushPromises()
    expect(w.get('[data-testid="advice-ai-off"]').text()).toContain('AI mode is on')
    expect(w.get('[data-testid="advice-form"]').attributes('disabled')).toBeDefined()
    expect(w.find('[data-testid="save-bar"]').exists()).toBe(false)
  })
})
