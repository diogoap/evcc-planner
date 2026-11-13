import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { pullDistance, pullMax, pullThreshold } from '../lib/pull'
import PullToRefresh from './PullToRefresh.vue'

// A one-finger touch event at the given height on the screen.
function touch(type: string, y?: number) {
  const e = new Event(type)
  Object.defineProperty(e, 'touches', { value: y === undefined ? [] : [{ clientY: y }] })
  window.dispatchEvent(e)
}

describe('pullDistance', () => {
  it('follows the finger at half speed, up to a limit', () => {
    expect(pullDistance(-20)).toBe(0)
    expect(pullDistance(100)).toBe(50)
    expect(pullDistance(1000)).toBe(pullMax)
  })
})

describe('PullToRefresh', () => {
  afterEach(() => {
    document.documentElement.classList.remove('modal-open')
    window.scrollY = 0
  })

  it('refreshes after a pull past the threshold, with a spinner meanwhile', async () => {
    let done = () => {}
    const refresh = vi.fn(() => new Promise<void>((resolve) => (done = resolve)))
    const w = mount(PullToRefresh, { props: { refresh, enabled: true }, attachTo: document.body })
    touch('touchstart', 100)
    touch('touchmove', 100 + pullThreshold * 2 + 10)
    await w.vm.$nextTick()
    expect(w.get('[data-testid=pull]').classes()).toContain('ready')
    touch('touchend')
    await w.vm.$nextTick()
    expect(refresh).toHaveBeenCalledTimes(1)
    expect(w.find('.spinning').exists()).toBe(true)
    done()
    await flushPromises()
    expect(w.find('[data-testid=pull]').exists()).toBe(false)
    w.unmount()
  })

  it('does nothing for a short pull, a scrolled page, an open dialog, or a browser tab', async () => {
    const refresh = vi.fn(async () => {})
    const w = mount(PullToRefresh, { props: { refresh, enabled: true }, attachTo: document.body })
    touch('touchstart', 100)
    touch('touchmove', 140)
    touch('touchend')

    window.scrollY = 300
    touch('touchstart', 100)
    touch('touchmove', 400)
    touch('touchend')
    window.scrollY = 0

    document.documentElement.classList.add('modal-open')
    touch('touchstart', 100)
    touch('touchmove', 400)
    touch('touchend')
    w.unmount()

    const tab = mount(PullToRefresh, { props: { refresh, enabled: false }, attachTo: document.body })
    touch('touchstart', 100)
    touch('touchmove', 400)
    touch('touchend')
    tab.unmount()
    await flushPromises()
    expect(refresh).not.toHaveBeenCalled()
  })
})
