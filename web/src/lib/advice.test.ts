import { describe, expect, it } from 'vitest'
import type { Day, DayAdvice } from '../api/types'
import { adviceLabel, confidenceLabel, sunText, tempText, windText } from './advice'

const advice = (over: Partial<DayAdvice>): DayAdvice => ({
  date: '2026-11-10',
  recommendation: 'none',
  noSuggestion: false,
  confidence: 3,
  summary: '',
  rationale: [],
  laterChargePct: 0,
  price: null,
  weather: null,
  solarKWh: null,
  solarHomeKWh: null,
  sunSource: '',
  dismissed: false,
  ...over,
})

const day = (over: Partial<Day>): Day => ({
  date: '2026-11-10',
  distanceKm: 0,
  energyKWh: 0,
  tripCount: 0,
  disabledCount: 0,
  doneCount: 0,
  reviewCount: 0,
  status: 'none',
  chargePct: 0,
  ...over,
})

describe('adviceLabel', () => {
  it('names each recommendation', () => {
    expect(adviceLabel(advice({ recommendation: 'charge_ahead' })).text).toBe('Charge ahead')
    expect(adviceLabel(advice({ recommendation: 'defer' })).text).toBe('Defer optional charging')
    expect(adviceLabel(advice({ noSuggestion: true, confidence: 0 })).text).toBe('No data / no suggestion')
    expect(adviceLabel(advice({}), day({ status: 'completed' })).text).toBe('Completed')
    // Advice for the rest of a day whose trips are done still shows.
    expect(adviceLabel(advice({ recommendation: 'defer' }), day({ status: 'completed' })).text).toBe(
      'Defer optional charging',
    )
  })

  it('reads "none" as keep plan or no plan needed', () => {
    expect(adviceLabel(advice({}), day({ status: 'charge', chargePct: 4 })).text).toBe('Keep plan')
    expect(adviceLabel(advice({}), day({ status: 'ready' })).text).toBe('No plan needed')
  })

  it('mutes low-confidence advice', () => {
    expect(adviceLabel(advice({ recommendation: 'charge_ahead', confidence: 1 })).tone).toBe('muted')
    expect(adviceLabel(advice({ recommendation: 'charge_ahead', confidence: 2 })).tone).toBe('ok')
  })
})

describe('weather texts', () => {
  const weather = {
    date: '2026-11-10',
    sunshinePct: 30,
    precipChancePct: 50,
    precipMinMm: 0,
    precipMaxMm: 0,
    tempMinLowC: 5,
    tempMinHighC: 5,
    tempMaxLowC: 10,
    tempMaxHighC: 10,
    windDir: 'W',
    windBft: 5,
  }

  it('combines KNMI wind and market wind', () => {
    const a = advice({ marketWind: 'high', marketWindGW: 6.5, weather })
    expect(windText(a)).toBe('Bft 5 W · market high (6.5 GW)')
    expect(tempText(a)).toBe('5–10 °C')
  })

  it('takes local sun from EVCC, else KNMI sunshine', () => {
    expect(sunText(advice({ sunSource: 'evcc', solarKWh: 7.6, solarHomeKWh: 2.1, weather }))).toBe(
      '7.6 kWh · 2.1 kWh while home',
    )
    expect(sunText(advice({ sunSource: 'knmi', weather }))).toBe('30% of possible sunshine')
    expect(sunText(advice({}))).toBe('')
  })

  it('labels confidence on a 1–3 scale', () => {
    expect(confidenceLabel(3)).toBe('3/3 — High')
    expect(confidenceLabel(1)).toBe('1/3 — Low')
    expect(confidenceLabel(0)).toBe('—')
  })
})
