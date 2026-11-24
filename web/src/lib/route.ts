import type { TripLeg, TripPoint } from '../api/types'

// A trip's route: 2 to 5 points from where it starts to where it ends. Home
// stands for the home address set in Settings.
export const MIN_POINTS = 2
export const MAX_POINTS = 5
export const HOME = 'Home'

export const isHome = (name: string) => name.trim().toLowerCase() === HOME.toLowerCase()

/** "Home → School → Shop → Home", with saved places by their name. */
export function routeText(points: TripPoint[]): string {
  return points.map((p) => p.placeName || p.name || 'No location').join(' → ')
}

/** What a point is called in the editor: Start, Stop 1, Stop 2, …, End. */
export function pointLabel(index: number, count: number): string {
  if (index === 0) return 'Start'
  if (index === count - 1) return 'End'
  return `Stop ${index}`
}

/** The letter marking a point, as in A → B → A. */
export const pointLetter = (index: number) => String.fromCharCode(65 + index)

/** A copy of the list with one item moved. */
export function moved<T>(list: T[], from: number, to: number): T[] {
  if (to < 0 || to >= list.length || from === to) return list.slice()
  const out = list.slice()
  const [item] = out.splice(from, 1)
  out.splice(to, 0, item)
  return out
}

/** Where a new stop goes: just before the end. */
export const newStopIndex = (count: number) => Math.max(1, count - 1)

/** "6.0 km · 12 min" for one leg. */
export function legText(leg: TripLeg): string {
  return `${leg.distanceKm.toFixed(1)} km · ${leg.minutes} min`
}
