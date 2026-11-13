// Pull to refresh: the indicator follows the finger at half speed, up to a
// limit, and a release past the threshold refreshes.
export const pullThreshold = 64
export const pullMax = 96

/** How far the indicator moves for a finger dragged down by `dy` px. */
export function pullDistance(dy: number) {
  return dy <= 0 ? 0 : Math.min(pullMax, dy * 0.5)
}

/** The installed app (home screen), which has no browser reload of its own. */
export function isStandalone() {
  const ios = (navigator as Navigator & { standalone?: boolean }).standalone === true
  return ios || !!window.matchMedia?.('(display-mode: standalone)').matches
}
