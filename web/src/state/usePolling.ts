// A task asked again every `ms` while the page is open: not while the tab is
// hidden (it runs once more as the tab shows again), never twice at the same
// time (a slow answer skips the ticks it covers, a hung one only for so long),
// aborted when the page leaves.
import { useEffect, useRef } from 'react'

export interface PollingOptions {
  /** Ask once at once, on mount (default true; false when the page loads the first answer itself). */
  immediate?: boolean
  /** Keep asking in a hidden tab too (default false). */
  runWhenHidden?: boolean
  /** Starts over (aborts, asks at once) when it changes: the id the task reads. */
  key?: unknown
}

/** The longest one run may last (a stalled connection): then the next tick asks again. At least this, or three periods. */
const HANG_MS = 30_000

/**
 * `task(signal)` may return a promise: the next tick waits for it, but not past a hang (the run's
 * signal is aborted after three periods or 30 s) and not when the tab shows again (a new run
 * replaces the one still out, whose signal is aborted). A failure is the task's to show.
 */
export function usePolling(task: (signal: AbortSignal) => unknown, ms: number, opts: PollingOptions = {}) {
  const { immediate = true, runWhenHidden = false, key } = opts
  const latest = useRef(task)
  latest.current = task
  useEffect(() => {
    const ac = new AbortController()
    let out: AbortController | null = null
    const run = async (force = false, replace = false) => {
      if (ac.signal.aborted || (out && !replace) || (!force && !runWhenHidden && document.hidden)) return
      out?.abort()
      const mine = new AbortController()
      out = mine
      const stop = () => mine.abort()
      ac.signal.addEventListener('abort', stop)
      const hung = window.setTimeout(() => {
        mine.abort()
        if (out === mine) out = null
      }, Math.max(ms * 3, HANG_MS))
      try {
        await latest.current(mine.signal)
      } catch {
        /* the task shows its own failure; asked again at the next tick */
      } finally {
        window.clearTimeout(hung)
        ac.signal.removeEventListener('abort', stop)
        if (out === mine) out = null
      }
    }
    // the first answer is wanted even in a tab opened in the background
    if (immediate) void run(true)
    const id = window.setInterval(() => void run(), ms)
    const onVisible = () => !document.hidden && void run(false, true)
    document.addEventListener('visibilitychange', onVisible)
    return () => {
      ac.abort()
      window.clearInterval(id)
      document.removeEventListener('visibilitychange', onVisible)
    }
  }, [ms, immediate, runWhenHidden, key])
}
