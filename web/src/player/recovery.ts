// Pure playback decisions, kept apart from React so they can be unit-tested:
// what to do when playback fails, and how to carry out a seek.

export type FailReason =
  /** The HLS session is gone (idle timeout, 404, network error, stall): same caps, same place. */
  | 'expired'
  /** The browser could not decode what it got: retry with transcode-only caps. */
  | 'media'
  /** A remote stream brought no data for minutes: restart, never transcode (that reads the same slow source). */
  | 'stalled'

export interface RecoveryState {
  /** Session restarts since playback last ran fine. */
  restarts: number
  fallbackUsed: boolean
}

export type Recovery = { action: 'restart' } | { action: 'fallback' } | { action: 'error' }

export const MAX_RESTARTS = 2

/**
 * An expired session is restarted at the same position (twice at most); a
 * decoding failure — or an expired session that keeps failing — gets one
 * retry with transcode-only caps; after that the viewer sees an error.
 */
export function recover(s: RecoveryState, reason: FailReason): Recovery {
  if ((reason === 'expired' || reason === 'stalled') && s.restarts < MAX_RESTARTS) return { action: 'restart' }
  if (reason === 'stalled') return { action: 'error' }
  if (!s.fallbackUsed) return { action: 'fallback' }
  return { action: 'error' }
}

/** Online: stale-link re-plays allowed within ONLINE_REPLAY_WINDOW_MS before the viewer is asked to pick another source. */
export const ONLINE_REPLAYS = 2
export const ONLINE_REPLAY_WINDOW_MS = 5 * 60_000

export type OnlineRecovery = { action: 'replay'; replays: number[] } | { action: 'error' }

/**
 * An online stream whose link went stale (a 410, `X-Vault-Online: expired`,
 * a long stall) is played again from the source, past the caches, at the
 * same position — at most twice in 5 minutes, and only once while it has
 * never `played` a frame. `replays` are the times of the earlier re-plays (ms).
 */
export function recoverOnline(replays: number[], now: number, played = true): OnlineRecovery {
  const recent = replays.filter((t) => now - t < ONLINE_REPLAY_WINDOW_MS)
  // a source that never showed a frame gets one fresh try, not two
  if (recent.length >= (played ? ONLINE_REPLAYS : 1)) return { action: 'error' }
  return { action: 'replay', replays: [...recent, now] }
}

/** Whether an answer of the online proxy says the link went stale. */
export function onlineExpired(status: number | undefined, header?: string | null): boolean {
  return status === 410 || (header ?? '').toLowerCase() === 'expired'
}

/**
 * What a failure of the media element means, by mode and error code. On a
 * remote stream (`slow`), a network error is the source, not the device.
 */
export function mediaErrorReason(mode: string, code: number | undefined, slow = false, started = true): FailReason {
  // online: a refused first request (the proxy's 410 on an MP4) reads as «source not supported»
  if (mode === 'online' && code === 4 && !started) return 'expired'
  if (code === 2 && slow) return 'stalled'
  // MEDIA_ERR_NETWORK on an HLS stream: the session went away (Safari, long pause).
  if (mode !== 'direct' && code === 2) return 'expired'
  return 'media'
}

/** What the server says about an HLS session (heartbeat). */
export interface HlsHealth {
  /** The session is gone (idle timeout, eviction, server restart). */
  gone: boolean
  running: boolean
  segments: number
}

export interface WaitContext {
  /** 'start': no frame yet; 'stall': it played, now it waits. */
  phase: 'start' | 'stall'
  mode: string
  /** A remote stream: data can take minutes. */
  slow: boolean
  /** The stream brought data (or ffmpeg wrote segments) since the last check. */
  progressed: boolean
  /** Checks in a row without any progress. */
  idleChecks: number
  /** Seconds since the session was requested. */
  elapsedSec: number
  /** The heartbeat's answer for HLS (undefined: direct play, or no answer). */
  hls?: HlsHealth
}

/** Checks without progress before a stream counts as stalled (× the timer, ~1.5 min). */
export const SLOW_IDLE_CHECKS = 3
/** The longest an online stream may take to its first frame (some sources are slow to start). */
export const ONLINE_START_LIMIT_SEC = 90
/** The longest a library HLS session may take to its first segment (slow 4K transcode). */
export const HLS_START_LIMIT_SEC = 180

/**
 * A start or stall timer fired: keep waiting, or why it failed. A timeout
 * alone never means the device cannot play the video, unless the stream
 * was there and the browser still did nothing with it.
 */
export function waitVerdict(c: WaitContext): 'wait' | FailReason {
  // Online: the proxy relays a source; no frames for this long is a stale
  // link (a slow source gets a while for its first bytes).
  if (c.mode === 'online') return c.phase === 'start' && c.elapsedSec < ONLINE_START_LIMIT_SEC ? 'wait' : 'expired'
  const hls = c.mode !== '' && c.mode !== 'direct'
  if (hls && c.hls?.gone) return 'expired'
  if (c.slow) {
    if (c.progressed) return 'wait'
    return c.idleChecks + 1 >= SLOW_IDLE_CHECKS ? 'stalled' : 'wait'
  }
  if (c.phase === 'start') {
    // Still working on the first segment (4K, slow disk): wait, within reason.
    if (hls && c.hls?.running && c.hls.segments === 0 && c.elapsedSec < HLS_START_LIMIT_SEC) return 'wait'
    return 'media'
  }
  // Mid-play: a transcode that is behind but still writing catches up.
  if (hls && c.hls?.running && c.progressed) return 'wait'
  return 'expired'
}

/**
 * The audio track for the next part of a multi-part movie: the language
 * of the track playing now, as a stream index of the next file (indices
 * differ between rips). Undefined = the next file's default track.
 */
export function audioForPart(
  current: { index: number; lang: string }[],
  currentIndex: number | undefined,
  next: { index: number; lang: string; default?: boolean }[],
  langKey: (l: string) => string,
): number | undefined {
  const playing = current.find((t) => t.index === currentIndex)
  if (!playing || !playing.lang) return undefined
  const want = langKey(playing.lang)
  const def = next.find((t) => t.default) ?? next[0]
  if (def && langKey(def.lang) === want) return undefined
  return next.find((t) => langKey(t.lang) === want)?.index
}

/** HLS ended this far before the part's end: the stream broke off, it did not finish. */
export const EARLY_END_SEC = 30

/** The stream ended at the same place again after a restart: the probed duration is wrong, this is the real end. */
export const REPEAT_END_SEC = 5

export function repeatedEarlyEnd(prev: { part: number; pos: number } | null, part: number, pos: number): boolean {
  return prev !== null && prev.part === part && Math.abs(prev.pos - pos) <= REPEAT_END_SEC
}

export function endedEarly(durationSec: number, positionSec: number): boolean {
  return durationSec > 0 && durationSec - positionSec > EARLY_END_SEC
}

export interface SeekContext {
  /** Part currently loaded. */
  part: number
  mode: string
  /** Seconds into the part where stream time 0 lies (HLS started mid-file). */
  offset: number
  /** End of what the HLS stream can seek to now, in stream time. */
  seekableEnd: number
}

export type SeekPlan =
  /** Set video.currentTime to `time` (stream time). */
  | { kind: 'local'; time: number }
  /** Load `part` again (or another part) starting at `at` seconds into it. */
  | { kind: 'reload'; part: number; at: number }

/** How to reach `offset` seconds into `part`. */
export function planSeek(c: SeekContext, part: number, offset: number): SeekPlan {
  if (part !== c.part) return { kind: 'reload', part, at: offset }
  // An online stream is the whole video from 0 (the source seeks, not a transcode).
  if (c.mode === 'direct' || c.mode === 'online') return { kind: 'local', time: offset }
  const rel = offset - c.offset
  // Inside what is already transcoded (one second of slack for the live edge).
  if (rel >= 0 && rel <= c.seekableEnd + 1) return { kind: 'local', time: rel }
  return { kind: 'reload', part, at: offset }
}
