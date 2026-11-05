// Saves the reading position: 2 s after the reader stops moving, at once
// (keepalive) when the page is hidden or closed, and when the reader closes.
// The position the book was opened at is not saved again (opening a book is
// not reading it). Failed saves are retried with the next move and reported
// after two in a row. Each position is queued (offline/store) until the
// server has it, so one reached without the server goes out when it is back.
// While a jump to another profile's place is held (`hold(true)`, «Семья»),
// nothing is saved: the reader only looks there until it reads on.
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { ApiError, saveReading } from '../api/client'
import { rememberProgress, settleProgress, settlesProgress } from '../offline/store'
import { t } from '../i18n'

export interface ReadingPosition {
  /** EPUB CFI or `page:N`. */
  locator: string
  /** 0..1 */
  fraction: number
}

export const SAVE_DEBOUNCE_MS = 2000
const REPORT_AFTER = 2

const keyOf = (p: ReadingPosition) => `${p.locator}|${p.fraction}`

type Flush = (keepalive?: boolean) => Promise<void>

/** Callable as `flush` itself (the original API), or destructured. */
export type ReadingProgressApi = Flush & {
  /** Saves a pending position now (resolves when sent or failed). */
  flush: Flush
  /** Russian note when saving keeps failing, else null. */
  error: string | null
  /** true: the position is another profile's place, not saved until released (the reader moved on). */
  hold: (on: boolean) => void
}

/**
 * `pos` is the position to save (set it only after the reader moved);
 * `initial` is the stored position the book opened at.
 */
export function useReadingProgress(itemId: number, pos: ReadingPosition | null, initial?: ReadingPosition | null): ReadingProgressApi {
  const latest = useRef<ReadingPosition | null>(null)
  const saved = useRef<string | null>(initial?.locator ? keyOf(initial) : null)
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const failures = useRef(0)
  const held = useRef(false)
  const [error, setError] = useState<string | null>(null)
  latest.current = pos

  const flush = useCallback(
    (keepalive = false): Promise<void> => {
      if (timer.current) {
        clearTimeout(timer.current)
        timer.current = null
      }
      const p = latest.current
      if (!p || itemId <= 0 || !p.locator || held.current) return Promise.resolve()
      const key = keyOf(p)
      if (key === saved.current) return Promise.resolve()
      const before = saved.current
      saved.current = key
      const f = Math.min(1, Math.max(0, p.fraction || 0))
      // queued until the server has it: read without the server, it goes out later
      const at = rememberProgress({ itemId, locator: p.locator, fraction: f })
      return saveReading(itemId, p.locator, f, keepalive).then(
        () => {
          settleProgress(itemId, at)
          failures.current = 0
          setError(null)
        },
        (e: unknown) => {
          if (settlesProgress(e)) settleProgress(itemId, at) // the book is gone; anything else stays queued
          // not sent: try again with the next move
          if (saved.current === key) saved.current = before
          console.warn('reading position not saved', e)
          failures.current++
          if (failures.current >= REPORT_AFTER) {
            setError(e instanceof ApiError && e.status === 0 ? t('reader.positionNotSavedOffline') : t('reader.positionNotSaved'))
          }
        },
      )
    },
    [itemId],
  )

  const locator = pos?.locator
  const fraction = pos?.fraction
  useEffect(() => {
    if (!locator) return
    if (timer.current) clearTimeout(timer.current)
    timer.current = setTimeout(() => void flush(false), SAVE_DEBOUNCE_MS)
  }, [locator, fraction, flush])

  useEffect(() => {
    const onHide = () => void flush(true)
    const onVisibility = () => {
      if (document.visibilityState === 'hidden') void flush(true)
    }
    window.addEventListener('pagehide', onHide)
    document.addEventListener('visibilitychange', onVisibility)
    return () => {
      window.removeEventListener('pagehide', onHide)
      document.removeEventListener('visibilitychange', onVisibility)
      void flush(true)
    }
  }, [flush])

  const hold = useCallback(
    (on: boolean) => {
      if (held.current === on) return
      held.current = on
      // released where the reader moved on to: saved as usual
      if (!on && latest.current) {
        if (timer.current) clearTimeout(timer.current)
        timer.current = setTimeout(() => void flush(false), SAVE_DEBOUNCE_MS)
      }
    },
    [flush],
  )

  return useMemo(() => Object.assign((k?: boolean) => flush(k), { flush, error, hold }), [flush, error, hold])
}
