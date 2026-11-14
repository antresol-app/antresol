// Stub of the sources build's module of the same name (without .stub), which
// is not in the public tree: imports resolve here only when it is absent
// (tsconfig moduleSuffixes, vite.config.ts resolve.extensions). The core
// build cannot play online or torrent sources, so the hook returns null and
// the episode guide falls back to the library.
import type { ReactNode } from 'react'
import type { EpisodeSource, SourceChoice, SourceKind, TitleEpisode } from '../../api/types'

/** `choice`: the profile's source choice of the title (an online source plays at its quality). */
export type EpisodeCtx = { kind: 'series' | 'movie'; tmdbId: number; title: string; poster?: string; episodes: TitleEpisode[]; choice?: SourceChoice | null }

export interface EpisodePlay {
  play(ep: TitleEpisode, src: EpisodeSource): void
  /** «Другие источники…»: the online sheet or the release table for that episode. */
  other(ep: TitleEpisode): void
  /** Pinned sources only. */
  download?(ep: TitleEpisode, src: EpisodeSource): void
  /** The kinds a tap prefers when the profile saved no choice for the title (pickSource's `prefer`). */
  prefer?: readonly SourceKind[]
  /** The sheets and dialogs it renders. */
  node: ReactNode
}

/** A player for non-library sources, or null when this build cannot play them. */
export function useEpisodePlay(_ctx: EpisodeCtx): EpisodePlay | null {
  return null
}
