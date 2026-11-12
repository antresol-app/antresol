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

/** What the viewer picked in this session (never saved on the server): a slot, or null for subtitles off. */
export interface TrackSession {
  audio?: string
  subs?: string | null
  /** The chosen renditions' HLS NAMEs: a programme without the slot keeps playing it (the server fills it). */
  audioName?: string
  subsName?: string
}

/**
 * The renditions to play. The viewer stays on the slot they chose even when
 * a programme lacks it (its audio is the programme's fallback, its
 * subtitles are empty), so nothing switches at a programme boundary.
 */
export function renditionsToPlay(
  audio: ChannelTrack[] | undefined,
  subs: ChannelTrack[] | undefined,
  audioSlot: string | undefined,
  subSlot: string | null,
  session: TrackSession,
): { audio: string | undefined; subs: string | null | undefined } {
  const own = (list: ChannelTrack[] | undefined, slot: string | null | undefined) => (slot ? list?.find((x) => x.slot === slot)?.name : undefined)
  const a = own(audio, audioSlot) ?? (session.audio && session.audio === audioSlot ? session.audioName : undefined) ?? playingAudio(audio, audioSlot)?.name
  let s: string | null | undefined
  if (subs === undefined && session.subs === undefined) s = undefined
  else if (subSlot === null) s = null
  else s = own(subs, subSlot) ?? (session.subs === subSlot ? session.subsName : undefined) ?? null
  return { audio: a, subs: s }
}

/** The audio slot to play: the viewer's slot; else the profile's first language present; else «ru»; else the first. */
export function pickAudioSlot(entries: ChannelTrack[] | undefined, prefs: Pick<PlaybackPrefs, 'audioLangs'> | null, session: TrackSession): string | undefined {
  if (session.audio) return session.audio
  if (!entries?.length) return undefined
  for (const lang of prefs?.audioLangs ?? []) {
    const e = entries.find((x) => x.lang === lang)
    if (e) return e.slot
  }
  return (entries.find((x) => x.slot === 'ru') ?? entries[0])?.slot
}

/** The audio entry that really plays: the chosen slot, else the programme's fallback («ru»). */
export function playingAudio(entries: ChannelTrack[] | undefined, slot: string | undefined): ChannelTrack | undefined {
  if (!entries?.length) return undefined
  return entries.find((x) => x.slot === slot) ?? entries.find((x) => x.slot === 'ru') ?? entries[0]
}

/** The subtitle slot (null: off) — the viewer's choice; else by the profile's subtitle mode. */
export function pickSubSlot(
  entries: ChannelTrack[] | undefined,
  prefs: Pick<PlaybackPrefs, 'audioLangs' | 'subMode' | 'subLangs'> | null,
  audio: ChannelTrack | undefined,
  session: TrackSession,
): string | null {
  if (session.subs !== undefined) return session.subs
  if (!entries?.length || !prefs || prefs.subMode === 'off') return null
  const langs: string[] = prefs.subLangs.length ? prefs.subLangs : prefs.audioLangs
  const first = () => {
    for (const lang of langs) {
      const e = entries.find((x) => x.lang === lang)
      if (e) return e.slot
    }
    return null
  }
  if (prefs.subMode === 'always') return first()
  // foreign: only when the voice is not one of the profile's languages
  if (audio && !(prefs.audioLangs as string[]).includes(audio.lang)) return first()
  return null
}

/** The index of the rendition whose HLS NAME is the entry's name (−1: none). */
export function renditionIndex(names: (string | undefined)[], name: string | undefined): number {
  if (!name) return -1
  return names.findIndex((n) => n === name)
}

/** The other way round: the slot of the rendition a player switched to by itself. */
export function slotOfName(entries: ChannelTrack[] | undefined, name: string | undefined): string | undefined {
  return entries?.find((x) => x.name === name)?.slot
}

/** The channels as the guide/list keeps them: a Channel or a ChannelEPG. */
export type Numbered = Pick<Channel, 'id' | 'number' | 'name' | 'category' | 'favorite'>
