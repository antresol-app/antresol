import type { EpisodeSource, TitleEpisode, TitleEpisodes } from '../../../api/types'

export const lib = (over: Partial<EpisodeSource> = {}): EpisodeSource => ({
  kind: 'library', ref: 'lib:1', quality: '1080p', labels: [], firstSeen: '2026-01-01', meta: { itemId: 7, fileId: 70 }, ...over,
})
export const online = (provider: string, voice: string, over: Partial<EpisodeSource> = {}): EpisodeSource => ({
  kind: 'online', ref: `on:${provider}:${voice}`, provider, quality: '720p', labels: [], firstSeen: '2026-01-01',
  meta: { providerId: provider, voice, lang: 'ru' }, ...over,
})
export const ep = (number: number, over: Partial<TitleEpisode> = {}): TitleEpisode => ({
  season: 1, number, name: `Name ${number}`, airDate: '2020-01-01', watched: false, new: false, sources: [], ...over,
})
export const guide = (episodes: TitleEpisode[]): TitleEpisodes => ({
  title: { kind: 'series', tmdbId: 5, name: 'Show', poster: '', status: 'returning', itemId: 7 }, episodes,
})
