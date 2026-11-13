import { describe, expect, it } from 'vitest'
import type { Block, ControlView, Day, SendOption } from '../api/types'
import {
  aheadText,
  blockLabel,
  controlLabel,
  dayLabel,
  dayName,
  onDay,
  reviewText,
  sendOptionLabel,
} from './status'

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

const block = (over: Partial<Block>): Block => ({
  index: 0,
  tripKeys: ['a'],
  titles: ['School drop-off'],
  firstTitle: 'School drop-off',
  firstDestination: 'School',
  firstArriveAt: '2026-11-10T07:15:00Z',
  departAt: '2026-11-10T06:48:00Z',
  returnAt: '2026-11-10T07:32:00Z',
  distanceKm: 12,
  energyKWh: 2.16,
  energyPct: 3.7,
  ownRequiredSoc: 18.7,
  requiredSoc: 41.8,
  targetSoc: 42,
  status: 'charge',
  overlap: false,
  startSoc: 38,
  departSoc: 41.8,
  arrivalSoc: 38.1,
  chargePct: 3.8,
  chargeKWh: 2.24,
  gridKWh: 2.49,
  chargeMinutes: 41,
  chargeFrom: '2026-11-09T18:00:00Z',
  latestChargeStart: '2026-11-10T05:36:00Z',
  shortfall: 0,
  ...over,
})

describe('status labels', () => {
  it('labels days', () => {
    expect(dayLabel(day({})).text).toBe('No trips')
    expect(dayLabel(day({ disabledCount: 2 })).text).toBe('All trips off')
    expect(dayLabel(day({ status: 'completed' })).text).toBe('Completed')
    expect(
      dayLabel(
        day({
          status: 'charge',
          chargePct: 14.2,
          requiredSoc: 41.2,
          targetSoc: 42,
          firstDepartAt: '2026-11-10T06:48:00Z',
        }),
      ),
    ).toEqual({ text: '+15% charge', tone: 'info', detail: '42% by 07:48' })
    expect(dayLabel(day({ status: 'charge', chargePct: 0, targetSoc: 20 })).text).toBe('Covered')
    expect(
      dayLabel(day({ status: 'unknown', targetSoc: 30, firstDepartAt: '2026-11-10T06:48:00Z' })).detail,
    ).toBe('30% by 07:48')
    expect(dayLabel(day({ status: 'shortfall' })).tone).toBe('bad')
  })

  it('explains departures', () => {
    expect(blockLabel(block({})).text).toBe('Charge 2.2 kWh (about 41 min); start charging by 06:36.')
    expect(blockLabel(block({ status: 'ready' })).tone).toBe('ok')
    expect(blockLabel(block({ status: 'shortfall', shortfall: 4.2 })).text).toContain('5% short')
    expect(blockLabel(block({ status: 'unknown' })).text).toContain('plug in')
  })
})

describe('send options', () => {
  const opt = (days: number, from: string, through: string): SendOption => ({
    days,
    from,
    through,
    readyBy: '2026-11-10T06:48:00Z',
    requiredSoc: 45.5,
    targetSoc: 46,
    tripKeys: [],
    titles: [],
    distanceKm: 100,
    energyKWh: 18,
    exceeds: false,
    covered: false,
  })
  const monday = '2026-11-09T18:00:00Z' // 19:00 in Amsterdam

  it('names days relative to now', () => {
    expect(dayName('2026-11-09', monday)).toBe('today')
    expect(dayName('2026-11-10', monday)).toBe('tomorrow')
    expect(dayName('2026-11-12', monday)).toBe('Thu 12 Nov')
    expect(onDay('2026-11-10', monday)).toBe('tomorrow')
    expect(onDay('2026-11-12', monday)).toBe('on Thu 12 Nov')
  })

  it('labels each horizon from the day of the next departure', () => {
    expect(sendOptionLabel(opt(1, '2026-11-09', '2026-11-09'), monday)).toBe('Trips today')
    expect(sendOptionLabel(opt(2, '2026-11-09', '2026-11-10'), monday)).toBe('Today and tomorrow')
    expect(sendOptionLabel(opt(3, '2026-11-09', '2026-11-11'), monday)).toBe('Today to Wed 11 Nov')
    expect(sendOptionLabel(opt(1, '2026-11-10', '2026-11-10'), monday)).toBe('Trips tomorrow')
    expect(sendOptionLabel(opt(2, '2026-11-10', '2026-11-11'), monday)).toBe('Tomorrow and Wed 11 Nov')
    expect(sendOptionLabel(opt(1, '2026-11-12', '2026-11-12'), monday)).toBe('Trips on Thu 12 Nov')
  })
})

describe('controlLabel', () => {
  const target = {
    soc: 42,
    minSoc: 42,
    readyBy: '2026-11-10T06:48:00Z',
    tripKey: 'recurring:1:2026-11-10',
    title: 'School drop-off',
    covered: false,
    ahead: [],
    savingEur: 0,
    charging: [],
  }
  const control = (over: Partial<ControlView>): ControlView => ({
    mode: 'automatic',
    paused: false,
    target,
    ...over,
  })

  it('names manual modes without detail', () => {
    expect(controlLabel(control({ mode: 'manual' }), true)).toEqual({
      mode: 'Send manually',
      detail: '',
      tone: 'muted',
    })
    expect(controlLabel(control({ mode: 'plug_in' }), true).mode).toBe('Sent when plugged in')
  })

  it('says what automatic plans send', () => {
    expect(controlLabel(control({}), false).detail).toBe('Sends 42% by Tue 10 Nov, 07:48 when plugged in.')
    expect(controlLabel(control({}), true).detail).toBe('Target 42% by Tue 10 Nov, 07:48.')
    expect(controlLabel(control({ target: { ...target, covered: true } }), true)).toMatchObject({
      detail: '42% by Tue 10 Nov, 07:48: the charge already covers it.',
      tone: 'ok',
    })
    expect(controlLabel(control({ target: null }), true).detail).toBe('No upcoming trip needs a plan.')
  })

  it('explains a pause', () => {
    const paused = control({ paused: true, pausedPlan: { soc: 80, time: '2026-11-11T06:00:00Z' } })
    expect(controlLabel(paused, true)).toEqual({
      mode: 'Automatic · paused',
      detail: 'Paused while the chosen plan (80% by Wed 11 Nov, 07:00) is in EVCC; resumes when it finishes.',
      tone: 'warn',
    })
  })
})

describe('aheadText', () => {
  const trip = {
    tripKey: 'manual:1',
    title: 'Utrecht',
    departAt: '2026-11-12T16:00:00Z',
    energyPct: 37,
    priceAhead: 0.23,
    priceLater: 0.31,
    savingEur: 1.94,
  }
  it('compares the prices', () => {
    expect(aheadText(trip)).toBe(
      'Includes Utrecht (Thu 12 17:00): €0.23/kWh before the next departure vs €0.31/kWh before it, saves about €1.94.',
    )
    expect(aheadText({ ...trip, savingEur: 0.01 })).not.toContain('saves')
  })
  it('explains a trip with too little time at home before it', () => {
    expect(aheadText({ ...trip, priceLater: null })).toBe(
      'Includes Utrecht (Thu 12 17:00): too little time at home before it to charge.',
    )
  })
})

describe('reviewText', () => {
  it('counts the trips to review', () => {
    expect(reviewText(1)).toBe('1 trip to review')
    expect(reviewText(3)).toBe('3 trips to review')
  })
})
