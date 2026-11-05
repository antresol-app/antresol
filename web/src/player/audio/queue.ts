// The play queue: item shape, shuffle and the next/previous rules. Pure
// functions; the engine owns the state.
import { trackStreamUrl } from '../../api/client'
import type { BookChapter, Song } from '../../api/types'
import { acceptedCodecs } from './caps'
import type { TrackGain } from './normalize'

export interface QueueItem {
  /** Unique within the queue. */
  key: string
  trackId?: number
  fileId?: number
  title: string
  artist: string
  album?: string
  albumId?: number
  cover: string
  durationSec: number
  src: string
  /** Start this far into the file (audiobook chapters, resume). */
  startSec?: number
  /** Music only (Task 15, «Сейчас играет»): artist link, «Lossless» badge, lyrics. */
  artistId?: number
  lossless?: boolean
  hasLyrics?: boolean
  /** Audiobooks: where this file starts in the book (ms) and every chapter of the book (absolute ms). */
  bookStartMs?: number
  chapters?: BookChapter[]
  /** Music: its ReplayGain (normalize.ts). */
  gain?: TrackGain
}

export type RepeatMode = 'off' | 'all' | 'one'

/** Album or playlist songs as queue items streamed by track id. */
export function songItems(songs: Song[]): QueueItem[] {
  const accept = acceptedCodecs()
  return songs.map((s) => ({
    key: `t${s.id}`,
    trackId: s.id,
    fileId: s.fileId,
    title: s.title,
    artist: s.artist,
    album: s.album,
    albumId: s.albumId,
    cover: s.cover,
    durationSec: s.durationMs / 1000,
    src: trackStreamUrl(s.id, { accept }),
    artistId: s.artistId,
    lossless: s.lossless,
    hasLyrics: s.hasLyrics,
    gain: { trackDb: s.gainTrackDb, albumDb: s.gainAlbumDb, trackPeak: s.peakTrack, albumPeak: s.peakAlbum },
  }))
}

/** Fisher–Yates on a copy. */
function permute<T>(xs: T[]): T[] {
  const a = [...xs]
  for (let i = a.length - 1; i > 0; i--) {
    const j = Math.floor(Math.random() * (i + 1))
    const t = a[i]!
    a[i] = a[j]!
    a[j] = t
  }
  return a
}

/** The current item first, the rest in random order. */
export function shuffled(items: QueueItem[], current: number): QueueItem[] {
  if (current < 0 || current >= items.length) return permute(items)
  return [items[current]!, ...permute(items.filter((_, i) => i !== current))]
}

/**
 * Where the queue goes after `index`. `auto` is the end of a track (repeat
 * one replays it); a press of «Следующий трек» always moves on. null: stop.
 */
export function nextIndex(len: number, index: number, repeat: RepeatMode, auto: boolean): number | null {
  if (len === 0) return null
  if (auto && repeat === 'one') return index
  if (index + 1 < len) return index + 1
  return repeat === 'off' ? null : 0
}

/** «Предыдущий трек» at the start of a track. null: restart the current one. */
export function previousIndex(len: number, index: number, repeat: RepeatMode): number | null {
  if (index > 0) return index - 1
  return repeat === 'all' && len > 1 ? len - 1 : null
}

/** Gives copies of already queued keys a fresh suffix. */
export function uniqueKeys(queue: QueueItem[], items: QueueItem[]): QueueItem[] {
  const used = new Set(queue.map((q) => q.key))
  return items.map((it) => {
    let key = it.key
    for (let n = 2; used.has(key); n++) key = `${it.key}~${n}`
    used.add(key)
    return key === it.key ? it : { ...it, key }
  })
}

/** src with the transcode start `t` (seconds) set. */
export function withStart(src: string, t: number): string {
  const u = new URL(src, 'http://vault.invalid')
  u.searchParams.set('t', String(Math.round(t * 1000) / 1000))
  return u.pathname + u.search
}
