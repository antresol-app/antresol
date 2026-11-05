// Which audio and subtitle track a file starts with. In order: what the
// profile chose in this series (a voice-over by its title: «Кубик в кубе»,
// «TVShows»), this device's last choice (only while the profile keeps the
// household's defaults), the profile's languages («Звук и субтитры»), and
// the file's own default. The app has the same rules (TrackPrefs.swift).
import type { PlaybackPrefs, SubTrack, Track, TrackChoice, TrackPrefs } from '../api/types'
import { langCode, langKey } from './timeline'

const CYRILLIC = /[Ѐ-ӿ]/

/** Letters only Ukrainian has among the Cyrillic ones: і ї є ґ. */
const UKRAINIAN = /[\u0456\u0457\u0454\u0491]/i

/**
 * The track's language: its tag, else a language named in its title (a
 * word of 3+ letters: «it», «be», «he» are words, not tags), else the
 * script of a Cyrillic title (Ukrainian by its own letters, else Russian);
 * '' unknown.
 */
export function trackLang(t: { lang: string; title: string }): string {
  const code = langCode(t.lang)
  if (code) return code
  for (const w of t.title.toLowerCase().split(/[^\p{L}]+/u)) {
    const c = w.length >= 3 ? langCode(w) : ''
    if (c) return c
  }
  if (!CYRILLIC.test(t.title)) return ''
  return UKRAINIAN.test(t.title) ? 'uk' : 'ru'
}

const norm = (s: string) => s.toLowerCase().replace(/ё/g, 'е').replace(/[^\p{L}\p{N}]+/gu, '')

// Words that say what a track is technically, not whose voice it is.
const GENERIC = new Set([
  'ac3', 'eac3', 'dts', 'hd', 'ma', 'truehd', 'atmos', 'aac', 'flac', 'mp3', 'opus', 'pcm', 'lpcm', 'dd', 'ddp', 'dolby', 'digital',
  'plus', 'stereo', 'mono', 'surround', 'ch', 'x', 'kbps', 'kbit', 'khz', 'bit', 'audio', 'track', 'default', 'original', 'orig',
])

/** A title of codec and channel words only («AC3 5.1», «DTS», «Stereo»): it names no voice. */
export function genericTitle(title: string): boolean {
  const words = title.toLowerCase().split(/[^\p{L}\p{N}]+/u).filter(Boolean)
  return words.every((w) => GENERIC.has(w) || /^\d+$/.test(w))
}

/** Two track titles name the same voice: equal but for case, ё and punctuation, or one inside the other («MVO Кубик в Кубе (5.1)»); never a codec-only title. */
export function sameTitle(x: string, y: string): boolean {
  if (genericTitle(x) || genericTitle(y)) return false
  const a = norm(x)
  const b = norm(y)
  if (a === b) return true
  const [short, long] = a.length < b.length ? [a, b] : [b, a]
  return short.length >= 4 && long.includes(short)
}

/** The track whose title names the same voice as `title` (an exact match before one inside another), in an agreeing language. */
function byTitle<T extends { lang: string; title: string; kind?: string }>(list: T[], title: string, lang: string): T | undefined {
  // the studio's track before its cleaned version (preferCleaned swaps to that)
  list = [...list.filter((t) => t.kind !== 'cleaned'), ...list.filter((t) => t.kind === 'cleaned')]
  if (genericTitle(title)) return undefined
  const ok = (t: T) => langsAgree(trackLang(t), lang)
  const n = norm(title)
  return list.find((t) => ok(t) && norm(t.title) === n) ?? list.find((t) => ok(t) && sameTitle(t.title, title))
}

/** Languages agree unless both are known and differ. */
const langsAgree = (x: string, y: string) => !x || !y || x === y

/** The file's own default within `lang` (else its first track in it). */
function bestAudio(tracks: Track[], match: (t: Track) => boolean): Track | undefined {
  const in_ = tracks.filter(match)
  return in_.find((t) => t.default) ?? in_[0]
}

export interface AudioContext {
  /** The voice remembered for the series. */
  series?: TrackChoice | null
  prefs: PlaybackPrefs
  /** This device's last audio language (a langKey), from before profile defaults. */
  device?: string | null
}

/** The audio track to ask for, or undefined for the file's default. */
export function pickAudio(tracks: Track[], o: AudioContext): number | undefined {
  if (tracks.length === 0) return undefined
  const def = tracks.find((t) => t.default) ?? tracks[0]!
  let pick = chooseAudio(tracks, o) ?? def
  // «Выбирать очищенную озвучку», or the series voice was its cleaned version: that version of the chosen voice
  if (o.prefs.preferCleaned || o.series?.kind === 'cleaned') {
    pick = tracks.find((t) => t.kind === 'cleaned' && t.cleanedFrom === pick.index) ?? pick
  }
  return pick.index !== def.index ? pick.index : undefined
}

function chooseAudio(tracks: Track[], o: AudioContext): Track | undefined {
  if (o.series) {
    const want = trackLang(o.series)
    const hit = byTitle(tracks, o.series.title, want) ?? (want ? bestAudio(tracks, (t) => trackLang(t) === want) : undefined)
    if (hit) return hit
  }
  if (!o.prefs.custom && o.device) {
    const hit = bestAudio(tracks, (t) => langKey(t.lang) === o.device)
    if (hit) return hit
  }
  for (const l of o.prefs.audioLangs) {
    const hit = bestAudio(tracks, (t) => trackLang(t) === l)
    if (hit) return hit
  }
  return undefined
}

export interface SubtitleContext {
  /** The subtitles remembered for the series. */
  series?: TrackChoice | null
  /** Subtitles were turned off in the series. */
  seriesOff?: boolean
  prefs: PlaybackPrefs
  /** This device's last subtitle language (a langKey; '' = off), from before profile defaults. */
  device?: string | null
}

/** A full track over a forced one. */
const bestSub = (subs: SubTrack[]) => subs.find((s) => !s.forced) ?? subs[0]

/** The subtitle track to show, or -1 for none. `audio` is the track that plays. */
export function pickSubtitle(subs: SubTrack[], audio: Track | undefined, o: SubtitleContext): number {
  const usable = subs.filter((s) => !s.burnIn && s.url)
  const audioLang = audio ? trackLang(audio) : ''
  // forced subtitles translate the odd foreign line of the audio's language
  const forced = () => (usable.find((s) => s.forced && langsAgree(trackLang(s), audioLang)) ?? { index: -1 }).index
  const byLangs = (langs: string[]) => {
    for (const l of langs) {
      const hit = bestSub(usable.filter((s) => trackLang(s) === l))
      if (hit) return hit.index
    }
    return -1
  }
  if (o.seriesOff) return forced()
  if (o.series) {
    const want = trackLang(o.series)
    const hit = byTitle(usable, o.series.title, want)
    if (hit) return hit.index
    const byLang = want ? byLangs([want]) : -1
    if (byLang >= 0) return byLang
  }
  if (!o.prefs.custom && o.device != null) {
    if (o.device === '') return forced()
    const hit = bestSub(usable.filter((s) => langKey(s.lang) === o.device))
    if (hit) return hit.index
  }
  const mine = o.prefs.audioLangs
  const foreign = audioLang !== '' && !mine.includes(audioLang as never)
  if (o.prefs.subMode === 'always' || (o.prefs.subMode === 'foreign' && foreign)) {
    const hit = byLangs(o.prefs.subLangs.length ? o.prefs.subLangs : mine)
    if (hit >= 0) return hit
  }
  return forced()
}

/** The cue clock for file time `t` with `offsetMs` (positive: the subtitles come later). */
export const offsetCueTime = (t: number, offsetMs: number) => t - offsetMs / 1000

/** What is remembered of a track (the file's language tag and the title; a cleaned voice-over says so). */
export const choiceOf = (t: { lang: string; title: string; kind?: string }): TrackChoice =>
  t.kind === 'cleaned' ? { lang: t.lang, title: t.title, kind: 'cleaned' } : { lang: t.lang, title: t.title }

/** A playback-prefs answer as the player uses it; null when it is not one (an older server, a stub). */
export function asPrefs(x: unknown): PlaybackPrefs | null {
  const p = x as Partial<PlaybackPrefs> | null
  if (!p || typeof p !== 'object' || !Array.isArray(p.audioLangs) || typeof p.subMode !== 'string') return null
  return {
    audioLangs: p.audioLangs,
    subMode: p.subMode,
    subLangs: Array.isArray(p.subLangs) ? p.subLangs : [],
    subSize: p.subSize ?? 'm',
    subBackground: p.subBackground ?? 'translucent',
    custom: Boolean(p.custom),
    preferCleaned: Boolean(p.preferCleaned),
  }
}

/** A track-prefs answer; undefined when it is not one. */
export function asTrackPrefs(x: unknown): TrackPrefs | undefined {
  const t = x as Partial<TrackPrefs> | null
  const prefs = asPrefs(t?.prefs)
  if (!t || !prefs) return undefined
  return { prefs, series: t.series && typeof t.series === 'object' ? t.series : null, offsets: t.offsets && typeof t.offsets === 'object' ? t.offsets : {} }
}
