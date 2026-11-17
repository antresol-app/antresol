// Stub of the sources build's module of the same name (without .stub), which
// is not in the public tree: imports resolve here only when it is absent
// (tsconfig moduleSuffixes, vite.config.ts resolve.extensions). The core
// build cannot play online sources, so the hook returns null and the
// episode guide falls back to the library (an addon's kinds play through
// its episodeActions).
import type { EpisodeCtx, EpisodePlay } from './episodeTypes'

export type { EpisodeCtx, EpisodePlay } from './episodeTypes'

/** A player for non-library sources, or null when this build cannot play them. */
export function useEpisodePlay(_ctx: EpisodeCtx): EpisodePlay | null {
  return null
}
