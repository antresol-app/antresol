// «Выравнивание громкости» (ReplayGain) of music: the gain each track plays
// at so that tracks from different albums sound about as loud. Pure math
// plus the choice, remembered on this device (`vault.music.normalize`) as
// the audiobook speed is.
//
// The server gives each track its ReplayGain (tags, else measured): gains
// in dB to the ReplayGain reference (-18 LUFS) and linear peaks. Played
// here PREAMP_DB louder (about -14 LUFS, as streaming services level),
// never so loud that the peak would clip (gain × peak ≤ 1).
import type { QueueItem } from './queue'

export type NormalizeMode = 'off' | 'track' | 'album' | 'auto'
export const NORMALIZE_MODES: NormalizeMode[] = ['auto', 'album', 'track', 'off']

/** dB above the ReplayGain reference (-18 LUFS → about -14 LUFS). */
export const PREAMP_DB = 4
/** The gain never goes beyond these (dB), whatever a tag says. */
const MIN_DB = -24
const MAX_DB = 12

export interface TrackGain {
  trackDb?: number | null
  albumDb?: number | null
  trackPeak?: number | null
  albumPeak?: number | null
}

const KEY = 'vault.music.normalize'
const listeners = new Set<() => void>()

export function loadNormalize(): NormalizeMode {
  try {
    const v = localStorage.getItem(KEY)
    if (v === 'off' || v === 'track' || v === 'album' || v === 'auto') return v
  } catch {
    /* blocked storage: the default */
  }
  return 'auto'
}

export function saveNormalize(mode: NormalizeMode) {
  try {
    localStorage.setItem(KEY, mode)
  } catch {
    /* not remembered */
  }
  for (const fn of listeners) fn()
}

export function subscribeNormalize(fn: () => void): () => void {
  listeners.add(fn)
  return () => {
    listeners.delete(fn)
  }
}

const num = (v: number | null | undefined): v is number => typeof v === 'number' && Number.isFinite(v)

/**
 * «Авто»: the album gain while an album plays in its order (not shuffled,
 * and the track next to it in the queue is of the same album), so its
 * quiet and loud songs keep their difference; else the track gain.
 */
export function resolveMode(mode: NormalizeMode, queue: QueueItem[], index: number, shuffle: boolean): 'off' | 'track' | 'album' {
  if (mode !== 'auto') return mode
  const it = queue[index]
  if (shuffle || !it?.albumId) return 'track'
  const same = (j: number) => queue[j]?.albumId === it.albumId
  return same(index - 1) || same(index + 1) ? 'album' : 'track'
}

/**
 * The linear gain of a track in a mode: the album gain (the track's when the
 * album's is unknown) or the track's, plus PREAMP_DB, limited by its peak
 * (gain × peak ≤ 1; no boost at all when the peak is unknown); 1 when off
 * or unknown.
 */
export function linearGain(g: TrackGain | undefined, mode: 'off' | 'track' | 'album'): number {
  if (!g || mode === 'off') return 1
  let db: number | null = null
  let peak: number | null = null
  if (mode === 'album' && num(g.albumDb)) {
    db = g.albumDb
    peak = num(g.albumPeak) ? g.albumPeak : null
  } else if (num(g.trackDb)) {
    db = g.trackDb
    peak = num(g.trackPeak) ? g.trackPeak : null
  }
  if (db === null) return 1
  const clamped = Math.min(Math.max(db + PREAMP_DB, MIN_DB), MAX_DB)
  const lin = Math.pow(10, clamped / 20)
  // an unknown peak: never louder than the file (it might clip)
  return peak !== null && peak > 0 ? Math.min(lin, 1 / peak) : Math.min(lin, 1)
}

/** The gain of queue[index] under the setting. */
export function itemGain(mode: NormalizeMode, queue: QueueItem[], index: number, shuffle: boolean): number {
  const it = queue[index]
  if (!it || it.trackId === undefined) return 1 // audiobooks are never levelled
  return linearGain(it.gain, resolveMode(mode, queue, index, shuffle))
}
