import { describe, expect, it } from 'vitest'
import type { HeatingRun, HeatingSummary } from '../api/types'
import { heatingLine, outcomeText, outcomeTone, outsideText, temp } from './heating'

const run = (over: Partial<HeatingRun>): HeatingRun => ({
  id: 1,
  key: 'heat:recurring:1:2026-11-10',
  tripKey: 'recurring:1:2026-11-10',
  title: 'School drop-off',
  departAt: '2026-11-10T06:48:00Z',
  decidedAt: '2026-11-10T06:33:00Z',
  outcome: 'heated',
  outsideC: 2,
  outsideSource: 'sensor',
  belowC: 3,
  ...over,
})

const summary = (over: Partial<HeatingSummary>): HeatingSummary => ({
  enabled: true,
  belowC: 3,
  leadMin: 15,
  targetC: 20,
  askPlugged: false,
  askUnplugged: true,
  ...over,
})

const next = {
  tripKeys: ['recurring:1:2026-11-10'],
  firstTitle: 'School drop-off',
  departAt: '2026-11-10T06:48:00Z',
}

describe('car heating texts', () => {
  it('formats temperatures to one decimal when not whole', () => {
    expect(temp(2)).toBe('2 °C')
    expect(temp(2.64)).toBe('2.6 °C')
    expect(temp(-0.04)).toBe('0 °C')
    expect(outsideText({ outsideC: 5, outsideSource: 'forecast' })).toBe('a low of 5 °C forecast')
    expect(outsideText({})).toBe('outside temperature unknown')
  })

  it('says what each decision was', () => {
    const at = { heatedAt: '2026-11-10T06:33:00Z' }
    expect(outcomeText(run(at))).toBe('On since 07:33 (2 °C outside)')
    expect(outcomeText(run({ ...at, outcome: 'confirmed', heatedAt: '2026-11-10T06:36:00Z' }))).toBe(
      'On since 07:36, after Heat now (2 °C outside)',
    )
    expect(outcomeText(run({ outcome: 'asked' }))).toBe('Asked on the phone at 07:33 (2 °C outside)')
    expect(outcomeText(run({ outcome: 'no_answer' }))).toBe('Not on: no answer within 30 minutes')
    expect(outcomeText(run({ outcome: 'warm', outsideC: 8 }))).toBe(
      'Not needed: 8 °C outside (heats below 3 °C)',
    )
    expect(outcomeText(run({ outcome: 'skipped', reason: "the car isn't home" }))).toBe(
      "Not on: the car isn't home",
    )
    expect(outcomeText(run({ outcome: 'failed', reason: 'unreachable', retry: true }))).toBe(
      'Failed: unreachable; trying again',
    )
    expect(outcomeTone(run({}))).toBe('ok')
    expect(outcomeTone(run({ outcome: 'failed' }))).toBe('bad')
    expect(outcomeTone(run({ outcome: 'no_answer' }))).toBe('warn')
    expect(outcomeTone(run({ outcome: 'warm' }))).toBe('muted')
  })

  it("shows the next departure's decision, else when it would heat, with the car's inside temperature", () => {
    expect(heatingLine(undefined, next, 10)).toBeNull()
    expect(heatingLine(summary({}), next, 10)).toEqual({
      text: 'Below 3 °C: heats to 20 °C at 07:33, before School drop-off · 10 °C in the car',
      tone: 'muted',
    })
    // Without the climate's reading, no inside temperature.
    expect(heatingLine(summary({}), next)?.text).toBe(
      'Below 3 °C: heats to 20 °C at 07:33, before School drop-off',
    )
    const decided = summary({ last: run({ heatedAt: '2026-11-10T06:33:00Z' }) })
    expect(heatingLine(decided, next, 14.2)).toEqual({
      text: 'On since 07:33 (2 °C outside) · 14.2 °C in the car',
      tone: 'ok',
    })
    // An earlier departure's decision gives way to the next departure.
    const later = {
      ...next,
      tripKeys: ['recurring:2:2026-11-10'],
      firstTitle: 'School pickup',
      departAt: '2026-11-10T13:33:00Z',
    }
    expect(heatingLine(decided, later)?.text).toBe(
      'Below 3 °C: heats to 20 °C at 14:18, before School pickup',
    )
    // Turned off: only a recent decision shows.
    expect(heatingLine(summary({ enabled: false }), next)).toBeNull()
    expect(heatingLine({ ...decided, enabled: false }, later)?.text).toBe('On since 07:33 (2 °C outside)')
  })
})
