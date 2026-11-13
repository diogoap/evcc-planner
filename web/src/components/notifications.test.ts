import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { NotificationSettings, PhoneNotification } from '../api/types'

const api = vi.hoisted(() => ({
  notificationSettings: vi.fn(),
  saveNotificationSettings: vi.fn(),
  phones: vi.fn(),
  notifications: vi.fn(),
  testNotification: vi.fn(),
  pressNotification: vi.fn(),
}))
vi.mock('../api/client', () => ({ api }))
const store = vi.hoisted(() => ({ notify: vi.fn(), refresh: vi.fn(), state: { meta: { demo: true } } }))
vi.mock('../store', () => store)

const { default: NotificationSettingsForm } = await import('./NotificationSettingsForm.vue')

const defaults: NotificationSettings = {
  enabled: false,
  phones: [],
  linkUrl: '',
  locationEntity: '',
  early: true,
  earlyLeadMin: 30,
  urgentLeadMin: 30,
  quietFrom: '23:00',
  quietTo: '07:00',
  urgentInQuiet: true,
  snoozeMin: 30,
  problems: true,
  questions: true,
  ready: false,
  solar: true,
}

const urgent: PhoneNotification = {
  id: 7,
  key: 'urgent:recurring:1:2026-11-10',
  kind: 'urgent',
  subject: 'recurring:1:2026-11-10',
  title: 'Plug in by 06:36',
  message: 'School drop-off needs 42% by 07:48.',
  verbs: ['snooze', 'skip', 'view'],
  urgent: true,
  state: 'sent',
  createdAt: '2026-11-10T05:10:00Z',
  sentAt: '2026-11-10T05:10:00Z',
}

describe('NotificationSettingsForm', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    api.notificationSettings.mockResolvedValue({ ...defaults })
    api.phones.mockResolvedValue(['mobile_app_demo_phone'])
    api.notifications.mockResolvedValue([urgent])
    api.saveNotificationSettings.mockImplementation((s: NotificationSettings) => Promise.resolve(s))
    api.pressNotification.mockResolvedValue(undefined)
  })

  it('chooses a phone, turns notifications on, and saves', async () => {
    const w = mount(NotificationSettingsForm)
    await flushPromises()
    expect(w.text()).toContain('notify.mobile_app_demo_phone')
    expect(w.get('[data-testid="notify-test"]').attributes('disabled')).toBeDefined()
    await w.get('[data-testid="phone-mobile_app_demo_phone"]').setValue(true)
    await w.get('[data-testid="notify-enabled"]').trigger('click')
    await w.get('[data-testid="notify-link"]').setValue('http://planner.test:7201/')
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(api.saveNotificationSettings).toHaveBeenCalledWith({
      ...defaults,
      enabled: true,
      phones: ['mobile_app_demo_phone'],
      linkUrl: 'http://planner.test:7201/',
    })
    expect(store.notify).toHaveBeenCalledWith('Notification settings saved')
  })

  it('lists recent notifications and presses the demo phone buttons', async () => {
    const w = mount(NotificationSettingsForm)
    await flushPromises()
    const item = w.get('[data-testid="notification-urgent"]')
    expect(item.text()).toContain('Plug in by 06:36')
    expect(item.text()).toContain('On the phone')
    const buttons = item.findAll('button').map((b) => b.text())
    expect(buttons).toEqual(['Remind in 30 min', 'Skip trip'])
    await item.findAll('button')[0]!.trigger('click')
    await flushPromises()
    expect(api.pressNotification).toHaveBeenCalledWith(7, 'snooze')
  })

  it('explains when Home Assistant cannot list phones', async () => {
    api.phones.mockRejectedValue(new Error('Set HA_TOKEN to send notifications through Home Assistant'))
    const w = mount(NotificationSettingsForm)
    await flushPromises()
    expect(w.text()).toContain('Set HA_TOKEN to send notifications')
  })
})
