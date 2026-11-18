import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { opensKeyboard, watchKeyboard } from './keyboard'

describe('the on-screen keyboard', () => {
  let stop: () => void
  beforeEach(() => {
    vi.useFakeTimers()
    document.body.innerHTML = `
      <input id="text" />
      <input id="number" type="number" />
      <input id="box" type="checkbox" />
      <textarea id="area"></textarea>
      <textarea id="ro" readonly></textarea>
      <button id="btn">OK</button>`
    stop = watchKeyboard()
  })
  afterEach(() => {
    stop()
    vi.useRealTimers()
  })

  const el = (id: string) => document.getElementById(id) as HTMLElement
  const typing = () => document.documentElement.classList.contains('typing')

  it('knows which fields bring it up', () => {
    expect(['text', 'number', 'area'].map((id) => opensKeyboard(el(id)))).toEqual([true, true, true])
    expect(['box', 'ro', 'btn'].map((id) => opensKeyboard(el(id)))).toEqual([false, false, false])
    expect(opensKeyboard(null)).toBe(false)
  })

  it('marks the page while a text field has focus, and lays it out again after', () => {
    const scroll = vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
    el('text').focus()
    expect(typing()).toBe(true)
    // Moving to another field keeps it.
    el('number').focus()
    vi.advanceTimersByTime(100)
    expect(typing()).toBe(true)
    expect(scroll).not.toHaveBeenCalled()
    el('btn').focus()
    vi.advanceTimersByTime(100)
    expect(typing()).toBe(false)
    expect(scroll).toHaveBeenCalledTimes(1)
    el('area').focus()
    el('area').blur()
    vi.advanceTimersByTime(100)
    expect(typing()).toBe(false)
  })
})
