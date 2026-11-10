import { describe, expect, it } from 'vitest'
import type { Block, Day } from '../api/types'
import { blockLabel, dayLabel } from './status'

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
