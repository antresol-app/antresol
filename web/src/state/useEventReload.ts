// Reloads on the server's events without a burst: events that come within
// `wait` of the first one are answered by one reload (an import or a season's
// download sends one per write batch), and none in a hidden tab — the tab
// reloads once as it shows again.
import { useEffect, useRef } from 'react'
import { useEvents } from '../api/client'

export function useEventReload(reload: () => void, types: readonly string[], wait = 400) {
  const latest = useRef(reload)
  latest.current = reload
  const timer = useRef<number | undefined>(undefined)
  const stale = useRef(false)
  useEvents(() => {
    if (timer.current !== undefined) return
    timer.current = window.setTimeout(() => {
      timer.current = undefined
      if (document.hidden) stale.current = true
      else latest.current()
    }, wait)
  }, types)
  useEffect(() => {
    const onVisible = () => {
      if (document.hidden || !stale.current) return
      stale.current = false
      latest.current()
    }
    document.addEventListener('visibilitychange', onVisible)
    return () => {
      window.clearTimeout(timer.current)
      timer.current = undefined
      document.removeEventListener('visibilitychange', onVisible)
    }
  }, [])
}
