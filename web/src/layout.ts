import { computed, reactive, ref, watch } from 'vue'

// Whether the charts get a page of their own: only with the bottom bar
// (narrow screens), and only when the Overview with its charts would not fit
// the window's height. The Overview and the Charts page report their sizes.
export const layout = reactive({
  /** The bottom bar is showing (screens up to 640 px wide). */
  compact: false,
  viewport: 0,
  /** The Overview's height without its charts; 0 until measured. */
  page: 0,
  /** The chart cards' height, measured when shown (an estimate before). */
  charts: 850,
})

export const chartsApart = computed(
  () => layout.compact && (layout.page === 0 || layout.page + layout.charts > layout.viewport),
)

if (typeof window !== 'undefined') {
  const media = window.matchMedia?.('(max-width: 640px)')
  const update = () => {
    layout.compact = !!media?.matches
    layout.viewport = window.innerHeight
  }
  update()
  media?.addEventListener('change', update)
  window.addEventListener('resize', update)
}

// How many of the plan's seven days the Overview and its charts show (the
// planner still plans all seven): fewer when the last days have little to
// show. Remembered on this device.
export const dayChoices = [3, 4, 5, 6, 7] as const
const daysKey = 'evcc-planner.days'
function savedDays() {
  try {
    const n = Number(localStorage.getItem(daysKey))
    return (dayChoices as readonly number[]).includes(n) ? n : 7
  } catch {
    return 7
  }
}
export const shownDays = ref(savedDays())
watch(shownDays, (n) => {
  try {
    localStorage.setItem(daysKey, String(n))
  } catch {
    // Not stored (e.g. private browsing); the choice lasts until reload.
  }
})
