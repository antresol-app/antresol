// «Отмечать и на Shikimori?»: asked once, after the first local mark change
// of a profile that linked Shikimori and has not chosen yet. The server
// remembers it was asked (write_state «asked»), so other devices stay quiet.
import { useSyncExternalStore } from 'react'
import { markShikimoriAsked } from '../api/client'
import { loadAccounts, setAccountsState } from './accounts'
import { session } from './session'

let open = false
let busy = false
const listeners = new Set<() => void>()
const emit = () => listeners.forEach((l) => l())

/** Call after a mark was saved: opens the prompt when it is due. */
export async function promptAfterMark() {
  if (open || busy || session.get().user?.isKid) return
  busy = true
  try {
    const s = await loadAccounts()
    if (!s || !s.linked || s.writeState !== 'unasked' || !s.canWrite) return
    setAccountsState({ ...s, writeState: 'asked' }) // once per session even if the call fails
    open = true
    emit()
    markShikimoriAsked().catch(() => undefined)
  } finally {
    busy = false
  }
}

export function closeWritePrompt() {
  open = false
  emit()
}

export function useWritePromptOpen() {
  return useSyncExternalStore(
    (l) => {
      listeners.add(l)
      return () => void listeners.delete(l)
    },
    () => open,
  )
}
