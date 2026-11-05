// AirPlay of a video from Safari. The receiver (an Apple TV) fetches the
// stream itself, without the browser's cookie, so once the video goes to
// it the player reloads the session at the same place with a media key in
// the URL (`mk=`, POST /api/auth/media-key; the server adds it to every
// URI of an HLS playlist it serves with one). Subtitles are drawn by the
// page and do not travel: the player says so while one is on.
import { getMediaKey } from '../api/client'
import { session } from '../state/session'

export type WirelessVideo = HTMLVideoElement & {
  webkitShowPlaybackTargetPicker?: () => void
  webkitCurrentPlaybackTargetIsWireless?: boolean
}

/** Safari with AirPlay (the event class exists); elsewhere there is no button. */
export const airplaySupported = () => typeof window !== 'undefined' && 'WebKitPlaybackTargetAvailabilityEvent' in window

/** A key with less than this left is replaced (a film and its pauses). */
export const KEY_MARGIN_MS = 3600_000

/** `url` (relative or absolute) with `mk=<key>`; an old `mk` is replaced, the rest kept. */
export function withMediaKey(url: string, key: string): string {
  const hashAt = url.indexOf('#')
  const hash = hashAt >= 0 ? url.slice(hashAt) : ''
  const base = hashAt >= 0 ? url.slice(0, hashAt) : url
  const q = base.indexOf('?')
  const path = q >= 0 ? base.slice(0, q) : base
  const params = (q >= 0 ? base.slice(q + 1) : '').split('&').filter((p) => p !== '' && !p.startsWith('mk='))
  params.push(`mk=${encodeURIComponent(key)}`)
  return `${path}?${params.join('&')}${hash}`
}

/** Whether a URL already carries a media key. */
export const hasMediaKey = (url: string) => /[?&]mk=/.test(url)

let cached: { key: string; expires: number; user: number | undefined } | null = null
let inFlight: Promise<string> | null = null

/**
 * A media key of the signed-in profile with at least an hour left; callers
 * at the same time share one request. (A key ends with the session that
 * asked for it: another profile signing in here asks for its own.)
 */
export function mediaKey(now = Date.now()): Promise<string> {
  const user = session.get().user?.id
  if (cached && cached.user === user && cached.expires - now >= KEY_MARGIN_MS) return Promise.resolve(cached.key)
  inFlight ??= getMediaKey()
    .then((r) => {
      const expires = Date.parse(r.expiresAt)
      cached = { key: r.key, expires: Number.isFinite(expires) ? expires : now + KEY_MARGIN_MS, user }
      return r.key
    })
    .finally(() => {
      inFlight = null
    })
  return inFlight
}

/**
 * The next key is asked for again: sign-out, another profile, and a
 * receiver that was refused (a revoked session's key is still in date).
 */
export function forgetMediaKey() {
  cached = null
  inFlight = null
}

// signed out or another profile: its key is not this one's
session.subscribe(() => {
  if (cached && session.get().user?.id !== cached.user) forgetMediaKey()
})
