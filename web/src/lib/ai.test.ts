import { describe, expect, it } from 'vitest'
import type { AIMacro } from '../api/types'
import {
  insertMacro,
  modelLabel,
  monthlyText,
  perMillion,
  recommendationText,
  tokensText,
  unknownMacros,
  unusedMacros,
  usd,
} from './ai'

const macros: AIMacro[] = [
  { name: 'now', description: 'The time.' },
  { name: 'trips', description: 'The trips.' },
  { name: 'energy_prices', description: 'The prices.' },
]

describe('placeholders', () => {
  it('finds unknown and unused ones as the server does', () => {
    const prompt = 'It is {{ now }}. {{Trips}} {{prices}} {{prices}} {{weather}}'
    expect(unknownMacros(prompt, macros)).toEqual(['prices', 'weather'])
    expect(unusedMacros(prompt, macros).map((m) => m.name)).toEqual(['energy_prices'])
  })

  it('puts one in at the cursor, replacing a selection', () => {
    expect(insertMacro('Plan: ', 6, 6, 'trips')).toEqual({ text: 'Plan: {{trips}}', cursor: 15 })
    expect(insertMacro('Plan: X.', 6, 7, 'now')).toEqual({ text: 'Plan: {{now}}.', cursor: 13 })
  })
})

describe('recommendationText', () => {
  const day = { date: '2026-11-10', confidence: 3, summary: '', rationale: [] }
  it('says what the AI decided in a few words', () => {
    expect(
      recommendationText({
        ...day,
        recommendation: 'charge_extra',
        targetSoc: 75,
        from: '2026-11-10T00:00:00Z',
        until: '2026-11-10T04:00:00Z',
      }),
    ).toBe('Charge extra to 75%, 01:00–05:00')
    expect(
      recommendationText({
        ...day,
        recommendation: 'charge_extra',
        targetSoc: 60,
        from: '2026-11-09T22:00:00Z',
        until: '2026-11-10T04:00:00Z',
      }),
    ).toBe('Charge extra to 60%, Mon 9 Nov 23:00–05:00')
    expect(
      recommendationText({ ...day, recommendation: 'defer', deferUntil: '2026-11-11', postpone: true }),
    ).toBe('Defer until Wed 11 Nov, pausing cheap-grid charging')
    expect(recommendationText({ ...day, recommendation: 'none' })).toBe('Nothing extra')
  })

  it('writes dollar amounts in cents, small ones with two significant digits', () => {
    expect([0.2336, 0.0584, 0.000631, 0.00295, 40, 8, 0.036, 26.28, 0.2037, 2.5].map(usd)).toEqual([
      '$0.23',
      '$0.058',
      '$0.00063',
      '$0.0029',
      '$40',
      '$8',
      '$0.036',
      '$26.28',
      '$0.20',
      '$2.50',
    ])
    expect([0.1, 2.5, 0.036, 40, 0.47, 1.4].map(perMillion)).toEqual([
      '$0.10',
      '$2.50',
      '$0.036',
      '$40',
      '$0.47',
      '$1.40',
    ])
    const m = { id: '~z-ai/glm-latest', inputPerM: 0.07, outputPerM: 1.4, runUsd: 0.00679 }
    expect(modelLabel(m, '~anthropic/claude-haiku-latest')).toBe('~z-ai/glm-latest · ≈ $0.0068/run')
    expect(modelLabel(m, '~z-ai/glm-latest')).toBe('~z-ai/glm-latest · ≈ $0.0068/run (default)')
    expect(monthlyText(m, 3)).toBe('≈ $0.61 a month at 3 runs a day')
    expect(monthlyText(m, 1)).toBe('≈ $0.20 a month at 1 run a day')
    expect(monthlyText(m, 0)).toBe('No automatic runs: only runs by hand cost')
  })

  it('rounds token counts', () => {
    expect(tokensText(2261)).toBe('about 2,300 tokens')
  })
})
