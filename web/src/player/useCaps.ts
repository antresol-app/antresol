// What this browser can decode, reported to POST /api/play so the server picks
// direct play, remux or transcode. A codec counts only when the <video> element
// (direct play, native HLS) and MediaSource (hls.js) both accept it — a codec
// that one of them refuses would give a black screen or silence.
import { useState } from 'react'
import type { ClientCaps } from '../api/types'

const VIDEO: [string, string][] = [
  ['h264', 'video/mp4; codecs="avc1.640028"'],
  ['hevc', 'video/mp4; codecs="hvc1.1.6.L120.90"'],
  ['av1', 'video/mp4; codecs="av01.0.08M.08"'],
  ['vp9', 'video/webm; codecs="vp9"'],
]

const AUDIO: [string, string][] = [
  ['aac', 'audio/mp4; codecs="mp4a.40.2"'],
  ['mp3', 'audio/mpeg'],
  ['ac3', 'audio/mp4; codecs="ac-3"'],
  ['eac3', 'audio/mp4; codecs="ec-3"'],
  ['opus', 'audio/mp4; codecs="opus"'],
  ['flac', 'audio/mp4; codecs="flac"'],
]

/** Containers a <video src> can open. Matroska is never claimed: browsers play it only partly. */
const CONTAINERS: [string, string][] = [
  ['mp4', 'video/mp4'],
  ['webm', 'video/webm'],
  ['mov', 'video/quicktime'],
]

/** Plays everywhere: the transcoder's output (H.264 + AAC in HLS). */
export const SAFE_CAPS: ClientCaps = { videoCodecs: ['h264'], audioCodecs: ['aac'], containers: [] }

interface MSELike {
  isTypeSupported(type: string): boolean
}

function mediaSource(): MSELike | undefined {
  if (typeof window === 'undefined') return undefined
  const w = window as unknown as { MediaSource?: MSELike; ManagedMediaSource?: MSELike }
  const ms = w.MediaSource ?? w.ManagedMediaSource
  return ms && typeof ms.isTypeSupported === 'function' ? ms : undefined
}

export function detectCaps(): ClientCaps {
  if (typeof document === 'undefined') return { ...SAFE_CAPS }
  const video = document.createElement('video')
  const ms = mediaSource()
  const canPlay = (t: string) => {
    try {
      return video.canPlayType(t) !== ''
    } catch {
      return false
    }
  }
  const mse = (t: string) => {
    if (!ms) return true
    try {
      return ms.isTypeSupported(t)
    } catch {
      return false
    }
  }
  const pick = (list: [string, string][]) => list.filter(([, t]) => canPlay(t) && mse(t)).map(([n]) => n)
  return {
    videoCodecs: pick(VIDEO),
    audioCodecs: pick(AUDIO),
    containers: CONTAINERS.filter(([, t]) => canPlay(t)).map(([n]) => n),
  }
}

/**
 * Caps for a retry after playback failed: drops the given codecs and every
 * container (forces HLS). Never leaves a list empty — H.264/AAC always remain.
 */
export function withoutCodecs(caps: ClientCaps, codecs: string[]): ClientCaps {
  const drop = new Set(codecs)
  const v = caps.videoCodecs.filter((c) => !drop.has(c))
  const a = caps.audioCodecs.filter((c) => !drop.has(c))
  return {
    ...caps,
    videoCodecs: v.length ? v : [...SAFE_CAPS.videoCodecs],
    audioCodecs: a.length ? a : [...SAFE_CAPS.audioCodecs],
    containers: [],
  }
}

/** The browser's capabilities, detected once per mount. */
export function useCaps(): ClientCaps {
  const [caps] = useState(detectCaps)
  return caps
}
