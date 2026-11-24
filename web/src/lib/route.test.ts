import { describe, expect, it } from 'vitest'
import { isHome, legText, moved, newStopIndex, pointLabel, pointLetter, routeText } from './route'

describe('route', () => {
  it('reads as one line with saved places by name', () => {
    expect(
      routeText([
        { name: 'Home', placeName: 'Home', home: true },
        { name: 'School', placeName: 'School', home: false },
        { name: 'Bouwmarkt 5', home: false },
        { name: '', home: false },
      ]),
    ).toBe('Home → School → Bouwmarkt 5 → No location')
  })

  it('names and letters the points', () => {
    expect([0, 1, 2, 3].map((i) => pointLabel(i, 4))).toEqual(['Start', 'Stop 1', 'Stop 2', 'End'])
    expect([0, 1].map((i) => pointLabel(i, 2))).toEqual(['Start', 'End'])
    expect([0, 1, 4].map(pointLetter)).toEqual(['A', 'B', 'E'])
    expect(isHome(' home ')).toBe(true)
    expect(isHome('Home 1, Town')).toBe(false)
  })

  it('moves points and adds stops before the end', () => {
    expect(moved(['a', 'b', 'c'], 2, 1)).toEqual(['a', 'c', 'b'])
    expect(moved(['a', 'b', 'c'], 0, -1)).toEqual(['a', 'b', 'c'])
    expect(newStopIndex(3)).toBe(2)
    expect(newStopIndex(2)).toBe(1)
    expect(legText({ distanceKm: 6, minutes: 12 })).toBe('6.0 km · 12 min')
  })
})
