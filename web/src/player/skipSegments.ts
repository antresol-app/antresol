// Skip segments of a file (POST /api/play `segments`): the recap, the
// intro and the credits, in seconds on the file timeline (the clock of the
// subtitles, usePlayback's localTime). The player offers «Пропустить
// пересказ / вступление» while playback is inside one, and «Следующая
// серия» (filling over 10 s, then playing it) at the credits.
import type { PlaySegment } from '../api/types'

/** Shorter segments are not offered (a stray chapter mark). */
export const MIN_SEGMENT_SEC = 3
/** The pill shows this long after playback enters a segment, even with the controls hidden. */
export const PILL_ALONE_MS = 8000
/** «Следующая серия» fills over this long, then plays it. */
export const NEXT_COUNTDOWN_SEC = 10
/** Credits ending this long before the file does leave a scene after them: no autoplay. */
export const POST_CREDITS_SEC = 20
/** The note after an automatic skip («Вступление пропущено · Вернуть») stays this long. */
export const SKIPPED_NOTE_MS = 6000

const KINDS = new Set(['recap', 'intro', 'credits'])

/** The segments the player uses: known kinds, at least MIN_SEGMENT_SEC, in time order. */
export function usableSegments(segs: PlaySegment[] | undefined): PlaySegment[] {
  return (segs ?? [])
    .filter((s) => KINDS.has(s.kind) && Number.isFinite(s.startSec) && Number.isFinite(s.endSec) && s.endSec - s.startSec >= MIN_SEGMENT_SEC)
    .sort((a, b) => a.startSec - b.startSec)
}

/** The segment playback is in at t (its last quarter second counts as out: a seek to its end lands there). */
export function segmentAt(segs: PlaySegment[], t: number): PlaySegment | undefined {
  return segs.find((s) => t >= s.startSec && t < s.endSec - 0.25)
}

/** A scene follows the credits: they end well before the file does. */
export function postCreditsScene(credits: PlaySegment, durationSec: number): boolean {
  return durationSec > 0 && durationSec - credits.endSec > POST_CREDITS_SEC
}

/** The credits run to the end of the file: skipping them ends it. */
export function creditsToEnd(credits: PlaySegment, durationSec: number): boolean {
  return durationSec <= 0 || durationSec - credits.endSec < 1
}

/** How far «Следующая серия» has filled (0…1), from where playback entered the credits. */
export function countdown(enteredAt: number, t: number): number {
  return Math.min(1, Math.max(0, (t - enteredAt) / NEXT_COUNTDOWN_SEC))
}

/** A skip pill shows for PILL_ALONE_MS after the segment starts, then only with the controls. */
export function pillVisible(enteredMs: number, nowMs: number, controlsShown: boolean): boolean {
  return controlsShown || nowMs - enteredMs < PILL_ALONE_MS
}
