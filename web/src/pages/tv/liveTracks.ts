// The live player's audio and subtitles. The stream is a master playlist
// with renditions named per slot (HLS NAME); the programme's `audio` /
// `subtitles` entries say which slots it really has and how to label them.
// The viewer's pick lives in this tab for the session (sessionStorage, here
// only as a convenience): it sticks across programmes and zaps, and it is
// never sent to the server — a channel remembers nothing (TV is TV).
import type Hls from 'hls.js'
import type { ChannelTrack } from '../../api/channels'
import { renditionIndex, type TrackSession } from './model'

const KEY = 'antresol.tv.tracks'

let session: TrackSession = read()

function read(): TrackSession {
  try {
    const v = JSON.parse(sessionStorage.getItem(KEY) ?? '{}') as TrackSession
    return v && typeof v === 'object' ? v : {}
  } catch {
    return {}
  }
}

export function trackSession(): TrackSession {
  return session
}

export function setTrackSession(next: TrackSession) {
  session = next
  try {
    sessionStorage.setItem(KEY, JSON.stringify(next))
  } catch {
    /* a private window: the choice lasts while the page does */
  }
}

/** Tests: forget the session's choice. */
export function resetTrackSession() {
  session = {}
  try {
    sessionStorage.removeItem(KEY)
  } catch {
    /* nothing kept */
  }
}

type NativeAudioTrack = { label: string; enabled: boolean; language?: string }
type NativeAudioList = { length: number; [i: number]: NativeAudioTrack; addEventListener?: (t: string, f: () => void) => void; removeEventListener?: (t: string, f: () => void) => void }
export type NativeVideo = HTMLVideoElement & { audioTracks?: NativeAudioList }

/** The renditions the player has: names of the audio and the subtitle tracks. */
export function renditionNames(hls: Hls | null, video: NativeVideo | null): { audio: string[]; subs: string[] } {
  if (hls) return { audio: hls.audioTracks.map((t) => t.name), subs: hls.subtitleTracks.map((t) => t.name) }
  const audio: string[] = []
  const list = video?.audioTracks
  if (list) for (let i = 0; i < list.length; i++) audio.push(list[i]?.label ?? '')
  const subs: string[] = []
  if (video) for (const t of Array.from(video.textTracks)) if (t.kind === 'subtitles' || t.kind === 'captions') subs.push(t.label)
  return { audio, subs }
}

/** Switch the player to the renditions named (undefined: leave as is; null subtitles: off). */
export function applyRenditions(hls: Hls | null, video: NativeVideo | null, audioName: string | undefined, subName: string | null | undefined) {
  if (hls) {
    if (audioName !== undefined) {
      const i = renditionIndex(hls.audioTracks.map((t) => t.name), audioName)
      if (i >= 0 && hls.audioTrack !== i) hls.audioTrack = i
    }
    if (subName !== undefined) {
      const i = subName === null ? -1 : renditionIndex(hls.subtitleTracks.map((t) => t.name), subName)
      if (hls.subtitleTrack !== i) hls.subtitleTrack = i
      // loaded into a hidden text track: the player draws the cues itself (above its bar)
      hls.subtitleDisplay = false
      for (const t of Array.from(video?.textTracks ?? [])) if ((t.kind === 'subtitles' || t.kind === 'captions') && t.label === subName && t.mode === 'disabled') t.mode = 'hidden'
    }
    return
  }
  if (!video) return
  const list = video.audioTracks
  if (audioName !== undefined && list) {
    let hit = -1
    for (let i = 0; i < list.length; i++) if (list[i]?.label === audioName) hit = i
    if (hit >= 0)
      for (let i = 0; i < list.length; i++) {
        const tr = list[i]
        if (tr) tr.enabled = i === hit
      }
  }
  if (subName !== undefined) {
    for (const t of Array.from(video.textTracks)) {
      if (t.kind !== 'subtitles' && t.kind !== 'captions') continue
      t.mode = subName !== null && t.label === subName ? 'hidden' : 'disabled'
    }
  }
}

/** Entries for the picker when the programme does not list them (an older server): the renditions themselves. */
export function entriesFromNames(names: string[]): ChannelTrack[] {
  return names.filter(Boolean).map((n) => ({ slot: n, name: n, label: n, lang: '' }))
}

/** The lines of the subtitle track named on screen now (the track is hidden: the player draws them). */
export function activeCueText(video: HTMLVideoElement | null, subName: string | null | undefined): string[] {
  if (!video || !subName) return []
  for (const t of Array.from(video.textTracks)) {
    if (t.label !== subName || t.mode === 'disabled' || !t.activeCues) continue
    const out: string[] = []
    for (const c of Array.from(t.activeCues)) {
      const text = (c as VTTCue).text?.replace(/<[^>]+>/g, '').trim()
      if (text) out.push(text)
    }
    return out
  }
  return []
}
