import { nextTick, onMounted, onUnmounted, type Ref } from 'vue'

// Open dialogs and panels, the topmost last: only it reacts to Escape and Tab,
// so a dialog opened from the day panel closes on its own.
const open: symbol[] = []

const focusable =
  'a[href], button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea, summary'

/**
 * Modal behaviour for a dialog or panel: focus moves in when it opens (to the
 * first element matching `first`) and back when it closes, Tab stays inside,
 * Escape closes, and the page behind doesn't scroll while any is open.
 */
export function useDialog(root: Ref<HTMLElement | null>, close: () => void, first = 'input, select, button') {
  const id = Symbol('dialog')
  // Focus goes back here when the dialog closes.
  let opener: HTMLElement | null = null

  function onKey(e: KeyboardEvent) {
    if (open[open.length - 1] !== id) return
    if (e.key === 'Escape') {
      close()
      return
    }
    if (e.key !== 'Tab' || !root.value) return
    const items = [...root.value.querySelectorAll<HTMLElement>(focusable)]
    const head = items[0]
    const tail = items[items.length - 1]
    if (!head || !tail) return
    const inside = root.value.contains(document.activeElement)
    if (e.shiftKey && (document.activeElement === head || !inside)) {
      e.preventDefault()
      tail.focus()
    } else if (!e.shiftKey && (document.activeElement === tail || !inside)) {
      e.preventDefault()
      head.focus()
    }
  }

  onMounted(async () => {
    opener = document.activeElement instanceof HTMLElement ? document.activeElement : null
    open.push(id)
    document.addEventListener('keydown', onKey)
    document.documentElement.classList.add('modal-open')
    await nextTick()
    root.value?.querySelector<HTMLElement>(first)?.focus()
  })

  onUnmounted(() => {
    document.removeEventListener('keydown', onKey)
    open.splice(open.indexOf(id), 1)
    if (!open.length) document.documentElement.classList.remove('modal-open')
    if (opener?.isConnected) opener.focus()
  })
}
