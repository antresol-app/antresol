// What «Календарь» and the home's «Скоро выйдет» share: an episode (or a
// movie's release) of a tracked title as a poster card of the library's
// Card, so both draw the same covers, links and corners as every shelf.
import type { Card, TrackingItem } from '../api/types'
import type { TFunction } from '../i18n'

/** YYYY-MM-DD of a local date (the server's days are the profile's calendar days). */
export const ymd = (d: Date) => `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`

/** The local date of a YYYY-MM-DD. */
export const parseYmd = (s: string) => new Date(Number(s.slice(0, 4)), Number(s.slice(5, 7)) - 1, Number(s.slice(8, 10)))

/** «С2 Э3» of an episode, «Фильм» of a movie. */
export function episodeLabel(t: TFunction, it: TrackingItem): string {
  if (it.title.kind === 'movie') return t('tracking.calendar.movie')
  return t('tracking.calendar.episode', {
    season: it.season,
    episode: it.episode,
  })
}

/**
 * The card of a calendar item: a library title opens its page, one off the
 * library its TMDB title page (at that episode); `subtitle` is what the
 * card says under the title; «доступно» is its chip when it has a source.
 */
export function calendarCard(t: TFunction, it: TrackingItem, subtitle = episodeLabel(t, it)): Card {
  const inLibrary = Boolean(it.title.itemId)
  const series = it.title.kind === 'series'
  return {
    id: it.title.itemId ?? 0,
    kind: it.title.kind,
    title: it.title.name,
    subtitle,
    poster: it.title.poster,
    backdrop: '',
    logo: '',
    progress: 0,
    onServer: inLibrary,
    tmdbKind: it.title.kind,
    tmdbId: it.title.tmdbId,
    ...(series && !inLibrary ? { season: it.season, episode: it.episode } : {}),
    ...(it.firstSeen ? { badges: [t('tracking.calendar.available')] } : {}),
  }
}

/**
 * One day's items as cards, one per title (a row never repeats a cover):
 * several episodes of a title that day say «С1 Э1–3» (one season) or the
 * first one and how many more; the card is «доступно» when any of them is.
 */
export function dayCards(t: TFunction, items: TrackingItem[]): Card[] {
  const groups = new Map<string, TrackingItem[]>()
  for (const it of items) {
    const k = `${it.title.kind}-${it.title.tmdbId}`
    const g = groups.get(k)
    if (g) g.push(it)
    else groups.set(k, [it])
  }
  return [...groups.values()].map((g) => {
    const first = g[0]!
    const avail = g.find((it) => it.firstSeen) ?? first
    if (g.length === 1) return calendarCard(t, first)
    const last = g[g.length - 1]!
    const oneSeason = g.every((it) => it.season === first.season)
    const label = oneSeason
      ? t('tracking.calendar.episodes', {
          season: first.season,
          from: first.episode,
          to: last.episode,
        })
      : `${episodeLabel(t, first)} +${g.length - 1}`
    return calendarCard(t, { ...first, firstSeen: avail.firstSeen }, label)
  })
}
