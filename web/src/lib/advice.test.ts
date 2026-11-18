import { describe, expect, it } from 'vitest'
import type { Day, DayAdvice, Target } from '../api/types'
import {
  adviceLabel,
  confidenceLabel,
  goodPrices,
  referenceText,
  sunText,
  tempText,
  windText,
} from './advice'

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
    expect(adviceLabel(advice({ recommendation: 'charge_extra' })).text).toBe('Charge extra')
    expect(adviceLabel(advice({ recommendation: 'charge_extra', targetSoc: 80 })).text).toBe(
      'Charge extra to 80%',
    )
    // Days not published yet show the outlook instead of "no data".
    const later = { noSuggestion: true, confidence: 0 }
    expect(adviceLabel(advice({ ...later, outlook: 'cheaper' })).short).toBe('Cheaper?')
    expect(adviceLabel(advice({ ...later, outlook: 'dearer' })).text).toBe('Looks dearer (not published yet)')
    expect(adviceLabel(advice({ ...later, outlook: 'similar' })).icon).toBe('dash')
    expect(adviceLabel(advice({ recommendation: 'defer' })).text).toBe('Defer optional charging')
    expect(adviceLabel(advice({ noSuggestion: true, confidence: 0 })).text).toBe('No data / no suggestion')
    expect(adviceLabel(advice({}), day({ status: 'completed' })).text).toBe('Completed')
    // A day the trip plan charges later trips ahead, without charge extra.
    const target: Target = {
      soc: 80,
      minSoc: 21,
      readyBy: '2026-11-10T15:33:00Z',
      tripKey: 'recurring:2:2026-11-10',
      title: 'Pickup',
      covered: false,
      ahead: [
        {
          tripKey: 'recurring:1:2026-11-11',
          title: 'School',
          departAt: '2026-11-11T06:33:00Z',
          energyPct: 4,
          priceAhead: 0,
          priceLater: null,
          savingEur: 0,
        },
      ],
      savingEur: 0,
      charging: [{ start: '2026-11-10T13:00:00Z', end: '2026-11-10T15:00:00Z', value: 0.27 }],
    }
    expect(adviceLabel(advice({}), day({ chargePct: 0 }), target)).toMatchObject({
      text: 'Charge ahead to 80%',
      short: 'Charge ahead',
      icon: 'bolt',
    })
    expect(adviceLabel(advice({ date: '2026-11-11' }), day({ chargePct: 0 }), target).text).not.toContain(
      'ahead',
    )
    expect(adviceLabel(advice({}), day({ chargePct: 0 }), { ...target, covered: true }).text).not.toContain(
      'ahead',
    )
    expect(
      adviceLabel(advice({ recommendation: 'charge_extra', targetSoc: 90 }), undefined, target).text,
    ).toBe('Charge extra to 90%')
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
    expect(adviceLabel(advice({ recommendation: 'charge_extra', confidence: 1 })).tone).toBe('muted')
    expect(adviceLabel(advice({ recommendation: 'charge_extra', confidence: 2 })).tone).toBe('ok')
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

  it("names charge extra's reference", () => {
    expect(referenceText(advice({}))).toBe('')
    expect(referenceText(advice({ reference: { value: 0.2, basis: 'published', detail: '' } }))).toBe(
      "Tomorrow's lowest €0.20",
    )
    expect(referenceText(advice({ reference: { value: 0.3, basis: 'history', days: 5, detail: '' } }))).toBe(
      "Past 5 days' lowest €0.30",
    )
    expect(
      referenceText(
        advice({
          reference: { value: 0.2933, basis: 'outlook', day: 'Thursday', source: 'weekly', detail: '' },
        }),
      ),
    ).toBe("Thu's expected lowest €0.29")
  })
})

describe('goodPrices', () => {
  const target = (over: Partial<Target>): Target => ({
    soc: 60,
    minSoc: 42,
    readyBy: '2026-11-10T06:48:00Z',
    tripKey: 'recurring:1:2026-11-10',
    title: 'School drop-off',
    covered: false,
    ahead: [],
    savingEur: 0,
    charging: [],
    ...over,
  })
  const ahead = {
    tripKey: 'k',
    title: 'Office',
    departAt: '2026-11-12T07:00:00Z',
    energyPct: 9,
    priceAhead: 0.18,
  }

  it('marks days whose low prices charge extra or charge ahead charges at', () => {
    const extra = advice({ date: '2026-11-10', recommendation: 'charge_extra', targetSoc: 60 })
    const solar = advice({ date: '2026-11-11', recommendation: 'charge_extra', targetSoc: 50, solar: true })
    expect([...goodPrices([extra, solar], null)]).toEqual([
      ['2026-11-10', 'Good price: cheap enough to charge extra to 60%'],
    ])
    // Charge ahead: the days of the trip plan's expected charging (Amsterdam
    // time: 23:30 UTC on the 9th is the 10th).
    const t = target({
      ahead: [{ ...ahead, priceLater: 0.3, savingEur: 1.2 }],
      savingEur: 1.2,
      charging: [
        { start: '2026-11-09T18:00:00Z', end: '2026-11-09T18:15:00Z', value: 0.2 },
        { start: '2026-11-09T23:30:00Z', end: '2026-11-09T23:45:00Z', value: 0.15 },
      ],
    })
    expect(Object.fromEntries(goodPrices([extra], t))).toEqual({
      '2026-11-10':
        'Good price: cheap enough to charge extra to 60%; the trip plan charges later trips ahead (saves about €1.20)',
      '2026-11-09': 'Good price: the trip plan charges later trips ahead (saves about €1.20)',
    })
    // Without later trips charged ahead, or once covered, the plan's own
    // charging is not highlighted; neither is advice without a suggestion.
    expect(goodPrices([], target({ charging: t.charging })).size).toBe(0)
    expect(goodPrices([], { ...t, covered: true }).size).toBe(0)
    expect(goodPrices([{ ...extra, noSuggestion: true }], null).size).toBe(0)
  })
})
