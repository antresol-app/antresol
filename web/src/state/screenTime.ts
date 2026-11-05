// «Экранное время» on the kid's side: the time left today (GET
// /api/me/screen-time) for the home screen's chip, and the player's guard:
// a toast five minutes before the end and, at the end (or when the server
// refuses playback), a friendly stop. The server enforces the limit on its
// own; this only makes the end gentle and expected.
import { useCallback, useEffect, useRef, useState } from 'react'
import { getMyScreenTime } from '../api/client'
import type { ScreenCategoryStatus, ScreenStatus } from '../api/types'

/** The warning comes this long before the end. */
export const WARN_SEC = 5 * 60
/** How often an open player asks again (a bonus, a limit changed). */
export const POLL_MS = 30_000

/** What the kid may still do now: null when nothing limits it today. */
export function videoLeft(st: ScreenStatus | null): ScreenCategoryStatus | null {
  if (!st || !st.limited) return null
  const v = st.video
  if (!v.allowed || v.remainingSec !== null) return v
  return null
}

// One ask for the many places that hide downloads (a song menu per row).
let shared: { at: number; p: Promise<ScreenStatus | null> } | null = null
const SHARED_MS = 60_000

/** For tests: forget the shared answer. */
export function resetScreenLimitsForTests() {
  shared = null
}

/**
 * What the signed-in kid's rules limit ({video, audio}); nothing for
 * another profile. A limited category neither downloads nor goes to the
 * device (the server refuses it as well).
 */
export function useScreenLimits(kid: boolean): { video: boolean; audio: boolean } {
  const [st, setSt] = useState<ScreenStatus | null>(null)
  useEffect(() => {
    if (!kid) {
      setSt(null)
      return
    }
    if (!shared || Date.now() - shared.at > SHARED_MS) shared = { at: Date.now(), p: getMyScreenTime().catch(() => null) }
    let live = true
    void shared.p.then((s) => live && setSt(s))
    return () => {
      live = false
    }
  }, [kid])
  return { video: Boolean(st?.videoLimited), audio: Boolean(st?.audioLimited) }
}

/** The status of the signed-in kid (null: not a kid, not loaded, or not limited). */
export function useScreenTime(enabled: boolean): ScreenStatus | null {
  const [st, setSt] = useState<ScreenStatus | null>(null)
  useEffect(() => {
    if (!enabled) {
      setSt(null)
      return
    }
    const ac = new AbortController()
    getMyScreenTime(ac.signal).then(setSt, () => {})
    return () => ac.abort()
  }, [enabled])
  return st
}

export interface ScreenGuard {
  /** Minutes left, while the warning toast shows (null: no toast). */
  warnMinutes: number | null
  /** The stop message: the time is over or outside the hours (null: plays on). */
  over: string | null
  /** Ask the server again now (a refused play: is it the limit?). */
  refresh: () => void
}

/** How long the warning toast stays. */
const TOAST_MS = 8000

/**
 * The player's guard: polls while enabled; warns once WARN_SEC before the
 * end (or at once when less is left) and stops at the end. A failed ask
 * at the end stops too (`fallback`): the server will refuse anyway.
 */
export function useScreenGuard(enabled: boolean, fallback: string): ScreenGuard {
  const [warnMinutes, setWarn] = useState<number | null>(null)
  const [over, setOver] = useState<string | null>(null)
  const warned = useRef(false)
  const timers = useRef<number[]>([])
  const fallbackRef = useRef(fallback)
  fallbackRef.current = fallback

  const clear = () => {
    for (const id of timers.current) window.clearTimeout(id)
    timers.current = []
  }

  const apply = useCallback((st: ScreenStatus, atEnd: boolean) => {
    clear()
    if (!st.limited) {
      setOver(null)
      return
    }
    const v = st.video
    if (!v.allowed) {
      setOver(v.message || fallbackRef.current)
      return
    }
    setOver(null)
    if (v.remainingSec === null) return
    const rem = Math.max(0, v.remainingSec)
    const warn = () => {
      if (warned.current) return
      warned.current = true
      setWarn(Math.max(1, Math.ceil(rem > WARN_SEC ? WARN_SEC / 60 : rem / 60)))
      // not one of `timers`: the next ask must not keep the toast up
      window.setTimeout(() => setWarn(null), TOAST_MS)
    }
    if (rem <= WARN_SEC) warn()
    else timers.current.push(window.setTimeout(warn, (rem - WARN_SEC) * 1000))
    // at the end: ask again (a bonus may have come), else stop
    if (!atEnd || rem > 0) timers.current.push(window.setTimeout(() => endRef.current(), rem * 1000 + 500))
    else setOver(fallbackRef.current)
  }, [])

  const ask = useCallback(
    (atEnd: boolean) => {
      getMyScreenTime().then(
        (st) => apply(st, atEnd),
        () => atEnd && setOver(fallbackRef.current),
      )
    },
    [apply],
  )
  const endRef = useRef(() => ask(true))
  endRef.current = () => ask(true)

  useEffect(() => {
    if (!enabled) return
    warned.current = false
    ask(false)
    const id = window.setInterval(() => ask(false), POLL_MS)
    return () => {
      window.clearInterval(id)
      clear()
    }
  }, [enabled, ask])

  const refresh = useCallback(() => {
    if (enabled) ask(false)
  }, [enabled, ask])
  return { warnMinutes, over, refresh }
}
