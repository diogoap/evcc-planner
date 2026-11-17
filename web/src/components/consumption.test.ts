import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import type { ConsumptionView, HASensor, Settings } from '../api/types'
import ConsumptionSetting from './ConsumptionSetting.vue'

const base = {
  consumptionKWhPer100Km: 18,
  consumptionUnit: 'kwh_per_100km',
  consumptionEntity: '',
  consumptionEntityUnit: 'km_per_kwh',
} as Settings

const sensors: HASensor[] = [
  {
    entityId: 'sensor.car_efficiency',
    name: 'Car efficiency',
    state: '6.4',
    unit: 'km/kWh',
    kind: 'efficiency',
  },
  {
    entityId: 'sensor.car_consumption',
    name: 'Car consumption',
    state: '16',
    unit: 'kWh/100km',
    kind: 'efficiency',
  },
  { entityId: 'sensor.car_parked', name: 'Parked', state: '0', unit: '', kind: 'efficiency' },
  { entityId: 'sensor.car_trip', name: 'Trip', state: '0.8', unit: '', kind: 'efficiency' },
]

function setup(settings: Partial<Settings> = {}, used: ConsumptionView | null = null) {
  const model = { ...base, ...settings }
  const w = mount(ConsumptionSetting, {
    props: { modelValue: model, sensors, used, now: '2026-11-09T18:00:00Z' },
  })
  return { w, model }
}

describe('ConsumptionSetting', () => {
  it('takes the value in either unit, keeps it in kWh/100 km, and shows both', async () => {
    const { w, model } = setup()
    const input = w.get('[data-testid=setting-consumptionKWhPer100Km]')
    expect((input.element as HTMLInputElement).value).toBe('18')
    expect(w.get('[data-testid=consumption-both]').text()).toBe('18.0 kWh/100 km · 5.6 km/kWh')

    // The other unit shows the same consumption.
    await w.get('[data-testid=setting-consumptionUnit]').setValue('km_per_kwh')
    expect((input.element as HTMLInputElement).value).toBe('5.56')
    expect(model.consumptionKWhPer100Km).toBe(18)
    await input.setValue('5')
    expect(model.consumptionKWhPer100Km).toBe(20)
    expect(w.get('[data-testid=consumption-both]').text()).toBe('20.0 kWh/100 km · 5.0 km/kWh')
    expect(input.attributes('max')).toBe('20')
  })

  it('previews a chosen sensor in both units, taking its unit from Home Assistant', async () => {
    const { w, model } = setup()
    expect(w.find('[data-testid=consumption-sensor]').exists()).toBe(false)
    await w.get('[data-testid=setting-consumptionEntity]').setValue('sensor.car_consumption')
    expect(model.consumptionEntityUnit).toBe('kwh_per_100km')
    expect(w.get('[data-testid=consumption-sensor]').text()).toBe(
      'Reads 16 kWh/100km now: 16.0 kWh/100 km · 6.3 km/kWh. Save to use it.',
    )
    await w.get('[data-testid=setting-consumptionEntity]').setValue('sensor.car_efficiency')
    expect(model.consumptionEntityUnit).toBe('km_per_kwh')
    expect(w.get('[data-testid=consumption-sensor]').text()).toContain('15.6 kWh/100 km · 6.4 km/kWh')
    // Without a unit from Home Assistant the choice stays; 0.8 km/kWh is 125 kWh/100 km.
    await w.get('[data-testid=setting-consumptionEntity]').setValue('sensor.car_trip')
    expect(model.consumptionEntityUnit).toBe('km_per_kwh')
    expect(w.get('[data-testid=consumption-sensor]').text()).toBe(
      'Reads 0.8 now: 125.0 kWh/100 km · 0.8 km/kWh, which is not plausible; check the unit it reports.',
    )
    await w.get('[data-testid=setting-consumptionEntity]').setValue('sensor.car_parked')
    expect(w.get('[data-testid=consumption-sensor]').text()).toBe(
      'Reads 0 now. Zero, unknown, or no reading is ignored.',
    )
  })

  it('says what the plan uses once saved, and why a reading was not used', () => {
    const saved = { consumptionEntity: 'sensor.car_efficiency' }
    const used: ConsumptionView = {
      kwhPer100Km: 15.625,
      source: 'homeassistant',
      entity: 'sensor.car_efficiency',
      unit: 'km_per_kwh',
      sensorValue: 6.4,
      observedAt: '2026-11-09T17:48:00Z',
    }
    expect(setup(saved, used).w.get('[data-testid=consumption-sensor]').text()).toBe(
      'The plan uses 15.6 kWh/100 km · 6.4 km/kWh from this sensor, read 12 min ago.',
    )
    expect(
      setup(saved, { ...used, note: 'sensor.car_efficiency reads 0' })
        .w.get('[data-testid=consumption-sensor]')
        .text(),
    ).toContain("The latest reading isn't used (sensor.car_efficiency reads 0), so it keeps this value.")
    expect(
      setup(saved, {
        kwhPer100Km: 18,
        source: 'manual',
        entity: 'sensor.car_efficiency',
        unit: 'km_per_kwh',
        note: 'no reading yet',
      })
        .w.get('[data-testid=consumption-sensor]')
        .text(),
    ).toBe('Not used: no reading yet. Until it has a valid value, the plan uses the value set here.')
  })
})
