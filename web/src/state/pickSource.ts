// Which source plays by default, and how sources group in the picker.
import type { EpisodeSource, SourceChoice, SourceKind } from '../api/types'

/**
 * Where a source kind sorts: the library's own file first (0); every other
 * kind takes the `order` of its addon's episodeActions entry (useSourceKinds;
 * above 0, so the library always ranks first); a kind with none sorts last.
 */
export type KindOrder = Readonly<Record<string, number>>
export const CORE_KIND_ORDER: KindOrder = { library: 0 }
const UNKNOWN = Number.MAX_SAFE_INTEGER
// the library always ranks first: an addon's order counts only above its 0
const rankOf = (kind: string, order: KindOrder) => {
  const core = CORE_KIND_ORDER[kind]
  if (core !== undefined) return core
  const o = order[kind]
  return typeof o === 'number' && o > 0 ? o : UNKNOWN
}

/** "2160p"/"4K" > "1080p" > "720p" > "480p" > unknown. */
export function qualityRank(q?: string): number {
  const s = (q ?? '').toLowerCase()
  if (/2160|4k|uhd/.test(s)) return 4
  if (/1080/.test(s)) return 3
  if (/720/.test(s)) return 2
  if (/480/.test(s)) return 1
  return 0
}

const providerOf = (s: EpisodeSource): string => s.meta.providerId || s.provider || ''
const dubOf = (s: EpisodeSource): string => s.meta.voice || s.meta.studios?.[0] || s.meta.audio || ''
const langOf = (s: EpisodeSource): string => s.meta.lang ?? ''

/** What picking s means as a default. */
export function choiceOf(s: EpisodeSource): SourceChoice {
  const c: SourceChoice = { kind: s.kind, provider: providerOf(s), voice: dubOf(s), lang: langOf(s), quality: s.quality ?? '' }
  for (const k of ['provider', 'voice', 'lang', 'quality'] as const) if (!c[k]) delete c[k]
  return c
}

const compareBy = (order: KindOrder) => (a: EpisodeSource, b: EpisodeSource): number =>
  rankOf(a.kind, order) - rankOf(b.kind, order) || qualityRank(b.quality) - qualityRank(a.quality)

/** The kind first by `order` (the library first), then the better quality; input order breaks ties. */
export function bestSource(sources: EpisodeSource[], order: KindOrder = {}): EpisodeSource | null {
  const compare = compareBy(order)
  let best: EpisodeSource | null = null
  for (const s of sources) if (!best || compare(s, best) < 0) best = s
  return best
}

function bestQuality(sources: EpisodeSource[]): EpisodeSource | null {
  let best: EpisodeSource | null = null
  for (const s of sources) if (!best || qualityRank(s.quality) > qualityRank(best.quality)) best = s
  return best
}

/**
 * The nearest source to the profile's choice: exact, same dub, same language,
 * else the best. With no choice, `prefer` (the kinds the profile tends to
 * watch, e.g. its remembered way) wins over the default order, the library's
 * own file still first.
 */
export function pickSource(sources: EpisodeSource[], choice: SourceChoice | null, prefer?: readonly SourceKind[], order: KindOrder = {}): EpisodeSource | null {
  const best = (ss: EpisodeSource[]) => bestSource(ss, order)
  if (!choice) {
    const top = best(sources)
    if (!prefer?.length || top?.kind === 'library') return top
    return best(sources.filter((s) => prefer.includes(s.kind))) ?? top
  }
  if (choice.kind) {
    const exact = sources.filter(
      (s) => s.kind === choice.kind && providerOf(s) === (choice.provider ?? '') && dubOf(s) === (choice.voice ?? ''),
    )
    const atQuality = choice.quality ? exact.find((s) => s.quality === choice.quality) : undefined
    const hit = atQuality ?? bestQuality(exact)
    if (hit) return hit
  }
  if (choice.voice) {
    const hit = best(sources.filter((s) => dubOf(s) === choice.voice))
    if (hit) return hit
  }
  if (choice.lang) {
    const hit = best(sources.filter((s) => langOf(s) === choice.lang))
    if (hit) return hit
  }
  return best(sources)
}

export type SourceGroup = { lang: string; dubs: { dub: string; items: EpisodeSource[] }[] }

/**
 * Language, then dub. The group and the dub that hold the household's pinned
 * release come first (it is shown first to every profile); then groups sort
 * by the UI language, then by size, unknown language ('') last; dubs by
 * size; a dub's items follow bestSource order (`order` as there).
 */
export function groupSources(sources: EpisodeSource[], uiLang = '', order: KindOrder = {}): SourceGroup[] {
  const compare = compareBy(order)
  const byLang = new Map<string, Map<string, EpisodeSource[]>>()
  for (const s of sources) {
    const l = langOf(s)
    const dubs = byLang.get(l) ?? new Map<string, EpisodeSource[]>()
    byLang.set(l, dubs)
    const d = dubOf(s)
    dubs.set(d, [...(dubs.get(d) ?? []), s])
  }
  const count = (g: SourceGroup) => g.dubs.reduce((n, d) => n + d.items.length, 0)
  const groups: SourceGroup[] = [...byLang].map(([lang, dubs]) => ({
    lang,
    dubs: [...dubs].map(([dub, items]) => ({ dub, items: [...items].sort(compare) })),
  }))
  const hasPinned = (items: EpisodeSource[]) => items.some((s) => s.kind === 'pinned')
  const rank = (g: SourceGroup) => (g.dubs.some((d) => hasPinned(d.items)) ? -1 : g.lang === '' ? 2 : g.lang === uiLang ? 0 : 1)
  groups.sort((a, b) => rank(a) - rank(b) || count(b) - count(a))
  const dubRank = (d: SourceGroup['dubs'][number]) => (hasPinned(d.items) ? 0 : 1)
  for (const g of groups) g.dubs.sort((a, b) => dubRank(a) - dubRank(b) || b.items.length - a.items.length)
  return groups
}
