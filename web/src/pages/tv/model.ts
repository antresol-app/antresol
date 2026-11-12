// «ТВ»: the pure parts of the channels — the one numbered list and its
// filters, zapping, the guide grid's geometry and keyboard moves, and the
// live player's choice of audio and subtitles. No React, no requests:
// pages/tv/__tests__ check them directly.
import type { Channel, ChannelProgram, ChannelTrack } from '../../api/channels'
import type { PlaybackPrefs } from '../../api/types'

// ---------- the list and its filters ----------

/** «Все», «Избранное» or one theme (a channel's category). In the address: ?f=fav, ?f=c:<theme>. */
export type ChannelFilter = { kind: 'all' } | { kind: 'fav' } | { kind: 'theme'; theme: string }

export const ALL: ChannelFilter = { kind: 'all' }

export function parseFilter(s: string | null | undefined): ChannelFilter {
  if (s === 'fav') return { kind: 'fav' }
  if (s?.startsWith('c:') && s.length > 2) return { kind: 'theme', theme: s.slice(2) }
  return ALL
}

export function filterParam(f: ChannelFilter): string {
  return f.kind === 'fav' ? 'fav' : f.kind === 'theme' ? `c:${f.theme}` : ''
}

/** The list in number order (a TV's list never reorders: favourites stay where they are). */
export function byNumber<T extends { number: number }>(list: T[]): T[] {
  return [...list].sort((a, b) => a.number - b.number)
}

/** The themes in the order of their first channel. */
export function themesOf(list: { number: number; category: string }[]): string[] {
  const out: string[] = []
  for (const c of byNumber(list)) if (c.category && !out.includes(c.category)) out.push(c.category)
  return out
}

/** The channels a filter shows, in number order. */
export function applyFilter<T extends { number: number; category: string; favorite?: boolean }>(list: T[], f: ChannelFilter): T[] {
  const sorted = byNumber(list)
  if (f.kind === 'fav') return sorted.filter((c) => c.favorite)
  if (f.kind === 'theme') return sorted.filter((c) => c.category === f.theme)
  return sorted
}

/** A filter that no longer has anything to point at (the theme is gone) falls back to all. */
export function validFilter(list: { number: number; category: string }[], f: ChannelFilter): ChannelFilter {
  return f.kind === 'theme' && !list.some((c) => c.category === f.theme) ? ALL : f
}

/** A channel with its star flipped (optimistic). */
export function withFavorite<T extends { id: number; favorite?: boolean }>(list: T[], id: number, on: boolean): T[] {
  return list.map((c) => (c.id === id ? { ...c, favorite: on } : c))
}

// ---------- zapping ----------

/**
 * The channel `step` away from `id` in the list the player was opened from
 * (number order, wrapping round). A channel no longer in that list (it was
 * unstarred) zaps from where its number would stand.
 */
export function zapTarget<T extends { id: number; number: number }>(list: T[], id: number, step: number, number?: number): T | undefined {
  const sorted = byNumber(list)
  if (sorted.length === 0) return undefined
  let i = sorted.findIndex((c) => c.id === id)
  if (i < 0) {
    if (number === undefined) return sorted[step > 0 ? 0 : sorted.length - 1]
    // the first channel after the number (going up) or the last before it (going down)
    const after = sorted.findIndex((c) => c.number > number)
    if (step > 0) return sorted[after < 0 ? 0 : after]
    const before = (after < 0 ? sorted.length : after) - 1
    return sorted[before < 0 ? sorted.length - 1 : before]
  }
  if (sorted.length === 1) return sorted[0]?.id === id ? undefined : sorted[0]
  i = (((i + step) % sorted.length) + sorted.length) % sorted.length
  return sorted[i]
}

// ---------- time ----------

/** How far a programme is (0…1) at `now`. */
export function progressOf(p: { start: string; end: string }, now: number): number {
  const s = Date.parse(p.start)
  const e = Date.parse(p.end)
  if (!(e > s)) return 0
  return Math.min(1, Math.max(0, (now - s) / (e - s)))
}

/** Whole minutes left (at least 1 while it is on). */
export function minutesLeft(p: { end: string }, now: number): number {
  return Math.max(1, Math.ceil((Date.parse(p.end) - now) / 60_000))
}

export const isOnAir = (p: { start: string; end: string }, now: number) => Date.parse(p.start) <= now && now < Date.parse(p.end)

// ---------- the guide grid ----------

export const MIN = 60_000
export const SLOT = 30 * MIN

/** Where the guide starts: an hour ago, on a half hour. */
export function guideOrigin(now: number): number {
  return Math.floor((now - 60 * MIN) / SLOT) * SLOT
}

/** A programme's block in a window [origin, end): px from the window's start and px wide. */
export interface Block {
  left: number
  width: number
  past: boolean
  live: boolean
  /** Starts before the window (its left edge is cut). */
  cut: boolean
}

export function blockOf(p: { start: string; end: string }, origin: number, end: number, pxPerMin: number, now: number): Block | null {
  const s = Date.parse(p.start)
  const e = Date.parse(p.end)
  const a = Math.max(s, origin)
  const b = Math.min(e, end)
  if (!(b > a)) return null
  return {
    left: ((a - origin) / MIN) * pxPerMin,
    width: ((b - a) / MIN) * pxPerMin,
    past: e <= now,
    live: s <= now && now < e,
    cut: s < origin,
  }
}

/**
 * A block's label stays in sight: when the block began left of the visible
 * time (it scrolled under the channel column, or started before the
 * guide's window) its title and time move to the visible left edge — but
 * never past the point where `minLabel` px of it would no longer fit in
 * the block. `viewLeft` is the first visible px of the time track.
 */
export function labelInset(blockLeft: number, blockWidth: number, viewLeft: number, minLabel = 64): number {
  const want = viewLeft - blockLeft
  if (want <= 0) return 0
  return Math.max(0, Math.min(want, blockWidth - minLabel))
}

/** The ruler's ticks every half hour: x and whether it is a full hour. */
export function ticksOf(origin: number, end: number, pxPerMin: number): { t: number; x: number; hour: boolean }[] {
  const out = []
  for (let t = Math.ceil(origin / SLOT) * SLOT; t < end; t += SLOT) {
    out.push({ t, x: ((t - origin) / MIN) * pxPerMin, hour: new Date(t).getMinutes() === 0 })
  }
  return out
}

/** Programmes of two windows of the guide, one copy each, by start. */
export function mergePrograms(a: ChannelProgram[], b: ChannelProgram[]): ChannelProgram[] {
  const seen = new Map<string, ChannelProgram>()
  for (const p of [...a, ...b]) seen.set(p.id ?? `${p.itemId}@${p.start}`, p)
  return [...seen.values()].sort((x, y) => Date.parse(x.start) - Date.parse(y.start))
}

/**
 * The block the arrow keys move to in the grid: ←/→ along the row, ↑/↓ to
 * the neighbour row's programme at `anchor` (the time the viewer stands
 * at, kept while moving vertically), else the nearest one.
 */
export function gridMove(rows: { start: string; end: string }[][], row: number, col: number, key: 'left' | 'right' | 'up' | 'down', anchor: number): [number, number] | null {
  if (key === 'left' || key === 'right') {
    const c = col + (key === 'left' ? -1 : 1)
    return c >= 0 && c < (rows[row]?.length ?? 0) ? [row, c] : null
  }
  for (let r = row + (key === 'up' ? -1 : 1); r >= 0 && r < rows.length; r += key === 'up' ? -1 : 1) {
    const ps = rows[r]
    if (!ps?.length) continue
    let best = 0
    let dist = Infinity
    ps.forEach((p, i) => {
      const s = Date.parse(p.start)
      const e = Date.parse(p.end)
      const d = anchor < s ? s - anchor : anchor >= e ? anchor - e + 1 : 0
      if (d < dist) {
        dist = d
        best = i
      }
    })
    return [r, best]
  }
  return null
}

// ---------- audio and subtitles ----------
//
// The live stream is an HLS master with up to 8 audio and 6 subtitle
// renditions whose NAMEs and order come from what the channel will air (the
// server's per-run track set). Nothing here assumes a name, a count or an
// order: a rendition is found by its NAME, a fallback by its language.

/** What the viewer picked in this session (never saved on the server). */
export interface TrackSession {
  /** The audio rendition chosen (its NAME) and its language (the fallback on a channel without that name). */
  audioName?: string
  audioLang?: string
  /** The subtitle rendition chosen; null: turned off; absent: no choice (the profile decides). */
  subsName?: string | null
  subsLang?: string
}

/** A rendition of the master as the player lists it. */
export interface Rendition {
  name: string
  lang: string
}

/** A session read back from storage: only the fields this version knows (round 3's slots are dropped). */
export function cleanSession(v: unknown): TrackSession {
  if (!v || typeof v !== 'object') return {}
  const o = v as Record<string, unknown>
  const out: TrackSession = {}
  if (typeof o.audioName === 'string' && o.audioName) out.audioName = o.audioName
  if (typeof o.audioLang === 'string') out.audioLang = o.audioLang
  if (o.subsName === null || (typeof o.subsName === 'string' && o.subsName)) out.subsName = o.subsName as string | null
  if (typeof o.subsLang === 'string') out.subsLang = o.subsLang
  return out
}

const sameLang = (a: string | undefined, b: string | undefined) => !!a && !!b && (a === b || a.split('-')[0] === b.split('-')[0])

/**
 * The audio entry the programme plays for the viewer, on our channels and
 * external ones alike: the one they chose on this channel (by name); else
 * one in the chosen one's language; else the first of the device's
 * languages present (`system`: the browser's, navigator.languages); else
 * the first of the profile's audio languages present; else the original
 * (an entry named so, else the first one the stream lists).
 */
export function pickAudio(entries: ChannelTrack[] | undefined, prefs: Pick<PlaybackPrefs, 'audioLangs'> | null, session: TrackSession, system: readonly string[] = []): ChannelTrack | undefined {
  if (!entries?.length) return undefined
  if (session.audioName) {
    const hit = entries.find((x) => x.name === session.audioName) ?? entries.find((x) => sameLang(x.lang, session.audioLang))
    if (hit) return hit
  }
  for (const lang of [...system, ...(prefs?.audioLangs ?? [])]) {
    const e = entries.find((x) => sameLang(x.lang, lang))
    if (e) return e
  }
  return entries.find((x) => ORIGINAL.test(x.name) || ORIGINAL.test(x.label)) ?? entries[0]
}

const ORIGINAL = /оригинал|original/i

/** The device's languages, most wanted first (the browser's; none known: none). */
export function systemLanguages(): string[] {
  if (typeof navigator === 'undefined') return []
  const l = navigator.languages?.length ? navigator.languages : navigator.language ? [navigator.language] : []
  return l.filter(Boolean)
}

/**
 * The subtitle entry shown (null: none): the viewer's choice by name, else in
 * its language, else none; with no choice, the profile's subtitle mode.
 */
export function pickSubs(
  entries: ChannelTrack[] | undefined,
  prefs: Pick<PlaybackPrefs, 'audioLangs' | 'subMode' | 'subLangs'> | null,
  audio: ChannelTrack | undefined,
  session: TrackSession,
): ChannelTrack | null {
  if (session.subsName === null) return null
  if (session.subsName !== undefined) return entries?.find((x) => x.name === session.subsName) ?? entries?.find((x) => sameLang(x.lang, session.subsLang)) ?? null
  if (!entries?.length || !prefs || prefs.subMode === 'off') return null
  const langs: string[] = prefs.subLangs.length ? prefs.subLangs : prefs.audioLangs
  const first = () => {
    for (const lang of langs) {
      const e = entries.find((x) => sameLang(x.lang, lang))
      if (e) return e
    }
    return null
  }
  if (prefs.subMode === 'always') return first()
  // foreign: only when the voice is not one of the profile's languages
  if (audio && !(prefs.audioLangs as string[]).some((l) => sameLang(l, audio.lang))) return first()
  return null
}

/**
 * The renditions to select in the master (undefined: leave the player's own;
 * null subtitles: off). The chosen audio NAME sticks while the master has it,
 * even through a programme that lacks it (the server plays the programme's
 * track in that language there: nothing switches at the boundary); a channel
 * without that name plays the programme's entry (picked by language).
 * Subtitles: the programme's entry; else the chosen rendition while the master
 * has it (empty there, and back on its own when the next programme has it).
 * `renditions` empty: the player has not listed them yet.
 */
export function renditionsToPlay(
  audio: ChannelTrack | undefined,
  subs: ChannelTrack | null,
  subsKnown: boolean,
  renditions: { audio: Rendition[]; subs: Rendition[] },
  session: TrackSession,
): { audio: string | undefined; subs: string | null | undefined } {
  const has = (list: Rendition[], name: string | null | undefined) => !!name && (list.length === 0 || list.some((r) => r.name === name))
  const a = session.audioName && has(renditions.audio, session.audioName) ? session.audioName : audio?.name
  let s: string | null | undefined
  if (!subsKnown && session.subsName === undefined) s = undefined
  else if (session.subsName === null) s = null
  else s = subs?.name ?? (has(renditions.subs, session.subsName) ? session.subsName : null) ?? null
  return { audio: a, subs: s }
}

/** The viewer chose an audio entry: it sticks by name, with its language as the fallback. */
export const chooseAudioIn = (session: TrackSession, e: ChannelTrack): TrackSession => ({ ...session, audioName: e.name, audioLang: e.lang || undefined })

/** The viewer chose subtitles (null: off). */
export const chooseSubsIn = (session: TrackSession, e: ChannelTrack | null): TrackSession =>
  e ? { ...session, subsName: e.name, subsLang: e.lang || undefined } : { ...session, subsName: null, subsLang: undefined }

/** The index of the rendition whose HLS NAME is `name` (−1: none). */
export function renditionIndex(names: (string | undefined)[], name: string | undefined): number {
  if (!name) return -1
  return names.findIndex((n) => n === name)
}

/** The channels as the guide/list keeps them: a Channel or a ChannelEPG. */
export type Numbered = Pick<Channel, 'id' | 'number' | 'name' | 'category' | 'favorite'>

/** The digits of the longest channel number in a list (its number column is that wide). */
export function numDigits(list: readonly Pick<Channel, 'number'>[]): number {
  return list.reduce((d, c) => Math.max(d, String(c.number).length), 1)
}
