import { afterEach, describe, expect, it, vi } from 'vitest'
import { alpha, onThemeChange } from './chart'

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
