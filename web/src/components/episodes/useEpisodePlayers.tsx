// The players of an episode guide's sources, by kind: the build's own
// (useEpisodePlay: online, and library through the player page) and the
// addons' `episodeActions` (useEpisodeActions; each entry's Host calls its
// hook and draws its sheets). Shared by the guide and by a title page's
// next-episode «Смотреть», so both dispatch, prefer and route «Другие
// источники…» the same way.
import { useCallback, useMemo, useState, type ReactNode } from 'react'
import type { EpisodeSource, SourceKind } from '../../api/types'
import { useEpisodeActions } from '../../addons/slots'
import { useSession } from '../../state/session'
import { useEpisodePlay } from './EpisodePlay'
import type { EpisodeCtx, EpisodePlay } from './episodeTypes'

export interface EpisodePlayers {
  /** The build's own player (null for a kid, or a build without one). */
  core: EpisodePlay | null
  /**
   * The player of a source kind: the build's for library and online, an
   * addon's for its kinds; null for a kind no live player plays (its addon
   * is off or gone, its bundle failed, its use() threw).
   */
  playerFor: (kind: SourceKind) => EpisodePlay | null
  /** The sources of `sources` some player here can play. */
  playable: (sources: EpisodeSource[]) => EpisodeSource[]
  /**
   * «Другие источники…»: the build's online picker when the profile watches
   * online and the guide lists online sources, else the first addon player
   * (its own search), else the build's.
   */
  anyPlay: EpisodePlay | null
  /** The kinds a tap prefers, of every player (undefined: none says). */
  prefer: readonly SourceKind[] | undefined
  /** Some player can bring a source into the library («Скачать серию»). */
  canDownload: boolean
  /** The addons' Hosts: draw them always, in the same place (their hook state lives there). */
  hosts: ReactNode
}

/** `ctx` must be stable (memoised): each addon's Host re-runs on a new one. */
export function useEpisodePlayers(ctx: EpisodeCtx): EpisodePlayers {
  const core = useEpisodePlay(ctx)
  const entries = useEpisodeActions()
  const { user } = useSession()
  const [byKind, setByKind] = useState<Record<string, EpisodePlay>>({})
  const onAddonPlay = useCallback(
    (k: string, p: EpisodePlay | null) =>
      setByKind((m) => {
        if (p ? m[k] === p : !(k in m)) return m
        const next = { ...m }
        if (p) next[k] = p
        else delete next[k]
        return next
      }),
    [],
  )
  // the one online predicate: the profile watches online and the title has online sources
  const online = Boolean(user?.canWatchOnline) && ctx.episodes.some((e) => e.sources.some((s) => s.kind === 'online'))
  const addonPlays = Object.values(byKind)
  const preferred = [...(core?.prefer ?? []), ...addonPlays.flatMap((p) => p.prefer ?? [])]
  const hosts = useMemo(() => entries.map((h) => <h.Host key={h.key} ctx={ctx} onPlay={onAddonPlay} />), [entries, ctx, onAddonPlay])
  // library and online are the build's own (never an addon's); any other kind only its addon's
  const playerFor = (k: SourceKind): EpisodePlay | null => (k === 'online' || k === 'library' ? core : (byKind[k] ?? null))
  return {
    core,
    playerFor,
    playable: (sources) => sources.filter((s) => playerFor(s.kind)),
    anyPlay: (online ? core : null) ?? addonPlays[0] ?? core ?? null,
    prefer: preferred.length > 0 ? preferred : undefined,
    canDownload: Boolean(core?.download) || addonPlays.some((p) => p.download),
    hosts,
  }
}
