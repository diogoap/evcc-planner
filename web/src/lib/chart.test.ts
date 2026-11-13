import { afterEach, describe, expect, it, vi } from 'vitest'
import { alpha, dayGrid, onThemeChange } from './chart'

describe('alpha', () => {
  it('adds an opacity to a #rrggbb token colour', () => {
    expect(alpha('#0a9c2f', 0.33)).toBe('#0a9c2f54')
    expect(alpha('#0A9C2F', 1)).toBe('#0A9C2Fff')
  })

  it('leaves other colour formats unchanged', () => {
    expect(alpha('rgba(1, 2, 3, 0.5)', 0.2)).toBe('rgba(1, 2, 3, 0.5)')
  })
})

describe('onThemeChange', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('renders again when the system switches between light and dark, until stopped', () => {
    const media = new EventTarget()
    const matchMedia = vi.fn(() => media)
    vi.stubGlobal('matchMedia', matchMedia)
    const render = vi.fn()

    const stop = onThemeChange(render)
    expect(matchMedia).toHaveBeenCalledWith('(prefers-color-scheme: dark)')
    media.dispatchEvent(new Event('change'))
    expect(render).toHaveBeenCalledTimes(1)

    stop()
    media.dispatchEvent(new Event('change'))
    expect(render).toHaveBeenCalledTimes(1)
  })
})

describe('dayGrid', () => {
  it('draws a clear line at local midnight and faint ones at other ticks', () => {
    const g = dayGrid('faint', 'clear')
    // 23:00 UTC on 9 November is midnight in Amsterdam.
    const midnight = { tick: { value: Date.parse('2026-11-09T23:00:00Z') } }
    const morning = { tick: { value: Date.parse('2026-11-10T05:00:00Z') } }
    expect([g.color(midnight), g.lineWidth(midnight)]).toEqual(['clear', 1.5])
    expect([g.color(morning), g.lineWidth(morning)]).toEqual(['faint', 1])
    expect(g.color({})).toBe('faint')
  })
})
