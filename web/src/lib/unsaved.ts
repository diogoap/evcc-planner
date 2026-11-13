import { computed, ref, type Ref } from 'vue'

/**
 * Unsaved changes in a form: `mark` remembers the values as loaded or saved,
 * `dirty` says whether the form differs from them, and `discard` puts them
 * back.
 */
export function useUnsaved<T>(form: Ref<T | null>) {
  const saved = ref('')
  const dirty = computed(
    () => !!form.value && saved.value !== '' && JSON.stringify(form.value) !== saved.value,
  )
  function mark() {
    saved.value = form.value ? JSON.stringify(form.value) : ''
  }
  function discard() {
    if (saved.value) form.value = JSON.parse(saved.value) as T
  }
  return { dirty, mark, discard }
}
