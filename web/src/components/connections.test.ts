import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { ConnectionSettings, ConnectionSettingsView } from '../api/types'

const api = vi.hoisted(() => ({
  connectionSettings: vi.fn(),
  saveConnectionSettings: vi.fn(),
  homeAssistantSensors: vi.fn(),
}))
vi.mock('../api/client', () => ({ api }))
vi.mock('../store', () => ({ notify: vi.fn(), refresh: vi.fn() }))

const { default: ConnectionSettingsForm } = await import('./ConnectionSettingsForm.vue')

const defaults: ConnectionSettings = {
  evccUrl: 'http://127.0.0.1:7070',
  evccLoadpoint: 1,
  evccVehicle: '',
  evccPollIntervalSec: 60,
  evccTimeoutSec: 10,
  haUrl: 'http://127.0.0.1:8123',
  haSocEntity: '',
  haRangeEntity: '',
  haUpdatedEntity: '',
  haPollIntervalMin: 10,
  haTimeoutSec: 10,
  calendarSyncIntervalMin: 60,
  routesTrafficAware: true,
  routesCacheTtlHours: 168,
  routesMonthlyFreeCalls: 5000,
  tibberUrl: 'https://tibber.com/nl/stroomprijsprognose',
  openRouterModel: 'anthropic/claude-haiku-5.5',
  openRouterTimeoutSec: 60,
  knmiDataset: 'outlook_weather_forecast',
  knmiVersion: '1.0',
  forecastDailyAt: '18:00',
  forecastRetryIntervalMin: 60,
  forecastVatPct: 21,
  retentionDays: 90,
}

const view = (settings: Partial<ConnectionSettings> = {}): ConnectionSettingsView => ({
  settings: { ...defaults, ...settings },
  defaults,
  env: [
    { name: 'Home Assistant access token', env: 'HA_TOKEN', set: true },
    { name: 'Google Routes API key', env: 'GOOGLE_MAPS_API_KEY', set: false },
  ],
})

describe('ConnectionSettingsForm', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    api.connectionSettings.mockResolvedValue(view({ evccUrl: 'http://192.0.2.10:7070' }))
    api.homeAssistantSensors.mockResolvedValue([
      { entityId: 'sensor.leaf_battery', name: 'Leaf battery', state: '72', unit: '%', kind: 'battery' },
      { entityId: 'sensor.leaf_range', name: 'Leaf range', state: '190', unit: 'km', kind: 'range' },
      {
        entityId: 'sensor.leaf_last_update',
        name: 'Leaf last update',
        state: '2026-11-11T07:30:00+00:00',
        unit: '',
        kind: 'timestamp',
      },
    ])
  })

  it('shows each group with defaults, env values, and sensor suggestions', async () => {
    const w = mount(ConnectionSettingsForm)
    await flushPromises()
    expect((w.get('[data-testid=connection-evccUrl]').element as HTMLInputElement).value).toBe(
      'http://192.0.2.10:7070',
    )
    expect(w.get('#hint-evccUrl').text()).toContain('Default: http://127.0.0.1:7070.')
    expect(w.get('#hint-haPollIntervalMin').text()).toContain('Default: 10 min.')
    expect(w.get('#hint-haSocEntity').text()).toContain('Default: none.')
    const ha = w.get('[data-testid="connection-group-Home Assistant (always-on SoC)"]')
    expect(ha.get('[data-testid=connection-env]').text()).toBe('HA_TOKEN: set')
    expect(ha.find('[data-testid=connection-env] .icon').exists()).toBe(true)
    expect(w.get('#hint-haUrl').text()).toContain('The access token is sent here')
    expect(w.get('[data-testid="connection-group-Google Routes"]').text()).toContain(
      'GOOGLE_MAPS_API_KEY: not set',
    )
    expect(w.get('[data-testid=connection-haSocEntity]').attributes('list')).toBe('ha-sensors-battery')
    expect(w.findAll('#ha-sensors-battery option').map((o) => o.attributes('value'))).toEqual([
      'sensor.leaf_battery',
    ])
    expect(w.findAll('#ha-sensors-range option').map((o) => o.attributes('value'))).toEqual([
      'sensor.leaf_range',
    ])
    expect(w.get('[data-testid=connection-haUpdatedEntity]').attributes('list')).toBe('ha-sensors-timestamp')
    expect(w.findAll('#ha-sensors-timestamp option').map((o) => o.attributes('value'))).toEqual([
      'sensor.leaf_last_update',
    ])
  })

  it('saves the edited fields and reports the new connection status', async () => {
    const saved = { ...view({ haSocEntity: 'sensor.leaf_battery' }), connections: [] }
    api.saveConnectionSettings.mockResolvedValue({
      ...saved,
      connections: [{ name: 'Home Assistant', status: 'ok', detail: 'SoC 72%' }],
    })
    const w = mount(ConnectionSettingsForm)
    await flushPromises()
    await w.get('[data-testid=connection-haSocEntity]').setValue('sensor.leaf_battery')
    await w.get('[data-testid=connection-routesTrafficAware]').trigger('click')
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(api.saveConnectionSettings).toHaveBeenCalledWith({
      ...defaults,
      evccUrl: 'http://192.0.2.10:7070',
      haSocEntity: 'sensor.leaf_battery',
      routesTrafficAware: false,
    })
    expect(w.emitted('saved')).toEqual([[[{ name: 'Home Assistant', status: 'ok', detail: 'SoC 72%' }]]])
  })

  it('resets the form to the defaults without saving', async () => {
    const w = mount(ConnectionSettingsForm)
    await flushPromises()
    await w.get('[data-testid=reset-connections]').trigger('click')
    expect((w.get('[data-testid=connection-evccUrl]').element as HTMLInputElement).value).toBe(
      'http://127.0.0.1:7070',
    )
    expect(api.saveConnectionSettings).not.toHaveBeenCalled()
  })

  it('asks before lowering history retention', async () => {
    api.saveConnectionSettings.mockResolvedValue(view({ retentionDays: 30 }))
    const w = mount(ConnectionSettingsForm)
    await flushPromises()
    await w.get('[data-testid=connection-retentionDays]').setValue(30)
    await w.get('form').trigger('submit')
    expect(api.saveConnectionSettings).not.toHaveBeenCalled()
    expect(w.get('[data-testid=confirm-retention]').text()).toContain('deletes older entries')
    expect(w.get('[data-testid=save-connections]').text()).toBe('Delete older history and save')
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(api.saveConnectionSettings).toHaveBeenCalledTimes(1)
    expect(w.find('[data-testid=confirm-retention]').exists()).toBe(false)
  })
})
