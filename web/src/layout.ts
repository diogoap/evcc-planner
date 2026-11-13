import { computed, reactive } from 'vue'

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
