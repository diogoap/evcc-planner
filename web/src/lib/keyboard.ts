// The on-screen keyboard. iOS moves fixed elements with it and, in the
// installed app, can leave them where its top was after it closes: the
// bottom bar then floats halfway up a long page. So while a text field has
// focus the bar is hidden (the keyboard covers it anyway), and when focus
// leaves the page is laid out again.

const notText = new Set([
  'button',
  'checkbox',
  'color',
  'file',
  'hidden',
  'image',
  'radio',
  'range',
  'reset',
  'submit',
])

/** Whether an element brings up the keyboard when focused. */
export function opensKeyboard(el: Element | null) {
  if (el instanceof HTMLTextAreaElement) return !el.readOnly
  if (el instanceof HTMLInputElement) return !notText.has(el.type) && !el.readOnly
  return el instanceof HTMLElement && el.isContentEditable === true
}

/**
 * Marks the page with the `typing` class while a text field has focus, and
 * nudges the scroll position when typing ends so fixed elements return to
 * their place. Returns a function that stops watching.
 */
export function watchKeyboard(doc: Document = document, win: Window = window) {
  const root = doc.documentElement
  let timer: ReturnType<typeof setTimeout> | undefined
  const update = () => {
    const typing = opensKeyboard(doc.activeElement)
    if (typing === root.classList.contains('typing')) return
    root.classList.toggle('typing', typing)
    if (!typing) win.scrollTo(win.scrollX, win.scrollY)
  }
  // While focus moves between fields, activeElement is briefly the body.
  const later = () => {
    clearTimeout(timer)
    timer = setTimeout(update, 50)
  }
  doc.addEventListener('focusin', update)
  doc.addEventListener('focusout', later)
  return () => {
    clearTimeout(timer)
    doc.removeEventListener('focusin', update)
    doc.removeEventListener('focusout', later)
    root.classList.remove('typing')
  }
}
