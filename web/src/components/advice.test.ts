import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { AdviceSource, Day, DayAdvice, Proposal } from '../api/types'
import AdviceBadges from './AdviceBadges.vue'
import AdviceCard from './AdviceCard.vue'
import WeekList from './WeekList.vue'
import WeekStrip from './WeekStrip.vue'

const api = vi.hoisted(() => ({
  proposal: vi.fn(),
  preview: vi.fn(),
  applyAdjustment: vi.fn(),
}))
vi.mock('../api/client', () => ({ api }))
// Chart.js needs a canvas; the dialog's chart is not under test.
vi.mock('./PriceChart.vue', () => ({ default: { template: '<div />' } }))

const { default: ProposalDialog } = await import('./ProposalDialog.vue')

const advice = (over: Partial<DayAdvice>): DayAdvice => ({
  date: '2026-11-10',
  recommendation: 'charge_ahead',
  noSuggestion: false,
  confidence: 2,
  indicator: 'high',
  summary: 'Published Tue prices are cheaper than the rest of the window.',
  rationale: ['Tue: €0.218/kWh average incl. VAT (published day-ahead prices).'],
  laterChargePct: 30,
  price: { value: 0.218, basis: 'published', detail: 'published day-ahead prices', stale: false },
  weather: null,
  solarKWh: 7.6,
  solarHomeKWh: 2.1,
  sunSource: 'evcc',
  dismissed: false,
  action: 'extra_plan',
  ...over,
})

const sources: AdviceSource[] = [
  { name: 'EVCC prices', kind: 'published', status: 'ok', detail: 'Published through Tue 10 Nov' },
  { name: 'Tibber outlook', kind: 'estimate', status: 'stale', detail: 'Report 2026-11-02 to 2026-11-08' },
]

const dates = ['2026-11-09', '2026-11-10', '2026-11-11']

function card(a: DayAdvice) {
  return mount(AdviceCard, { props: { advice: a, sources, now: '2026-11-09T18:00:00Z', dates } })
}

describe('AdviceCard', () => {
  it('shows the recommendation, confidence, indicator, and plan action', async () => {
    const w = card(advice({}))
    expect(w.text()).toContain("Tuesday's advice")
    expect(w.get('[data-testid=advice-label]').text()).toBe('Charge ahead')
    expect(w.get('[data-testid=advice-confidence]').text()).toBe('Medium confidence')
    expect(w.get('[data-testid=advice-indicator]').text()).toBe('Charge: High (more)')
    expect(w.get('[data-testid=advice-basis]').text()).toBe('Published')
    // The sun names its source in one word.
    const sun = w.get('[data-testid=advice-sun]')
    for (const text of ['EVCC', '7.6 kWh', '2.1 kWh while home']) expect(sun.text()).toContain(text)
    expect(sun.text()).not.toContain('solar forecast')
    await w.get('button.primary').trigger('click')
    expect(w.emitted('propose')).toEqual([['extra_plan']])
    const dismiss = w.findAll('button').find((b) => b.text() === 'Dismiss')!
    await dismiss.trigger('click')
    expect(w.emitted('dismiss')).toEqual([[false]])
    expect(w.get('[data-testid=advice-sources]').text()).toContain('stale')
  })

  it('says to keep the car plugged in when the sun charges ahead, with no plan', () => {
    const w = card(
      advice({
        solar: true,
        confidence: 1,
        action: undefined,
        solarPct: 4.2,
        solarPrice: 0.268,
        solarGridPrice: 0.378,
        solarFrom: '2026-11-10T09:30:00Z',
        solarUntil: '2026-11-10T13:30:00Z',
        summary:
          "Tomorrow's sun charges at about €0.38/kWh, or €0.27 without the tax you don't pay on own solar: cheaper than Sunday (about €0.33). Keep the car plugged in.",
      }),
    )
    expect(w.get('[data-testid=advice-label]').text()).toBe('Charge ahead from solar')
    // The grid price in the sun's slots, as on the price chart.
    expect(w.get('[data-testid=advice-solar]').text()).toBe(
      'Keep the car plugged in from 10:30 to 14:30: EVCC charges from the sun by itself, about 4% at €0.38/kWh.',
    )
    expect(w.find('button.primary').exists()).toBe(false)
    expect(w.text()).not.toContain('no plan action')
  })

  it('offers no action for missing or weak evidence', () => {
    const none = card(
      advice({ noSuggestion: true, confidence: 0, recommendation: 'none', action: undefined }),
    )
    expect(none.get('[data-testid=advice-label]').text()).toBe('No data / no suggestion')
    expect(none.find('button.primary').exists()).toBe(false)
    expect(none.find('[data-testid=advice-indicator]').exists()).toBe(false)

    const weak = card(advice({ confidence: 1, action: undefined }))
    expect(weak.text()).toContain('Low confidence: no plan action')
    expect(weak.find('button.primary').exists()).toBe(false)
  })

  it('says why a postponement that would change nothing is not offered', () => {
    const note =
      "No postponement needed: every price until then is above EVCC's cheap-grid limit (€0.20/kWh; cheapest €0.38), so EVCC will not charge from the grid anyway."
    const w = card(
      advice({
        recommendation: 'defer',
        confidence: 3,
        deferUntil: '2026-11-11',
        action: undefined,
        actionNote: note,
      }),
    )
    expect(w.get('[data-testid=advice-action-note]').text()).toBe(note)
    expect(w.find('button.primary').exists()).toBe(false)
    expect(w.findAll('button').some((b) => b.text() === 'Dismiss')).toBe(true)
  })

  it('leads with the lowest published price and its time, over the average', () => {
    const w = card(
      advice({
        price: {
          value: 0.51,
          basis: 'published',
          detail: 'published',
          stale: false,
          min: 0.3696,
          minAt: '2026-11-11T02:45:00Z',
        },
      }),
    )
    const value = w.get('.fact-value')
    expect(value.text()).toBe('€0.37 (03:45)')
    expect(value.get('[role=img]').attributes('aria-label')).toBe('Lowest')
    const facts = w.get('.facts').text()
    expect(facts).toContain('€0.51 avg')
    expect(facts).not.toContain('incl. VAT')
  })

  it('labels estimates and shows an active change with a way to end it', async () => {
    const w = card(
      advice({
        action: undefined,
        price: { value: 0.298, basis: 'estimate', detail: 'Tibber outlook estimate', stale: true },
        adjustment: {
          id: 7,
          kind: 'extra_plan',
          source: 'ui',
          adviceDate: '2026-11-10',
          vehicle: 'leaf',
          loadpoint: 1,
          plan: { soc: 70, time: '2026-11-10T06:48:00Z' },
          expiresAt: '2026-11-10T06:48:00Z',
          status: 'active',
          note: '',
          createdAt: '2026-11-09T18:00:00Z',
        },
      }),
    )
    expect(w.get('[data-testid=advice-basis]').text()).toBe('Estimate · stale')
    expect(w.get('[data-testid=advice-adjustment]').text()).toContain(
      'Extra plan active: 70% by Tue 10 Nov, 07:48',
    )
    await w.get('[data-testid=advice-adjustment] button').trigger('click')
    expect(w.emitted('cancel')).toEqual([[7]])
  })

  it('steps between days', async () => {
    const w = card(advice({}))
    await w.get('[aria-label="Next day"]').trigger('click')
    expect(w.emitted('select')).toEqual([['2026-11-11']])
  })
})

const weather = {
  date: '2026-11-12',
  sunshinePct: 70,
  precipChancePct: 10,
  precipMinMm: 0,
  precipMaxMm: 0,
  tempMinLowC: 1,
  tempMinHighC: 1,
  tempMaxLowC: 8,
  tempMaxHighC: 8,
  windDir: 'N',
  windBft: 2,
}

describe('AdviceBadges', () => {
  it('shows the key figures as badges', () => {
    const w = mount(AdviceBadges, { props: { advice: advice({ weather: { ...weather, windBft: 5 } }) } })
    expect(w.get('[data-testid=badge-advice]').text()).toBe('Charge ahead')
    expect(w.get('[data-testid=badge-confidence]').text()).toBe('2/3')
    expect(w.findAll('[data-testid=badge-confidence] .dot.on')).toHaveLength(2)
    expect(w.get('[data-testid=badge-indicator]').attributes('aria-label')).toBe(
      'Charge indicator: High (more)',
    )
    expect(w.findAll('[data-testid=badge-indicator] .bar-step.on')).toHaveLength(3)
    expect(w.get('[data-testid=badge-price]').text()).toBe('€0.22')
    expect(w.get('[data-testid=badge-price]').classes()).toContain('basis-published')
    // With published slots the badge shows the day's lowest price.
    const low = mount(AdviceBadges, {
      props: {
        advice: advice({
          price: {
            value: 0.51,
            basis: 'published',
            detail: 'published',
            stale: false,
            min: 0.3696,
            minAt: '2026-11-11T02:45:00Z',
          },
        }),
      },
    })
    expect(low.get('[data-testid=badge-price]').text()).toBe('€0.37')
    expect(low.get('[data-testid=badge-price] [role=img]').attributes('aria-label')).toBe('Lowest')
    expect(low.get('[data-testid=badge-price]').attributes('title')).toBe(
      'Lowest published price €0.370/kWh at 03:45 (average €0.510)',
    )
    expect(w.get('[data-testid=badge-sun]').text()).toBe('7.6 kWh')
    expect(w.get('[data-testid=badge-sun]').attributes('title')).toContain('EVCC solar forecast, local')
    expect(w.get('[data-testid=badge-wind]').text()).toBe('5 Bft')
    expect(w.get('[data-testid=badge-wind]').classes()).toContain('tone-ok')
  })

  it('marks estimates and KNMI sunshine, and drops what is unknown', () => {
    const w = mount(AdviceBadges, {
      props: {
        advice: advice({
          recommendation: 'defer',
          indicator: undefined,
          confidence: 1,
          price: { value: 0.298, basis: 'estimate', detail: 'Tibber outlook estimate', stale: false },
          solarKWh: null,
          solarHomeKWh: null,
          sunSource: 'knmi',
          weather,
        }),
      },
    })
    expect(w.get('[data-testid=badge-advice]').text()).toBe('Defer')
    expect(w.find('[data-testid=badge-indicator]').exists()).toBe(false)
    expect(w.get('[data-testid=badge-price]').text()).toBe('~€0.30')
    expect(w.get('[data-testid=badge-price]').classes()).toContain('basis-estimate')
    expect(w.get('[data-testid=badge-sun]').text()).toBe('70%')
    expect(w.get('[data-testid=badge-sun]').attributes('title')).toContain('KNMI outlook, national')
    // Sunny is highlighted; Bft 2 is not windy.
    expect(w.get('[data-testid=badge-sun]').classes()).toEqual(expect.arrayContaining(['tone-ok']))
    expect(w.get('[data-testid=badge-sun]').classes()).not.toContain('basis-weekly')
    expect(w.get('[data-testid=badge-wind]').classes()).not.toContain('tone-ok')

    const none = mount(AdviceBadges, {
      props: {
        advice: advice({
          noSuggestion: true,
          confidence: 0,
          recommendation: 'none',
          price: null,
          sunSource: '',
        }),
      },
    })
    expect(none.get('[data-testid=badge-advice]').text()).toBe('No data')
    expect(none.find('[data-testid=badge-confidence]').exists()).toBe(false)
    expect(none.find('[data-testid=badge-sun]').exists()).toBe(false)
  })
})

describe('WeekStrip advice', () => {
  it('shows each day’s advice and price, and selects the day', async () => {
    const days: Day[] = [
      {
        date: '2026-11-10',
        distanceKm: 100,
        energyKWh: 18,
        tripCount: 3,
        disabledCount: 0,
        doneCount: 0,
        reviewCount: 0,
        status: 'charge',
        chargePct: 4,
      },
    ]
    const w = mount(WeekStrip, {
      props: {
        days,
        advice: [advice({ price: { ...advice({}).price!, min: 0.14 } })],
        selected: '2026-11-10',
      },
    })
    const tile = w.get('[data-testid="day-2026-11-10"]')
    expect(tile.get('[data-testid=badge-advice]').text()).toBe('Charge ahead')
    // Charging ahead from the sun has its own short label and icon.
    const sunny = mount(WeekStrip, { props: { days, advice: [advice({ solar: true })] } })
    expect(sunny.get('[data-testid=badge-advice]').text()).toBe('Solar')
    expect(tile.get('[data-testid=badge-price]').text()).toBe('€0.14')
    expect(tile.text()).toContain('Charge ahead, confidence medium')
    expect(tile.attributes('aria-pressed')).toBe('true')
    await tile.trigger('click')
    expect(w.emitted('select')).toEqual([['2026-11-10']])
  })
})

describe('WeekList', () => {
  it('lists each day with its figures, plan, and full advice, and selects the day', async () => {
    const days: Day[] = [
      {
        date: '2026-11-10',
        distanceKm: 100,
        energyKWh: 18,
        tripCount: 3,
        disabledCount: 0,
        doneCount: 0,
        reviewCount: 1,
        status: 'charge',
        chargePct: 3.8,
        targetSoc: 42,
        firstDepartAt: '2026-11-10T06:48:00Z',
      },
    ]
    const w = mount(WeekList, {
      props: {
        days,
        advice: [
          advice({
            weather: { ...weather, windBft: 5 },
            price: { ...advice({}).price!, min: 0.14, minAt: '2026-11-10T02:00:00Z' },
          }),
        ],
        selected: '2026-11-10',
      },
    })
    const row = w.get('[data-testid="day-2026-11-10"]')
    for (const text of ['Tue 10', '100 km', '18.0 kWh', '+4% charge', '42% by 07:48'])
      expect(row.text()).toContain(text)
    // Calendar trips to review sit with the advice.
    expect(row.get('.wl-advice [data-testid=review-count]').text()).toBe('1 trip to review')
    expect(row.get('[data-testid=badge-advice]').text()).toBe('Charge ahead')
    expect(row.get('[data-testid=badge-confidence]').text()).toBe('2/3')
    // Price and weather in their own columns: the average, as for estimates,
    // over the lowest published slot.
    expect(row.get('[data-testid=badge-price]').text()).toBe('€0.22 avg')
    expect(row.get('[data-testid=price-min]').text()).toBe('€0.14 min')
    expect(row.findAll('[data-testid=badge-price]')).toHaveLength(1)
    // Sunny (over 50%) and windy (Bft 5 or more) days are highlighted.
    const [sunshine, wind] = [row.get('[data-testid=badge-sunshine]'), row.get('[data-testid=badge-wind]')]
    expect(sunshine.text()).toBe('70%')
    expect(wind.text()).toBe('5 Bft')
    expect(sunshine.classes()).toContain('tone-ok')
    expect(wind.classes()).toContain('tone-ok')
    expect(wind.attributes('title')).toBe('Wind (KNMI outlook): windy, usually cheaper prices')
    expect(row.get('[data-testid=solar]').text()).toBe('7.6 kWh')
    expect(row.get('[data-testid=solar] [role=img]').attributes('aria-label')).toBe('Solar forecast')
    expect(row.find('[data-testid=badge-sun]').exists()).toBe(false)
    expect(row.attributes('aria-pressed')).toBe('true')
    await row.trigger('click')
    expect(w.emitted('select')).toEqual([['2026-11-10']])

    const calm = mount(WeekList, {
      props: { days, advice: [advice({ weather: { ...weather, sunshinePct: 50, windBft: 4 } })] },
    })
    expect(calm.get('[data-testid=badge-sunshine]').classes()).not.toContain('tone-ok')
    expect(calm.get('[data-testid=badge-wind]').classes()).not.toContain('tone-ok')
  })
})

describe('ProposalDialog', () => {
  const proposal: Proposal = {
    kind: 'extra_plan',
    date: '2026-11-10',
    available: true,
    advice: advice({}),
    soc: 80,
    minSoc: 42,
    startSoc: 38,
    capacityKWh: 59,
    readyBy: '2026-11-10T06:48:00Z',
    latestReadyBy: '2026-11-10T06:48:00Z',
    protected: { title: 'School drop-off', departAt: '2026-11-10T06:48:00Z', targetSoc: 42 },
    currentPlan: null,
    smartCostLimit: 0.2,
    expiresAt: '2026-11-10T06:48:00Z',
    maxExpiresAt: '2026-11-12T06:48:00Z',
    notes: ['EVCC has no plan now; this one is removed when it expires.'],
  }

  beforeEach(() => {
    api.proposal.mockReset().mockResolvedValue(proposal)
    api.preview
      .mockReset()
      .mockResolvedValue({ available: false, reason: 'Plug in first', slots: [], priceSlots: [] })
    api.applyAdjustment.mockReset().mockResolvedValue({
      kind: 'extra_plan',
      plan: { soc: 60, time: '2026-11-10T06:48:00Z' },
      expiresAt: '2026-11-10T06:48:00Z',
    })
  })

  it('writes nothing until confirmed, then sends the edited proposal', async () => {
    const w = mount(ProposalDialog, { props: { date: '2026-11-10', kind: 'extra_plan' } })
    await flushPromises()
    expect(w.get('[data-testid=proposal-soc]').element).toHaveProperty('value', '80')
    expect(w.get('[data-testid=proposal-protected]').text()).toContain('needs 42%')
    expect(api.applyAdjustment).not.toHaveBeenCalled()

    await w.get('[data-testid=proposal-soc]').setValue('60')
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(api.applyAdjustment).toHaveBeenCalledWith({
      kind: 'extra_plan',
      date: '2026-11-10',
      soc: 60,
      readyBy: '2026-11-10T06:48:00Z',
      expiresAt: '2026-11-10T06:48:00Z',
    })
    expect(w.emitted('applied')?.[0]?.[0]).toBe('Extra plan sent to EVCC: 60% by Tue 10 Nov, 07:48')
  })

  it('converts a kWh target to SoC with the usable capacity', async () => {
    const w = mount(ProposalDialog, { props: { date: '2026-11-10', kind: 'extra_plan' } })
    await flushPromises()
    await w.get('select[aria-label="Target unit"]').setValue('kWh')
    await w.get('[data-testid=proposal-kwh]').setValue('11.8')
    expect(w.text()).toContain('≈ 58% SoC') // 38% + 11.8 / 59 kWh
  })

  it('explains why a proposal is not available', async () => {
    api.proposal.mockResolvedValue({ ...proposal, available: false, reason: 'This advice was dismissed.' })
    const w = mount(ProposalDialog, { props: { date: '2026-11-10', kind: 'extra_plan' } })
    await flushPromises()
    expect(w.get('[data-testid=proposal-unavailable]').text()).toBe('This advice was dismissed.')
    expect(w.find('form').exists()).toBe(false)
  })
})
