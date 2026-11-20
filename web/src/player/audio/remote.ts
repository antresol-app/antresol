// «Воспроизвести на…»: the listening handed to another player (an addon's
// output, such as the speakers of the home network). The engine keeps the
// queue and its rules (shuffle, the next track, an audiobook's position);
// the remote makes the sound and tells where it is. Its items carry
// absolute stream URLs with the profile's media key (`mk=`), since the
// other player fetches them without the browser's cookie, as AirPlay does.
import { mediaKey, withMediaKey } from '../airplay'
import type { QueueItem, RepeatMode } from './queue'

/** One item of the queue as a remote plays it. */
export interface RemoteItem {
  /** The queue key (unique within the queue). */
  key: string
  /** Absolute, with the media key. */
  url: string
  title: string
  artist: string
  album?: string
  durationSec: number
}

/** The listening as the remote should have it. */
export interface RemoteSnapshot {
  kind: 'music' | 'audiobook'
  items: RemoteItem[]
  /** The current item (items[index]). */
  index: number
  /** Seconds into the current item. */
  positionSec: number
  playing: boolean
  /** The engine's repeat: `all` wraps the queue, `one` repeats the current item. */
  repeat: RepeatMode
  /** 0..1 */
  volume: number
}

/** What the engine asks of a remote. Every call is a wish: the remote reports what happens through its handle. */
export interface AudioRemote {
  /**
   * The queue as it is now. `jump`: play items[index] from positionSec
   * (a new current item, a start); otherwise only the queue around the
   * current item changed (an added, removed or moved track, shuffle,
   * repeat) and the current item plays on where it is.
   */
  sync(s: RemoteSnapshot, jump: boolean): void
  play(): void
  pause(): void
  /** Seconds into the current item. */
  seek(sec: number): void
  /** 0..1 */
  volume?(v: number): void
  /** The engine ends the listening (stop, sign-out) or takes it back: stop the sound and let go. */
  stop(): void
}

/** What a remote reports (any subset). */
export interface RemoteUpdate {
  /** Where it plays now («Living room», «Living room + 1»): the players show it. */
  name?: string
  /** The queue key of the item it plays. */
  key?: string
  positionSec?: number
  durationSec?: number
  playing?: boolean
  buffering?: boolean
  /** Its volume, 0..1: the player's slider shows it while the remote plays. */
  volume?: number
}

/** The remote's side of the engine. */
export interface RemoteHandle {
  update(u: RemoteUpdate): void
  /** Says a line under the mini player (a speaker went away, a refused PIN). */
  notice(text: string): void
  /**
   * Gives the listening back: the browser plays on from where the remote
   * was (`resume`), or stays paused there. A no-op once let go.
   */
  release(opts?: { resume?: boolean }): void
  /** Still the engine's remote (not released, not replaced). */
  readonly active: boolean
}

/** A path the server serves for a remote (a saved copy on the device is not reachable from elsewhere). */
export function remotePath(it: QueueItem): string {
  // a track as it is on disk (no `accept`: the remote decodes any format
  // itself, and only a whole file can be sought in, by Range)
  if (it.trackId) return `/api/tracks/${it.trackId}/stream`
  if (it.fileId && it.src.startsWith('/api/audio/')) return `/api/audio/${it.fileId}`
  return it.src
}

/** The queue's items for a remote: absolute URLs with the profile's media key. */
export async function remoteItems(queue: QueueItem[], origin = window.location.origin): Promise<RemoteItem[]> {
  const key = await mediaKey()
  return queue.map((it) => ({
    key: it.key,
    url: withMediaKey(new URL(remotePath(it), origin).toString(), key),
    title: it.title,
    artist: it.artist,
    album: it.album,
    durationSec: it.durationSec,
  }))
}
