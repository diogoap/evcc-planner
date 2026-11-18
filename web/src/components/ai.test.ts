import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { AISettings, AISettingsView, AIView } from '../api/types'

const api = vi.hoisted(() => ({
  aiSettings: vi.fn(),
  saveAISettings: vi.fn(),
  previewAI: vi.fn(),
  ai: vi.fn(),
  runAI: vi.fn(),
  aiRun: vi.fn(),
}))
vi.mock('../api/client', () => ({ api }))
const store = vi.hoisted(() => ({ notify: vi.fn(), refresh: vi.fn() }))
vi.mock('../store', () => store)

const { default: AISettingsForm } = await import('./AISettingsForm.vue')
const { default: AIRunCard } = await import('./AIRunCard.vue')

const defaultPrompt = 'It is {{now}}.\n{{goals}}\n{{trips}}'
const defaultGoals = "- Every trip has enough charge.\n- Battery care: EVCC's charge limit is 80%."
const view = (over: Partial<AISettings> = {}): AISettingsView => ({
  settings: {
    maxRunsPerDay: 3,
    onPrices: true,
    onPlugIn: true,
    model: 'anthropic/claude-haiku-latest',
    prompt: defaultPrompt,
    goals: defaultGoals,
    autoApply: false,
    autoMinConfidence: 3,
    autoPostpone: false,
    autoSet: false,
    ...over,
  },
  defaultPrompt,
  defaultGoals,
  macros: [
    { name: 'now', description: 'The current date and time.' },
    { name: 'goals', description: 'Your goals.' },
    { name: 'trips', description: 'Trips of the next 7 days.' },
    { name: 'energy_prices', description: 'Published prices.' },
  ],
  defaultModel: 'anthropic/claude-haiku-latest',
  decider: 'OpenRouter anthropic/claude-haiku-latest',
})

describe('AISettingsForm', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    api.aiSettings.mockResolvedValue(view())
    api.saveAISettings.mockImplementation((s: AISettings) => Promise.resolve(view(s)))
    api.previewAI.mockResolvedValue({
      system: 'Answer in JSON.',
      text: 'It is Monday.',
      chars: 30,
      tokens: 8,
    })
  })

  it('puts a placeholder in at the cursor and saves the prompt', async () => {
    const w = mount(AISettingsForm, { props: { active: true }, attachTo: document.body })
    await flushPromises()
    const area = w.get('[data-testid="ai-prompt"]').element as HTMLTextAreaElement
    expect(area.value).toBe(defaultPrompt)
    expect(w.get('[data-testid="ai-unused"]').text()).toBe('Not used: {{energy_prices}}.')
    expect(w.get('[data-testid="ai-reset"]').attributes('disabled')).toBeDefined()

    area.setSelectionRange(area.value.length, area.value.length)
    await w.get('[aria-label^="Insert {{energy_prices}}"]').trigger('click')
    expect(area.value).toBe(`${defaultPrompt}{{energy_prices}}`)
    expect(w.find('[data-testid="ai-unused"]').exists()).toBe(false)
    expect(w.emitted('dirty')?.at(-1)).toEqual([true])

    await w.get('form').trigger('submit')
    await flushPromises()
    expect(api.saveAISettings).toHaveBeenCalledWith(
      expect.objectContaining({ prompt: `${defaultPrompt}{{energy_prices}}` }),
    )
    expect(store.notify).toHaveBeenCalledWith('AI settings saved')
    expect(w.emitted('saved')).toHaveLength(1)
    w.unmount()
  })

  it('shows the default model with an example of another', async () => {
    const w = mount(AISettingsForm, { props: { active: true } })
    await flushPromises()
    expect((w.get('[data-testid="ai-model"]').element as HTMLInputElement).value).toBe(
      'anthropic/claude-haiku-latest',
    )
    expect(w.get('#hint-ai-model').text()).toContain('e.g. openai/gpt-luna-latest')
  })

  it('shows the goals, saves your own, and puts the default back', async () => {
    const w = mount(AISettingsForm, { props: { active: true } })
    await flushPromises()
    const goals = w.get('[data-testid="ai-goals"]')
    expect((goals.element as HTMLTextAreaElement).value).toBe(defaultGoals)
    expect(w.get('[data-testid="ai-goals-reset"]').attributes('disabled')).toBeDefined()
    await goals.setValue('- Charge to 100% when prices are negative.')
    expect(w.get('[data-testid="ai-goals-reset"]').attributes('disabled')).toBeUndefined()
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(api.saveAISettings).toHaveBeenCalledWith(
      expect.objectContaining({ goals: '- Charge to 100% when prices are negative.' }),
    )
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    await w.get('[data-testid="ai-goals-reset"]').trigger('click')
    expect((goals.element as HTMLTextAreaElement).value).toBe(defaultGoals)
  })

  it('warns about unknown placeholders and blocks saving', async () => {
    api.aiSettings.mockResolvedValue(view({ prompt: 'Plan {{prices}}' }))
    const w = mount(AISettingsForm, { props: { active: true } })
    await flushPromises()
    expect(w.get('[data-testid="ai-unknown"]').text()).toContain('Unknown placeholders: {{prices}}')
    await w.get('[data-testid="ai-prompt"]').setValue('Plan {{prices}} now')
    expect(w.get('[data-testid="save-bar"] button[type="submit"]').attributes('disabled')).toBeDefined()
  })

  it('is greyed out outside AI mode, but still previews', async () => {
    const w = mount(AISettingsForm, { props: { active: false } })
    await flushPromises()
    expect(w.get('[data-testid="ai-runs"]').attributes('disabled')).toBeDefined()
    expect(w.get('[data-testid="ai-auto"]').attributes('disabled')).toBeDefined()
    expect(w.get('[data-testid="ai-prompt"]').attributes('readonly')).toBeDefined()
    expect(w.get('[aria-label^="Insert {{now}}"]').attributes('disabled')).toBeDefined()
    const preview = w.findAll('button').find((b) => b.text().startsWith('Preview'))!
    expect(preview.attributes('disabled')).toBeUndefined()
    await preview.trigger('click')
    await flushPromises()
    expect(api.previewAI).toHaveBeenCalled()
  })

  it('previews the prompt with the data filled in', async () => {
    const w = mount(AISettingsForm, { props: { active: true } })
    await flushPromises()
    await w
      .findAll('button')
      .find((b) => b.text().startsWith('Preview'))!
      .trigger('click')
    await flushPromises()
    expect(api.previewAI).toHaveBeenCalledWith(defaultPrompt, defaultGoals)
    expect(w.get('[data-testid="ai-preview"]').text()).toContain('It is Monday.')
  })
})

describe('AIRunCard', () => {
  const status: AIView = {
    active: true,
    running: false,
    decider: 'OpenRouter anthropic/claude-haiku-5.5',
    runsToday: 1,
    maxRuns: 3,
    inUse: true,
    status: 'In use: the AI plan from Mon 9 Nov, 19:00.',
    latest: {
      id: 4,
      startedAt: '2026-11-09T18:00:00Z',
      finishedAt: '2026-11-09T18:00:00Z',
      trigger: 'prices',
      status: 'ok',
      decider: 'OpenRouter anthropic/claude-haiku-5.5',
      model: 'anthropic/claude-haiku-5.5',
      promptTokens: 2300,
      completionTokens: 600,
      result: {
        summary: 'Charge tonight for Tuesday.',
        important: ['Tuesday night averages €0.17'],
        ahead: { targetSoc: 64, tripIds: ['T2'], reason: 'Cheaper tonight.' },
        days: [
          {
            date: '2026-11-10',
            recommendation: 'charge_extra',
            targetSoc: 75,
            from: '2026-11-10T00:00:00Z',
            until: '2026-11-10T04:00:00Z',
            confidence: 3,
            summary: 'The cheapest night.',
            rationale: [],
          },
        ],
        warnings: ['Lowered the charge-ahead target from 95% to the cap of 90%.'],
      },
    },
    current: null,
  }

  beforeEach(() => {
    vi.clearAllMocks()
    api.ai.mockResolvedValue(status)
    api.runAI.mockResolvedValue(status)
    api.aiRun.mockResolvedValue({
      ...status.latest,
      prompt: 'It is Monday. T1 …',
      response: '{"summary":"x"}',
    })
  })

  it('shows the latest decision and what the planner corrected', async () => {
    const w = mount(AIRunCard, { props: { active: true } })
    await flushPromises()
    expect(w.get('[data-testid="ai-status"]').text()).toBe('In use: the AI plan from Mon 9 Nov, 19:00.')
    expect(w.get('[data-testid="ai-summary"]').text()).toBe('Charge tonight for Tuesday.')
    expect(w.get('[data-testid="ai-days"]').text()).toContain('Tue 10: Charge extra to 75%, 01:00–05:00')
    expect(w.get('[data-testid="ai-warnings"]').text()).toContain('cap of 90%')
    expect(w.text()).toContain('New prices')
  })

  it('cannot run outside AI mode', async () => {
    const w = mount(AIRunCard, { props: { active: false } })
    await flushPromises()
    expect(w.get('[data-testid="ai-run-now"]').attributes('disabled')).toBeDefined()
  })

  it('runs now and loads what was sent on request', async () => {
    const w = mount(AIRunCard, { props: { active: true } })
    await flushPromises()
    await w.get('[data-testid="ai-run-now"]').trigger('click')
    await flushPromises()
    expect(api.runAI).toHaveBeenCalled()
    expect(store.notify).toHaveBeenCalledWith('AI plan made and applied')
    expect(store.refresh).toHaveBeenCalled()
    const details = w.get('details').element as HTMLDetailsElement
    details.open = true
    await w.get('details').trigger('toggle')
    await flushPromises()
    expect(api.aiRun).toHaveBeenCalledWith(4)
    expect(w.get('[data-testid="ai-sent"]').text()).toBe('It is Monday. T1 …')
  })
})
