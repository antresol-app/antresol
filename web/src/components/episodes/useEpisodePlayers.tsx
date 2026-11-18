// The players of an episode guide's sources, by kind: the addons'
// `episodeActions` (useEpisodeActions; each entry's Host calls its hook and
// draws its sheets). The library is the core's own (the guide and the row
// play it through the player page). Shared by the guide and by a title
// page's next-episode «Смотреть», so both dispatch, prefer and route
// «Другие источники…» the same way.
import { useCallback, useMemo, useState, type ReactNode } from 'react'
import type { EpisodeSource, SourceKind } from '../../api/types'
import { useEpisodeActions, useSourceKinds } from '../../addons/slots'
import type { EpisodeCtx, EpisodePlay } from './episodeTypes'

export interface EpisodePlayers {
  /**
   * The player of a source kind: an addon's for its kinds; null for the
   * library (the core's) and for a kind no live player plays (its addon is
   * off or gone, its bundle failed, its use() threw).
   */
  playerFor: (kind: SourceKind) => EpisodePlay | null
  /** The sources of `sources` some player here can play. */
  playable: (sources: EpisodeSource[]) => EpisodeSource[]
  /**
   * «Другие источники…»: the player of the kind the guide lists that ranks
   * best on the kinds' scale (its own search), else the best ranked player.
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
  const entries = useEpisodeActions()
  const { order } = useSourceKinds()
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
  const addonPlays = Object.values(byKind)
  const preferred = addonPlays.flatMap((p) => p.prefer ?? [])
  // the kinds that have a player, best ranked first (an unranked kind last, in the order they came)
  const ranked = Object.keys(byKind).sort((a, b) => (order[a] ?? Number.MAX_SAFE_INTEGER) - (order[b] ?? Number.MAX_SAFE_INTEGER))
  const listed = new Set(ctx.episodes.flatMap((e) => e.sources.map((s) => s.kind)))
  const anyKind = ranked.find((k) => listed.has(k)) ?? ranked[0]
  const hosts = useMemo(() => entries.map((h) => <h.Host key={h.key} ctx={ctx} onPlay={onAddonPlay} />), [entries, ctx, onAddonPlay])
  // the library is the core's own (never an addon's); any other kind only its addon's
  const playerFor = (k: SourceKind): EpisodePlay | null => (k === 'library' ? null : (byKind[k] ?? null))
  return {
    playerFor,
    playable: (sources) => sources.filter((s) => playerFor(s.kind)),
    anyPlay: anyKind ? (byKind[anyKind] ?? null) : null,
    prefer: preferred.length > 0 ? preferred : undefined,
    canDownload: addonPlays.some((p) => p.download),
    hosts,
  }
}
