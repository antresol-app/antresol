// While a service syncs on the server (the periodic run, the pull right after
// linking, «Синхронизировать») its card asks for the link state again,
// with growing pauses, until the server says the sync ended: the card then shows
// how it went (the time, the status, the counts) without a reload. One hook for
// the three cards, so none guesses a running sync from a missing time.
import { useEffect, useRef } from 'react'
import { getAccounts } from '../../api/client'
import type { AccountsState } from '../../api/types'

/**
 * `active`: the card's state says `syncing`. Each answer goes to `apply`
 * (the card's own slice, picked by `pick`); the pause grows from `first` by half
 * each time, to `max` (ms). A failed read is asked again.
 */
export function useSyncPolling<S>(active: boolean, pick: (a: AccountsState) => S | undefined, apply: (s: S) => void, first: number, max: number) {
  // the latest closures, so a re-render does not restart the pauses
  const latest = useRef({ pick, apply })
  latest.current = { pick, apply }
  useEffect(() => {
    if (!active) return
    let live = true
    let timer: ReturnType<typeof setTimeout>
    const next = (delay: number) => {
      timer = setTimeout(() => {
        getAccounts()
          .then(
            (a) => {
              const s = latest.current.pick(a)
              if (live && s) latest.current.apply(s)
            },
            () => {},
          )
          .finally(() => {
            if (live) next(Math.min(delay * 1.5, max))
          })
      }, delay)
    }
    next(first)
    return () => {
      live = false
      clearTimeout(timer)
    }
  }, [active, first, max])
}
