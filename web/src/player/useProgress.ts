// Saves the viewer's position: every 10 s while playing, when the page is
// hidden or closed, and once more when the player goes away — unless the
// player already saved its final position (closed or finished), so a late
// save can never overwrite it. A jump to another profile's place is held
// (see familyMarks.heldPosition): the position before it is what is saved
// until the jump target has played on for a few seconds.
import { useCallback, useEffect, useRef, type RefObject } from 'react'
import { saveProgress } from '../api/client'
import { heldPosition, type Hold } from './familyMarks'

const SAVE_EVERY_MS = 10_000

export interface ProgressOptions {
  /** 0: nothing to save (a stream watched without adding it). */
  itemId: number
  /** Position on the whole timeline, seconds. */
  absRef: RefObject<number>
  /** Durations of all parts, seconds. */
  durRef: RefObject<number[]>
  playing: boolean
  /** Saves somewhere else than the library item (online playback); used even with itemId 0. */
  saver?: (positionMs: number, durationMs: number, keepalive: boolean) => Promise<void>
  /** A held jump (seconds on the whole timeline); cleared here once it played on. */
  holdRef?: RefObject<Hold | null>
}

export function useProgress({ itemId, absRef, durRef, playing, saver, holdRef }: ProgressOptions) {
  const last = useRef(-1)
  const done = useRef(false)

  const save = useCallback(
    (opts: { keepalive?: boolean; finished?: boolean; final?: boolean } = {}) => {
      if (done.current || (itemId <= 0 && !saver)) return Promise.resolve()
      if (opts.final || opts.finished) done.current = true
      const dur = durRef.current.reduce((a, b) => a + b, 0)
      let pos = opts.finished ? dur : absRef.current
      if (!opts.finished && holdRef) {
        const h = heldPosition(holdRef.current, pos)
        if (h.release) holdRef.current = null
        pos = h.pos
      }
      if (!(dur > 0) || !(pos > 0)) return Promise.resolve()
      const ms = Math.round(pos * 1000)
      if (!opts.finished && !opts.final && Math.abs(ms - last.current) < 500) return Promise.resolve()
      last.current = ms
      const d = Math.round(dur * 1000)
      return (saver ? saver(ms, d, Boolean(opts.keepalive)) : saveProgress(itemId, ms, d, opts.keepalive)).catch(() => {})
    },
    [absRef, durRef, itemId, saver, holdRef],
  )

  useEffect(() => {
    if (!playing) return
    const id = window.setInterval(() => void save(), SAVE_EVERY_MS)
    return () => window.clearInterval(id)
  }, [playing, save])

  useEffect(() => {
    const onHide = () => void save({ keepalive: true })
    const onVisibility = () => document.visibilityState === 'hidden' && onHide()
    window.addEventListener('pagehide', onHide)
    document.addEventListener('visibilitychange', onVisibility)
    return () => {
      window.removeEventListener('pagehide', onHide)
      document.removeEventListener('visibilitychange', onVisibility)
    }
  }, [save])

  // Leaving without close() (back button, route change): one last save.
  useEffect(() => {
    done.current = false
    return () => void save({ keepalive: true, final: true })
  }, [save])

  return save
}
