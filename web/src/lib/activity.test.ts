import { describe, expect, it } from 'vitest'
import { activityQuery } from '../api/client'
import type { AuditEvent } from '../api/types'
import {
  callLine,
  changeRows,
  eventHistory,
  fieldLabel,
  formatValue,
  groupByDay,
  size,
  subjectLabel,
} from './activity'

const event = (over: Partial<AuditEvent>): AuditEvent => ({
  id: 1,
  at: '2026-11-09T18:00:00Z',
  type: 'action',
  source: 'ui',
  action: 'trip.update',
  subjectType: 'trip',
  subjectId: 'recurring:1:2026-11-13',
  summary: 'Edited School drop-off on Fri 13 Nov',
  outcome: 'success',
  ...over,
})

describe('activity', () => {
  it('lists changed fields with their before and after values', () => {
    const rows = changeRows(
      event({
        before: { arriveBy: '2026-11-13 08:15', enabled: true },
        after: { arriveBy: '2026-11-13 08:45', enabled: false },
      }),
    )
    expect(rows).toEqual([
      { key: 'arriveBy', label: 'Arrive by', before: '2026-11-13 08:15', after: '2026-11-13 08:45' },
      { key: 'enabled', label: 'On', before: 'Yes', after: 'No' },
    ])
  })

  it('shows creations and deletions one-sided', () => {
    const created = changeRows(event({ action: 'trip.create', after: { title: 'Dentist' } }))
    expect(created).toEqual([{ key: 'title', label: 'Title', before: undefined, after: 'Dentist' }])
    const deleted = changeRows(event({ action: 'trip.delete', before: { title: 'Dentist' }, after: null }))
    expect(deleted[0]).toMatchObject({ before: 'Dentist', after: undefined })
  })

  it('labels fields and values', () => {
    expect(fieldLabel('consumptionKWhPer100Km')).toBe('Consumption (kWh/100 km)')
    expect(fieldLabel('somethingNew')).toBe('Something new')
    expect(fieldLabel('enabled', event({ subjectType: 'settings' }))).toBe('Daily advice')
    expect(formatValue(null)).toBe('—')
    expect(formatValue([])).toBe('none')
    expect(formatValue(['2026-12-21 to 2027-01-03 (Christmas)'])).toBe('2026-12-21 to 2027-01-03 (Christmas)')
    expect(formatValue(18.5)).toBe('18.5')
  })

  it('describes external calls', () => {
    const call = event({
      type: 'api_call',
      service: 'google_routes',
      subjectType: 'api',
      statusCode: 200,
      durationMs: 240,
      bytes: 1536,
    })
    expect(subjectLabel(call)).toBe('Google Routes')
    expect(callLine(call)).toBe('HTTP 200 · 240 ms · 1.5 kB')
    expect(callLine(event({ type: 'api_call', durationMs: 30 }))).toBe('No response · 30 ms · 0 B')
    expect(size(3 * 1024 * 1024)).toBe('3.0 MB')
  })

  it('groups by local day', () => {
    const groups = groupByDay([
      event({ id: 3, at: '2026-11-09T23:30:00Z' }), // 00:30 on the 10th in Amsterdam
      event({ id: 2, at: '2026-11-09T22:30:00Z' }),
      event({ id: 1, at: '2026-11-09T08:00:00Z' }),
    ])
    expect(groups.map((g) => [g.date, g.events.map((e) => e.id)])).toEqual([
      ['2026-11-10', [3]],
      ['2026-11-09', [2, 1]],
    ])
  })

  it('links events to the history of their subject', () => {
    expect(
      eventHistory(event({ after: { title: 'School drop-off', arriveBy: '2026-11-13 08:45' } })),
    ).toEqual({
      subject: 'trip:recurring:1:2026-11-13',
      label: 'School drop-off, Fri 13',
    })
    expect(
      eventHistory(event({ subjectType: 'schedule', subjectId: '2', before: { title: 'Pickup' } })),
    ).toEqual({
      subject: 'schedule:2',
      label: 'Pickup',
    })
    expect(eventHistory(event({ subjectType: 'adjustment', related: 'plan' }))?.subject).toBe('plan')
    expect(eventHistory(event({ subjectType: 'settings' }))).toBeUndefined()
    expect(eventHistory(event({ type: 'api_call', subjectType: 'api' }))).toBeUndefined()
  })

  it('builds filter query strings without empty values', () => {
    expect(
      activityQuery({ type: 'trip', source: '', from: '2026-11-01' }, { before: 12, format: undefined }),
    ).toBe('?type=trip&from=2026-11-01&before=12')
    expect(activityQuery({ subject: 'trip:manual:3' })).toBe('?subject=trip%3Amanual%3A3')
    expect(activityQuery({})).toBe('')
  })
})
