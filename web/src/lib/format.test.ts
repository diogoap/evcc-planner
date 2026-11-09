import { describe, expect, it } from 'vitest'
import {
  ago,
  clock,
  dateTime,
  dayLong,
  dayShort,
  duration,
  km,
  kwh,
  localDate,
  price,
  relativeDay,
  weekdays,
  zonedToISO,
} from './format'

describe('format (Europe/Amsterdam)', () => {
  it('formats instants in the planner zone, not the browser zone', () => {
    expect(clock('2026-11-10T06:48:00Z')).toBe('07:48')
    expect(clock('2026-07-01T06:48:00Z')).toBe('08:48')
    expect(localDate('2026-11-09T23:30:00Z')).toBe('2026-11-10')
    expect(dateTime('2026-11-10T06:48:00Z')).toBe('Tue 10 Nov, 07:48')
  })

  it('formats plain dates', () => {
    expect(dayShort('2026-11-09')).toBe('Mon 09')
    expect(dayLong('2026-11-10')).toBe('Tuesday 10 November')
  })

  it('converts local wall time to instants across DST changes', () => {
    expect(zonedToISO('2026-11-10', '07:48')).toBe('2026-11-10T06:48:00Z')
    expect(zonedToISO('2026-07-01', '07:48')).toBe('2026-07-01T05:48:00Z')
    expect(zonedToISO('2026-10-25', '08:15')).toBe('2026-10-25T07:15:00Z')
    expect(zonedToISO('2026-03-29', '08:15')).toBe('2026-03-29T06:15:00Z')
  })

  it('describes relative days and ages', () => {
    const now = '2026-11-09T18:00:00Z'
    expect(relativeDay('2026-11-09T20:00:00Z', now)).toBe('Today')
    expect(relativeDay('2026-11-10T06:48:00Z', now)).toBe('Tomorrow')
    expect(relativeDay('2026-11-12T06:48:00Z', now)).toBe('Thu 12 Nov')
    expect(ago('2026-11-09T17:59:40Z', now)).toBe('just now')
    expect(ago('2026-11-09T17:48:00Z', now)).toBe('12 min ago')
    expect(ago('2026-11-09T15:45:00Z', now)).toBe('2 h ago')
  })

  it('formats quantities', () => {
    expect(km(100)).toBe('100 km')
    expect(km(6.54)).toBe('6.5 km')
    expect(km(0)).toBe('0 km')
    expect(km(null)).toBe('—')
    expect(kwh(4.32)).toBe('4.3 kWh')
    expect(price(0.3566)).toBe('€0.36/kWh')
    expect(duration(42)).toBe('42 min')
    expect(duration(80)).toBe('1 h 20 min')
    expect(duration(120)).toBe('2 h')
  })

  it('summarizes weekdays', () => {
    expect(weekdays([1, 2, 3, 4, 5])).toBe('Mon–Fri')
    expect(weekdays([1, 3, 5])).toBe('Mon, Wed, Fri')
    expect(weekdays([6, 7])).toBe('Sat, Sun')
    expect(weekdays([1, 2, 3, 4, 5, 6, 7])).toBe('Every day')
  })
})
