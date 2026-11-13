import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import { ref } from 'vue'
import SaveBar from '../components/SaveBar.vue'
import { useUnsaved } from './unsaved'

describe('useUnsaved', () => {
  it('is dirty after a change, clean after saving, and discards back to the saved values', () => {
    const form = ref<{ soc: number } | null>(null)
    const u = useUnsaved(form)
    expect(u.dirty.value).toBe(false)
    form.value = { soc: 42 }
    u.mark()
    expect(u.dirty.value).toBe(false)
    form.value.soc = 60
    expect(u.dirty.value).toBe(true)
    u.discard()
    expect(form.value).toEqual({ soc: 42 })
    expect(u.dirty.value).toBe(false)
    form.value.soc = 70
    u.mark()
    expect(u.dirty.value).toBe(false)
  })
})

describe('SaveBar', () => {
  it('says when changes are unsaved and offers Discard', async () => {
    const clean = mount(SaveBar, { props: { dirty: false, label: 'Save settings' } })
    expect(clean.text()).toBe('Save settings')
    expect(clean.classes()).not.toContain('dirty')

    const w = mount(SaveBar, {
      props: { dirty: true, label: 'Save settings', testid: 'save' },
      slots: { default: '<button type="button">Reset to defaults</button>' },
    })
    // Other buttons wait until the form is saved.
    expect(w.text()).not.toContain('Reset to defaults')
    expect(w.text()).toContain('Unsaved changes')
    expect(w.get('[data-testid=save]').attributes('type')).toBe('submit')
    await w
      .findAll('button')
      .find((b) => b.text() === 'Discard')!
      .trigger('click')
    expect(w.emitted('discard')).toHaveLength(1)
  })
})
