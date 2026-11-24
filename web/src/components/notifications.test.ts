import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { NotificationCopy, NotificationSettings, PhoneNotification } from '../api/types'

const api = vi.hoisted(() => ({
  notificationSettings: vi.fn(),
  saveNotificationSettings: vi.fn(),
  phones: vi.fn(),
  notificationCopy: vi.fn(),
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
  extra: true,
  arrival: true,
  arrivalInQuiet: true,
  prices: true,
  evening: false,
  eveningAt: '19:30',
  planSent: true,
}

const copy: NotificationCopy[] = [
  {
    setting: 'arrival',
    kind: 'arrived',
    titlePrefix: '[EVCC Planner] 🏠',
    title: 'Back home: plug in',
    aiSuffix: '(🤖 AI mode)',
    messages: ['{trip} needs {target}% by {departure}.[ {advice}]', '{advice}'],
    buttons: ['snooze', 'view'],
  },
]

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
    api.notificationCopy.mockResolvedValue(copy)
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

  it('turns back home reminders off', async () => {
    const w = mount(NotificationSettingsForm)
    await flushPromises()
    expect(w.text()).toContain('Back home reminders')
    await w.get('[role="switch"][aria-label="Back home reminders"]').trigger('click')
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(api.saveNotificationSettings).toHaveBeenCalledWith({ ...defaults, arrival: false })
  })

  it('keeps back home reminders out of quiet hours with their own check', async () => {
    const w = mount(NotificationSettingsForm)
    await flushPromises()
    const quiet = w.get<HTMLInputElement>('[data-testid="notify-arrival-quiet"]')
    expect(quiet.element.checked).toBe(true)
    expect(w.find('[role="switch"][aria-label*="quiet hours"][aria-label^="Back home"]').exists()).toBe(false)
    await quiet.setValue(false)
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(api.saveNotificationSettings).toHaveBeenCalledWith({ ...defaults, arrivalInQuiet: false })
    // Without back home reminders the check has nothing to do.
    await w.get('[role="switch"][aria-label="Back home reminders"]').trigger('click')
    expect(quiet.attributes('disabled')).toBeDefined()
  })

  it('turns the evening reminder on and sets its time', async () => {
    const w = mount(NotificationSettingsForm)
    await flushPromises()
    const at = w.get<HTMLInputElement>('[data-testid="notify-evening-at"]')
    expect(at.element.value).toBe('19:30')
    expect(at.attributes('disabled')).toBeDefined()
    await w.get('[role="switch"][aria-label="Evening reminder"]').trigger('click')
    expect(at.attributes('disabled')).toBeUndefined()
    await at.setValue('20:00')
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(api.saveNotificationSettings).toHaveBeenCalledWith({
      ...defaults,
      evening: true,
      eveningAt: '20:00',
    })
  })

  it('shows what a notification says on hover or tap, with what is filled in marked', async () => {
    const w = mount(NotificationSettingsForm, { attachTo: document.body })
    await flushPromises()
    const button = w.get('[aria-label="What back home reminders say"]')
    expect(button.attributes('aria-expanded')).toBe('false')
    // Only settings with copy get the icon.
    expect(w.find('[aria-label="What early reminders say"]').exists()).toBe(false)
    await w.get('.info-tip').trigger('mouseenter')
    expect(button.attributes('aria-expanded')).toBe('true')
    const panel = w.get(`#${button.attributes('aria-controls')}`)
    expect(panel.text()).toContain('[EVCC Planner] 🏠 Back home: plug in [ (🤖 AI mode)]')
    expect(panel.text()).toContain('Buttons: Remind in 30 min · View plan')
    expect(panel.text()).toContain('One of these, as it applies.')
    expect(panel.text()).toContain('In AI mode, the title ends with “(🤖 AI mode)”.')
    expect(panel.findAll('.copy-slot').map((s) => s.text())).toContain('{trip}')
    // A tap keeps it open; Escape closes it.
    await button.trigger('click')
    await w.get('.info-tip').trigger('mouseleave')
    await new Promise((r) => setTimeout(r, 200))
    expect(button.attributes('aria-expanded')).toBe('true')
    await button.trigger('keydown', { key: 'Escape' })
    expect(button.attributes('aria-expanded')).toBe('false')
    w.unmount()
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

  it('shows a send that is tried again, with its note and no demo buttons', async () => {
    api.notifications.mockResolvedValue([
      {
        ...urgent,
        state: 'retrying',
        sentAt: undefined,
        tries: 1,
        retryAt: '2026-11-10T05:12:00Z',
        note: 'Not sent (phone unreachable); trying again at 06:12',
      },
    ])
    const w = mount(NotificationSettingsForm)
    await flushPromises()
    const item = w.get('[data-testid="notification-urgent"]')
    expect(item.get('.pill').text()).toBe('Trying again')
    expect(item.get('.pill').classes()).toContain('tone-warn')
    expect(item.text()).toContain('Not sent (phone unreachable); trying again at 06:12')
    expect(item.findAll('button')).toHaveLength(0)
  })

  it('explains when Home Assistant cannot list phones', async () => {
    api.phones.mockRejectedValue(new Error('Set HA_TOKEN to send notifications through Home Assistant'))
    const w = mount(NotificationSettingsForm)
    await flushPromises()
    expect(w.text()).toContain('Set HA_TOKEN to send notifications')
  })
})
