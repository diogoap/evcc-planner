import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { HASensor, HeatingSettings, HeatingView } from '../api/types'

const api = vi.hoisted(() => ({
  heatingSettings: vi.fn(),
  saveHeatingSettings: vi.fn(),
  heating: vi.fn(),
  homeAssistantSensors: vi.fn(),
  notificationCopy: vi.fn(),
  demoClimate: vi.fn(),
}))
vi.mock('../api/client', () => ({ api }))
const store = vi.hoisted(() => ({ notify: vi.fn(), refresh: vi.fn(), state: { meta: { demo: false } } }))
vi.mock('../store', () => store)

const { default: HeatingSettingsForm } = await import('./HeatingSettingsForm.vue')

const settings: HeatingSettings = {
  enabled: false,
  belowC: 3,
  leadMin: 15,
  climateEntity: 'climate.car_climate',
  outsideEntity: 'sensor.outside_temperature',
  targetC: 20,
  askPlugged: false,
  askUnplugged: true,
}

const view = (over: Partial<HeatingView> = {}): HeatingView => ({
  settings,
  outside: { c: 1.5, source: 'sensor', at: '2026-11-10T06:30:00Z' },
  climate: {
    state: 'off',
    targetC: 18,
    minC: 16,
    maxC: 26,
    modes: ['heat_cool', 'off'],
    mode: 'heat_cool',
    sendC: 20,
  },
  notes: [],
  recent: [],
  ...over,
})

const sensors: HASensor[] = [
  { entityId: 'climate.car_climate', name: 'Car climate', state: 'off', unit: '', kind: 'climate' },
  { entityId: 'sensor.outside_temperature', name: 'Outside', state: '1.5', unit: '°C', kind: 'temperature' },
  { entityId: 'sensor.car_battery', name: 'Battery', state: '70', unit: '%', kind: 'battery' },
]

describe('HeatingSettingsForm', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    store.state.meta.demo = false
    api.heatingSettings.mockResolvedValue({ ...settings })
    api.heating.mockResolvedValue(view())
    api.homeAssistantSensors.mockResolvedValue(sensors)
    api.notificationCopy.mockResolvedValue([])
    api.saveHeatingSettings.mockImplementation((s: HeatingSettings) => Promise.resolve(s))
  })

  it('loads the settings, the entities to choose from, and what it reads now', async () => {
    const w = mount(HeatingSettingsForm)
    await flushPromises()
    expect((w.get('[data-testid=heating-climateEntity]').element as HTMLInputElement).value).toBe(
      'climate.car_climate',
    )
    expect(w.findAll('#ha-sensors-climate option').map((o) => o.attributes('value'))).toEqual([
      'climate.car_climate',
    ])
    expect(w.findAll('#ha-sensors-temperature option').map((o) => o.attributes('value'))).toEqual([
      'sensor.outside_temperature',
    ])
    expect(w.get('[data-testid=heating-outside]').text()).toBe('1.5 °C, read 07:30')
    expect(w.get('[data-testid=heating-climate]').text()).toBe('Off · set to 18 °C · 16 °C–26 °C')
    expect(w.text()).toContain('heat_cool at 20 °C')
    expect(w.find('[data-testid=heating-demo]').exists()).toBe(false)
  })

  it('saves, reports unsaved changes, and shows recent decisions', async () => {
    api.heating.mockResolvedValue(
      view({
        recent: [
          {
            id: 1,
            key: 'heat:manual:1',
            tripKey: 'manual:1',
            title: 'Office',
            departAt: '2026-11-10T06:48:00Z',
            decidedAt: '2026-11-10T06:33:00Z',
            outcome: 'skipped',
            reason: "the car isn't home",
            belowC: 3,
          },
        ],
        notes: ['Without a car location sensor (Settings → Notifications), the car counts as home.'],
      }),
    )
    const w = mount(HeatingSettingsForm)
    await flushPromises()
    expect(w.get('[data-testid=heating-recent]').text()).toContain("Not on: the car isn't home")
    expect(w.text()).toContain('the car counts as home')
    await w.get('[data-testid=heating-enabled]').trigger('click')
    await w.get('[data-testid=heating-belowC]').setValue('1.5')
    expect(w.emitted('dirty')?.at(-1)).toEqual([true])
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(api.saveHeatingSettings).toHaveBeenCalledWith({ ...settings, enabled: true, belowC: 1.5 })
    expect(store.notify).toHaveBeenCalledWith('Car heating settings saved')
    expect(w.emitted('dirty')?.at(-1)).toEqual([false])
  })

  it('in the demo, sets the outside temperature', async () => {
    store.state.meta.demo = true
    api.demoClimate.mockResolvedValue(view({ outside: { c: -2, source: 'sensor' } }))
    const w = mount(HeatingSettingsForm)
    await flushPromises()
    await w.get('[data-testid=heating-demo-outside]').setValue('-2')
    await w.get('[data-testid=heating-demo-set]').trigger('click')
    await flushPromises()
    expect(api.demoClimate).toHaveBeenCalledWith({ outsideC: -2 })
    expect(w.get('[data-testid=heating-outside]').text()).toBe('-2 °C')
  })
})
