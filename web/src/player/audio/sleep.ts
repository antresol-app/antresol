// Sleep timer: after 15/30/45/60 minutes, or at the end of the current
// chapter, the sound fades out over 10 s, playback pauses and the volume
// comes back for the next play. A module store like the engine; the menu
// reads the time left through useSleepRemaining().
import { useSyncExternalStore } from 'react'
import { chapterAt } from './audiobook'
import { audio } from './engine'

export type SleepTimer = { kind: 'minutes'; endsAt: number } | { kind: 'chapter' }

/** The fade-out before the pause. */
export const FADE_MS = 10_000
const TICK_MS = 250

let timer: SleepTimer | null = null
/** «До конца главы»: the end of the chapter (ms in the book or track) and the item it was armed on. */
let chapterEnd = 0
let armedKey = ''
let book = false
/** Books: the chapter the timer counts down, where the book was at the last tick, and whether playback ran past the chapter's end. */
let armedIndex = -1
let lastMs = 0
let reached = false

/**
 * A move further than this past the chapter end between two ticks is the
 * listener's own jump (a seek, «Следующая глава», a chapter click), not
 * playback reaching the end: the timer then follows the new chapter.
 */
const NATURAL_MS = 6000
let interval: ReturnType<typeof setInterval> | null = null
/** Whole seconds left, as shown on the button (null: off). */
let shown: number | null = null
const subs = new Set<() => void>()

function emit() {
  for (const fn of subs) fn()
}

/** Where the engine is now: ms in the book (audiobooks) or in the track, and the item's key. */
function where(): { ms: number; key: string } {
  const s = audio.get()
  const it = s.queue[s.index]
  const ch = chapterAt(it, s.positionSec)
  if (ch) return { ms: ch.absMs, key: it!.key }
  return {
    ms: (it?.bookStartMs ?? 0) + s.positionSec * 1000,
    key: it?.key ?? '',
  }
}

function stop() {
  if (interval) clearInterval(interval)
  interval = null
  timer = null
  audio.setFade(1)
}

function update(left: number | null) {
  const next = left === null ? null : Math.ceil(left / 1000)
  if (next !== shown) {
    shown = next
    emit()
  }
}

/** «До конца главы» in a book: follow the chapter the listener moved to; note when playback itself crossed the end. */
function track() {
  if (timer?.kind !== 'chapter' || !book || reached) return
  const s = audio.get()
  const ch = chapterAt(s.queue[s.index], s.positionSec)
  if (!ch) return
  if (ch.index !== armedIndex) {
    const step = ch.absMs - lastMs
    if (lastMs < chapterEnd && ch.absMs >= chapterEnd && step >= 0 && step <= NATURAL_MS * (s.rate || 1)) reached = true
    else {
      armedIndex = ch.index
      chapterEnd = ch.chapter.endMs
    }
  }
  lastMs = ch.absMs
}

function tick() {
  if (!timer) return
  if (audio.get().queue.length === 0) return cancelSleep()
  track()
  const left = sleepRemaining(Date.now())
  if (left === null) return
  if (left <= 0) {
    audio.pause()
    stop()
    update(null)
    return
  }
  audio.setFade(left < FADE_MS ? left / FADE_MS : 1)
  update(left)
}

/** Starts (or replaces) the timer. */
export function armSleep(t: SleepTimer) {
  stop()
  timer = t
  if (t.kind === 'chapter') {
    const s = audio.get()
    const it = s.queue[s.index]
    const ch = chapterAt(it, s.positionSec)
    book = it?.bookStartMs !== undefined
    armedKey = it?.key ?? ''
    chapterEnd = ch ? ch.chapter.endMs : (it?.bookStartMs ?? 0) + (s.durationSec || it?.durationSec || 0) * 1000
    armedIndex = ch ? ch.index : -1
    lastMs = ch ? ch.absMs : 0
    reached = false
  }
  interval = setInterval(tick, TICK_MS)
  update(sleepRemaining(Date.now()))
}

/** Turns the timer off; playback goes on at full volume. */
export function cancelSleep() {
  const was = timer !== null
  stop()
  if (was || shown !== null) update(null)
}

/** Milliseconds until the pause (of listening time for «До конца главы»), null when off. */
export function sleepRemaining(now: number): number | null {
  if (!timer) return null
  if (timer.kind === 'minutes') return Math.max(0, timer.endsAt - now)
  const { ms, key } = where()
  // music: the next track means the end of «the chapter»
  if (!book && key !== armedKey) return 0
  if (reached) return 0
  const rate = audio.get().rate || 1
  return Math.max(0, (chapterEnd - ms) / rate)
}

export const currentSleep = (): SleepTimer | null => timer

/** Seconds left on the timer (re-renders once a second), null when off. */
export function useSleepRemaining(): number | null {
  return useSyncExternalStore(
    (fn) => {
      subs.add(fn)
      return () => {
        subs.delete(fn)
      }
    },
    () => shown,
    () => shown,
  )
}
