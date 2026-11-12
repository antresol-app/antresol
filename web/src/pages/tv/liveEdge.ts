// Live playback in the channel player: where the live edge is, how far the
// picture is behind it, where a resume after a pause starts and which of
// the player's controls show. Pure functions over seconds of the media
// timeline, so the rules are the same for our channels and external ones
// (the player reads the numbers from hls.js or the browser's own HLS).
//
// - A channel may be paused. The picture can come back where it stopped
//   only while the stream still holds it: our channels keep about a minute
//   (OWN_WINDOW_S); an external stream keeps its seekable range. A pause
//   longer than that resumes at the live edge.
// - More than BEHIND_LIVE_S behind the edge, «В эфир» shows (and jumps to
//   the edge); at the edge «Прямой эфир» shows.
// - A stream that is not live (a provider's recording with an end) has no
//   edge: no «В эфир», and a pause resumes where it stopped.

/** Behind the edge by more than this, the picture is not «live» any more. */
export const BEHIND_LIVE_S = 10
/** What our channels' live playlists hold behind the edge (internal/channels: 10 × 6 s segments). */
export const OWN_WINDOW_S = 60

export interface LiveClock {
  /** a live stream (a playlist without an end) */
  live: boolean
  /** the playing position */
  position: number
  /** the live edge: where playback sits when it is live */
  edge: number
  /** the oldest position the stream still holds (seekable start) */
  start: number
}

export const NOT_LIVE: LiveClock = { live: false, position: 0, edge: 0, start: 0 }

/**
 * The live edge. hls.js knows where it keeps a live stream (its sync
 * position, a few segments before the playlist's end); the browser's own
 * HLS says only where the stream ends, so the edge is that end less the
 * distance playback started at (`startOffset`).
 */
export function liveEdge(p: { syncPosition?: number | null; seekableEnd: number; startOffset: number }): number {
  const sync = p.syncPosition
  if (typeof sync === 'number' && Number.isFinite(sync) && sync > 0) return Math.min(sync, p.seekableEnd)
  return Math.max(0, p.seekableEnd - Math.max(0, p.startOffset))
}

/** How many seconds the picture is behind the edge (0 at it, or for a stream that is not live). */
export function behindLive(c: LiveClock): number {
  if (!c.live) return 0
  return Math.max(0, c.edge - c.position)
}

/** «В эфир» shows: live, and behind the edge by more than BEHIND_LIVE_S. */
export function showGoLive(c: LiveClock): boolean {
  return c.live && behindLive(c) > BEHIND_LIVE_S
}

/** How far back the stream holds the picture: a minute on our channels, the seekable range on an external one. */
export function liveWindow(own: boolean, c: LiveClock): number {
  return own ? OWN_WINDOW_S : Math.max(0, c.edge - c.start)
}

/**
 * Where to resume after a pause: the live edge when the stopped picture is
 * gone (the pause plus how far behind it already was outlasts the window,
 * or the position fell out of the stream), else null — go on from there.
 */
export function resumeTarget(p: { own: boolean; pausedMs: number; behindAtPause: number; clock: LiveClock }): number | null {
  const c = p.clock
  if (!c.live) return null
  const gone = p.pausedMs / 1000 + Math.max(0, p.behindAtPause) >= liveWindow(p.own, c)
  if (gone || c.position < c.start) return c.edge
  return null
}

/**
 * What shows of the player's chrome. The bars (header, info, tools) come
 * with a tap or the pointer moving and go after a few quiet seconds, stay
 * while paused, a menu or the list is open, or the channel is not playing;
 * on an upright phone they are always there (under the picture). The
 * centre play/pause sits on the picture and follows the same rule — also
 * on an upright phone, where it still hides — but only once the channel
 * plays (the spinner or the error has the centre before that).
 */
export function chromeShown(s: { poked: boolean; paused: boolean; playing: boolean; upright: boolean; info: boolean; panel: boolean; menuOpen: boolean }): { bars: boolean; center: boolean } {
  const awake = s.poked || s.paused || s.info || s.menuOpen
  return {
    bars: s.upright || awake || s.panel || !s.playing,
    center: s.playing && awake && !(s.panel && !s.upright),
  }
}

/** What the clock is read from: the video element and, when it plays the stream, hls.js. */
export interface ClockSource {
  video: { currentTime: number; duration: number; seekable?: { length: number; start(i: number): number; end(i: number): number } | null }
  hls?: { liveSyncPosition?: number | null; latestLevelDetails?: { live?: boolean } | null } | null
  /** browser HLS: how far before the stream's end playback started (see liveEdge) */
  startOffset: number
}

/**
 * The clock now. Live is hls.js's playlist without an end, or (the
 * browser's own HLS) a video whose duration is infinite.
 */
export function readClock(s: ClockSource): LiveClock {
  const v = s.video
  const r = v.seekable
  const live = s.hls ? Boolean(s.hls.latestLevelDetails?.live) : v.duration === Infinity
  if (!live || !r || r.length === 0) return { ...NOT_LIVE, position: v.currentTime }
  const start = r.start(0)
  const end = r.end(r.length - 1)
  return { live: true, position: v.currentTime, start, edge: liveEdge({ syncPosition: s.hls?.liveSyncPosition, seekableEnd: end, startOffset: s.startOffset }) }
}
