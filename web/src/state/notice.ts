// A short confirmation that outlives the page it came from («„Брат“ в
// Корзине» after a delete navigates away): one at a time, shown by the
// Shell's <Notice>, gone after a few seconds.
import { useSyncExternalStore } from 'react'

let current: { text: string; key: number } | null = null
let timer: ReturnType<typeof setTimeout> | undefined
let seq = 0
const listeners = new Set<() => void>()

function emit() {
  for (const l of listeners) l()
}

/** Shows text for ms (default 3 s), replacing what was shown. */
export function showNotice(text: string, ms = 3000) {
  clearTimeout(timer)
  current = { text, key: ++seq }
  emit()
  timer = setTimeout(() => {
    current = null
    emit()
  }, ms)
}

export function clearNotice() {
  clearTimeout(timer)
  current = null
  emit()
}

function subscribe(l: () => void) {
  listeners.add(l)
  return () => {
    listeners.delete(l)
  }
}

/** The notice on show, null when none. */
export function useNotice() {
  return useSyncExternalStore(subscribe, () => current)
}
