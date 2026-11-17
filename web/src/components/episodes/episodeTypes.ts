// The shapes the episode guide hands a player of non-library sources
// (the build's own, or an addon's `episodeActions` entry) and takes back.
import type { ReactNode } from 'react'
import type { EpisodeSource, SourceChoice, SourceKind, TitleEpisode } from '../../api/types'

/** `choice`: the profile's source choice of the title (an online source plays at its quality). */
export type EpisodeCtx = { kind: 'series' | 'movie'; tmdbId: number; title: string; poster?: string; episodes: TitleEpisode[]; choice?: SourceChoice | null }

export interface EpisodePlay {
  play(ep: TitleEpisode, src: EpisodeSource): void
  /** «Другие источники…»: the player's own way of finding another source of that episode. */
  other(ep: TitleEpisode): void
  /** «Скачать серию»: for sources that can be brought into the library. */
  download?(ep: TitleEpisode, src: EpisodeSource): void
  /** The kinds a tap prefers when the profile saved no choice for the title (pickSource's `prefer`). */
  prefer?: readonly SourceKind[]
  /** The sheets and dialogs it renders. */
  node: ReactNode
}
