import { describe, expect, it } from 'vitest'
import { bothUnits, consumptionRange, fromKWhPer100Km, plausible, toKWhPer100Km, unitOf } from './consumption'

describe('consumption units', () => {
  it('converts between kWh/100 km and km/kWh', () => {
    expect(toKWhPer100Km(18, 'kwh_per_100km')).toBe(18)
    expect(toKWhPer100Km(5, 'km_per_kwh')).toBe(20)
    expect(fromKWhPer100Km(16, 'km_per_kwh')).toBe(6.25)
    expect(toKWhPer100Km(0, 'km_per_kwh')).toBe(0)
  })

  it('shows a value in both units', () => {
    expect(bothUnits(16)).toBe('16.0 kWh/100 km · 6.3 km/kWh')
    expect(bothUnits(15.625)).toBe('15.6 kWh/100 km · 6.4 km/kWh')
    expect(bothUnits(0)).toBe('—')
  })

  it('knows the plausible range and Home Assistant units', () => {
    expect(consumptionRange('kwh_per_100km')).toEqual([5, 60])
    expect(consumptionRange('km_per_kwh')[1]).toBe(20)
    expect(plausible(18)).toBe(true)
    expect(plausible(200)).toBe(false)
    expect(unitOf('km/kWh')).toBe('km_per_kwh')
    expect(unitOf('kWh/100 km')).toBe('kwh_per_100km')
    expect(unitOf('mi/kWh')).toBeUndefined()
  })
})
