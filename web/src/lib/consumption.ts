import type { ConsumptionUnit } from '../api/types'

// The car's consumption: stored in kWh/100 km, entered and shown in either
// kWh/100 km or km/kWh (100 / the other).

export const consumptionUnits: { value: ConsumptionUnit; label: string }[] = [
  { value: 'kwh_per_100km', label: 'kWh/100 km' },
  { value: 'km_per_kwh', label: 'km/kWh' },
]

/** The plausible consumption in a unit, as the planner accepts it: [lowest, highest]. */
export function consumptionRange(unit: ConsumptionUnit): [number, number] {
  return unit === 'km_per_kwh' ? [100 / 60, 20] : [5, 60]
}

/** A consumption given in unit, in kWh/100 km (and back: the formula is its own inverse). */
export function toKWhPer100Km(v: number, unit: ConsumptionUnit) {
  if (unit !== 'km_per_kwh') return v
  return v > 0 ? 100 / v : 0
}

export const fromKWhPer100Km = toKWhPer100Km

/** 16.1 kWh/100 km · 6.2 km/kWh */
export function bothUnits(kwh: number) {
  if (!(kwh > 0)) return '—'
  return `${kwh.toFixed(1)} kWh/100 km · ${(100 / kwh).toFixed(1)} km/kWh`
}

/** The unit a Home Assistant unit of measurement stands for, if it is one. */
export function unitOf(haUnit: string): ConsumptionUnit | undefined {
  switch (haUnit.toLowerCase().replace(/\s/g, '')) {
    case 'km/kwh':
      return 'km_per_kwh'
    case 'kwh/100km':
      return 'kwh_per_100km'
    default:
      return undefined
  }
}

/** Whether a consumption in kWh/100 km is plausible (5 to 60). */
export function plausible(kwh: number) {
  return kwh >= 5 && kwh <= 60
}
