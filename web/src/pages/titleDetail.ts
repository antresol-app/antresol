// A title of the core title page (GET /api/title/{kind}/{tmdbId}): a TMDB
// title that may not be in the library, shaped like a library item.
import type { ItemDetail as Detail, TitleInfo } from '../api/types'

/** A TMDB title that is not on the server, shaped like a library item (`id` 0). */
export function fromTitle(ti: TitleInfo, kid: boolean): Detail {
  return {
    id: 0,
    kind: ti.kind,
    title: ti.title,
    subtitle: '',
    year: ti.year || undefined,
    poster: ti.poster,
    backdrop: ti.backdrop,
    logo: '',
    progress: 0,
    onServer: false,
    originalTitle: ti.originalTitle,
    overview: ti.overview,
    genres: ti.genres,
    runtime: ti.runtime,
    rating: ti.rating,
    imdbId: ti.imdbId,
    cast: ti.cast ?? [],
    seasons: [],
    files: [],
    inWatchlist: false,
    actions: { play: false, toDevice: false, watchNow: !kid, toServer: !kid },
    // what an addon's item action or panel names the title by
    tmdbId: ti.tmdbId,
  }
}
