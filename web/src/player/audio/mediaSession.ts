// Lock screen, Control Center, headphone and keyboard media keys for the
// audio engine. Every call is a no-op where the Media Session API is missing.
import { imageUrl } from '../../api/client'
import type { QueueItem } from './queue'

export interface SessionControls {
  play(): void
  pause(): void
  previous(): void
  next(): void
  seek(sec: number): void
  skip(deltaSec: number): void
}

const ACTIONS = ['play', 'pause', 'previoustrack', 'nexttrack', 'seekto', 'seekbackward', 'seekforward'] as const

/** Seconds for the ±skip actions when the system does not say. */
const SKIP_SEC = 15

function ms(): MediaSession | null {
  return typeof navigator !== 'undefined' && 'mediaSession' in navigator && navigator.mediaSession ? navigator.mediaSession : null
}

/** The metadata the engine set last: clear() never removes someone else's. */
let own: MediaMetadata | null = null

/** `over` replaces the artist/album lines (an audiobook's chapter and author). */
export function setMetadata(item: QueueItem, over?: { artist: string; album: string }) {
  const s = ms()
  if (!s || typeof MediaMetadata === 'undefined') return
  const art = imageUrl(item.cover)
  try {
    s.metadata = own = new MediaMetadata({
      title: item.title,
      artist: over?.artist ?? item.artist,
      album: over?.album ?? item.album ?? '',
      artwork: art ? [{ src: art, sizes: '512x512' }] : [],
    })
  } catch {
    /* ignore */
  }
}

export interface SkipOptions {
  /** Seconds of «назад» / «вперёд» when the system does not say. */
  back: number
  forward: number
  /** false: no previous/next track (iOS then shows the ±skip buttons). */
  tracks: boolean
}

export function setHandlers(c: SessionControls, o: SkipOptions = { back: SKIP_SEC, forward: SKIP_SEC, tracks: true }) {
  const s = ms()
  if (!s) return
  const handlers: Record<(typeof ACTIONS)[number], MediaSessionActionHandler | null> = {
    play: () => c.play(),
    pause: () => c.pause(),
    previoustrack: o.tracks ? () => c.previous() : null,
    nexttrack: o.tracks ? () => c.next() : null,
    seekto: (d) => d.seekTime !== undefined && c.seek(d.seekTime),
    seekbackward: (d) => c.skip(-(d.seekOffset || o.back)),
    seekforward: (d) => c.skip(d.seekOffset || o.forward),
  }
  for (const a of ACTIONS) {
    try {
      s.setActionHandler(a, handlers[a])
    } catch {
      /* action not supported by this browser */
    }
  }
}

export function setPlaying(playing: boolean) {
  const s = ms()
  if (s) s.playbackState = playing ? 'playing' : 'paused'
}

export function setPosition(position: number, duration: number, rate: number) {
  const s = ms()
  if (!s || typeof s.setPositionState !== 'function') return
  if (!Number.isFinite(duration) || duration <= 0) return
  try {
    s.setPositionState({ duration, position: Math.min(Math.max(position, 0), duration), playbackRate: rate })
  } catch {
    /* ignore */
  }
}

export function clear() {
  const s = ms()
  if (!s) return
  try {
    if (s.metadata === own) s.metadata = null
    own = null
    s.playbackState = 'none'
    for (const a of ACTIONS) s.setActionHandler(a, null)
    s.setPositionState?.()
  } catch {
    /* ignore */
  }
}
