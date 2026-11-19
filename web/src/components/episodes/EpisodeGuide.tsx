// Episode-first guide of a series: seasons, every episode with the sources it
// can be watched from (disk, an addon's kinds). A tap plays the profile's
// remembered source, else the best one (of the kinds the players prefer, when
// they say). A library source plays here, any other kind through the addon
// whose episodeActions declare it (useEpisodeActions); a long press, a
// right-click or ▾ opens the source sheet. Renders nothing when the server has
// no guide for the title: the caller keeps its own UI then.
import { useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate } from 'react-router'
import { getSourceChoice, getTitleEpisodes, putSourceChoice } from '../../api/client'
import type { EpisodeSource, SourceChoice, TitleEpisode, TitleEpisodes } from '../../api/types'
import type { EpisodeCtx } from './episodeTypes'
import { clock } from '../../player/timeline'
import { locale, useLang, useT } from '../../i18n'
import { useSourceKinds } from '../../addons/slots'
import { bestSource, choiceOf, groupSources, pickSource } from '../../state/pickSource'
import { Badge } from '../../ui/Badge'
import { IconButton } from '../../ui/IconButton'
import { GroupedSection } from '../../ui/List'
import { SegmentedControl } from '../../ui/SegmentedControl'
import { useLongPress } from '../../ui/useLongPress'
import { episodeTitle, seasonLabel } from '../EpisodeList'
import { useEpisodePlayers } from './useEpisodePlayers'
import { SourceSheet } from './SourceSheet'
import styles from './episodes.module.css'

export interface EpisodeGuideProps {
  kind: 'series' | 'movie'
  tmdbId: number
  title: string
  poster?: string
  initialSeason?: number
  onPlayLibrary?: (itemId: number, fileId: number) => void
  /** «На устройство» of a library source (fileId 0: the item's file); no row without it. */
  onDownloadLibrary?: (itemId: number, fileId: number) => void
  /**
   * The guide once known, and again when the choice changes: null when the
   * server has none for the title (404, empty or failed), so the caller
   * can keep its own UI.
   */
  onLoaded?: (guide: { episodes: TitleEpisode[]; choice: SourceChoice | null } | null) => void
}

const MAX_TAGS = 4
const NO_EPISODES: TitleEpisode[] = []

const today = () => {
  const d = new Date()
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
}
const isUpcoming = (e: TitleEpisode) => e.sources.length === 0 && e.airDate > today()
const seasonOrder = (a: number, b: number) => (a === 0 ? 1 : b === 0 ? -1 : a - b)

const providerOf = (s: EpisodeSource) => s.provider || s.meta.providerId || ''

/** One badge per distinct kind, provider and quality. */
function tagsOf(sources: EpisodeSource[], kindLabel: (k: string) => string): string[] {
  const seen = new Map<string, string>()
  for (const s of sources) {
    const key = `${s.kind}|${providerOf(s)}|${s.quality ?? ''}`
    if (seen.has(key)) continue
    const label = s.kind === 'library' || s.kind === 'pinned' ? `${kindLabel(s.kind)} ${s.quality ?? ''}` : s.kind === 'online' ? `${providerOf(s) || kindLabel(s.kind)} ${s.quality ?? ''}` : providerOf(s) || kindLabel(s.kind)
    seen.set(key, label.trim())
  }
  return [...seen.values()]
}

function EpisodeRow({ ep, canOther, kindLabel, onTap, onSheet }: { ep: TitleEpisode; canOther: boolean; kindLabel: (kind: string) => string; onTap: () => void; onSheet: () => void }) {
  const t = useT()
  const press = useLongPress(onSheet)
  const name = episodeTitle({ title: ep.name, episodeNo: ep.number })
  const upcoming = isUpcoming(ep)
  // nothing to play and nowhere to look (a kid's row with no disk file): no sheet to open
  const inert = upcoming || (ep.sources.length === 0 && !canOther)
  const state = ep.watched ? t('episodes.watched') : ep.new ? t('episodes.new') : ep.positionSec ? t('episodes.progress', { time: clock(ep.positionSec) }) : ''
  const tags = tagsOf(ep.sources, kindLabel)
  const shown = tags.slice(0, MAX_TAGS)
  const date = upcoming ? new Intl.DateTimeFormat(locale(), { day: 'numeric', month: 'long' }).format(new Date(`${ep.airDate}T00:00:00`)) : ''
  const body = (
    <>
      <span className={styles.number}>{ep.number}</span>
      <span className={styles.text}>
        <span className={styles.label}>{name}</span>
        <span className={styles.hint}>
          {upcoming && t('episodes.upcoming', { date })}
          {!upcoming && state && <Badge tone={ep.new && !ep.watched ? 'accent' : 'default'}>{state}</Badge>}
          {shown.map((tag) => (
            <Badge key={tag}>{tag}</Badge>
          ))}
          {tags.length > MAX_TAGS && <Badge>{t('episodes.moreTags', { n: tags.length - MAX_TAGS })}</Badge>}
          {!upcoming && tags.length === 0 && t('episodes.noSources')}
        </span>
      </span>
    </>
  )
  return (
    <li className={styles.row}>
      {inert ? (
        <div className={`${styles.main} ${styles.static} ${styles.muted}`}>{body}</div>
      ) : (
        <button type="button" className={`${styles.main} ${ep.sources.length === 0 ? styles.muted : ''}`} onClick={onTap} {...press}>
          {body}
        </button>
      )}
      {!inert && <IconButton icon="chevronDown" size="xs" variant="plain" label={t('episodes.choose')} onClick={onSheet} />}
    </li>
  )
}

export function EpisodeGuide({ kind, tmdbId, title, poster, initialSeason, onPlayLibrary, onDownloadLibrary, onLoaded }: EpisodeGuideProps) {
  const t = useT()
  const lang = useLang()
  const navigate = useNavigate()
  const [data, setData] = useState<TitleEpisodes | null>(null)
  const [choice, setChoice] = useState<SourceChoice | null>(null)
  const [season, setSeason] = useState<number | null>(null)
  const [sheet, setSheet] = useState<TitleEpisode | null>(null)
  const loadedRef = useRef(onLoaded)
  loadedRef.current = onLoaded

  useEffect(() => {
    const ctl = new AbortController()
    setData(null)
    Promise.all([getTitleEpisodes(kind, tmdbId, ctl.signal), getSourceChoice(kind, tmdbId, ctl.signal).catch(() => null)]).then(
      ([d, c]) => {
        if (ctl.signal.aborted) return
        setData(d)
        setChoice(c)
        // told in the same batch as the guide's own state, not from an effect
        // after it: the page swaps its tile rail for the guide in one commit,
        // never a frame with both
        loadedRef.current?.(d && Array.isArray(d.episodes) && d.episodes.length > 0 ? { episodes: d.episodes, choice: c } : null)
      },
      () => !ctl.signal.aborted && loadedRef.current?.(null),
    )
    return () => ctl.abort()
  }, [kind, tmdbId])

  const episodes = data?.episodes ?? NO_EPISODES
  // the guide's context, stable while nothing in it changes (each addon's Host re-runs on a new one)
  const ctx = useMemo<EpisodeCtx>(() => ({ kind, tmdbId, title, poster, episodes, choice }), [kind, tmdbId, title, poster, episodes, choice])
  // the addons' players by source kind (each addon Host draws its own sheets)
  const players = useEpisodePlayers(ctx)
  const { playerFor, anyPlay, prefer, canDownload } = players
  const addonHosts = players.hosts
  // how the kinds sort and what they are called (the core's, and the addons' entries')
  const kinds = useSourceKinds()
  const seasons = useMemo(() => [...new Set(episodes.map((e) => e.season))].sort(seasonOrder), [episodes])
  // the Hosts stay first in the same fragment, so a guide that loads keeps their hook state
  if (!data || episodes.length === 0) return <>{addonHosts}</>

  const firstOpen = episodes.find((e) => !e.watched && !isUpcoming(e))
  const wanted = season ?? initialSeason ?? firstOpen?.season ?? seasons[0]!
  const current = seasons.includes(wanted) ? wanted : seasons[0]!
  const rows = episodes.filter((e) => e.season === current)

  const playLibrary = (s: EpisodeSource): boolean => {
    const itemId = s.meta.itemId ?? data.title.itemId
    if (!itemId) return false
    const fileId = s.meta.fileId ?? 0
    if (onPlayLibrary) onPlayLibrary(itemId, fileId)
    else navigate(`/watch/${itemId}${fileId ? `?file=${fileId}` : ''}`)
    return true
  }
  /** Plays src; false when this build cannot. */
  const run = (ep: TitleEpisode, s: EpisodeSource): boolean => {
    if (s.kind === 'library') return playLibrary(s)
    const p = playerFor(s.kind)
    if (!p) return false
    p.play(ep, s)
    return true
  }
  const tap = (ep: TitleEpisode) => {
    // only among what plays here: a kind with no live player never takes the tap
    const picked = pickSource(ep.sources.filter((s) => s.kind === 'library' || playerFor(s.kind)), choice, prefer, kinds.order)
    if (picked && run(ep, picked)) return
    const lib = bestSource(ep.sources.filter((s) => s.kind === 'library'))
    if (!(lib && playLibrary(lib))) setSheet(ep)
  }
  const choose = (ep: TitleEpisode, s: EpisodeSource) => {
    const c = choiceOf(s)
    setChoice(c)
    loadedRef.current?.({ episodes, choice: c })
    putSourceChoice(kind, tmdbId, c).catch(() => undefined)
    setSheet(null)
    run(ep, s)
  }

  const open = sheet ? (rows.concat(episodes).find((e) => e.season === sheet.season && e.number === sheet.number) ?? sheet) : null
  return (
    <>
      {addonHosts}
      <section className={styles.guide} aria-label={t('episodes.guide')}>
        {seasons.length > 1 && (
          <SegmentedControl label={t('common.kind.season')} segments={seasons.map((n) => ({ value: String(n), label: seasonLabel({ number: n }) }))} value={String(current)} onChange={(v) => setSeason(Number(v))} row align="start" />
        )}
        <GroupedSection title={seasons.length > 1 ? undefined : seasonLabel({ number: current })}>
          {rows.map((e) => (
            <EpisodeRow key={`${e.season}:${e.number}`} ep={e} canOther={Boolean(anyPlay)} kindLabel={kinds.label} onTap={() => tap(e)} onSheet={() => setSheet(e)} />
          ))}
        </GroupedSection>
        {open && (
          <SourceSheet
            episode={{ number: open.number, name: open.name }}
            groups={groupSources(open.sources, lang, kinds.order)}
            current={pickSource(open.sources, choice, prefer, kinds.order)}
            onPick={(s) => choose(open, s)}
            onOther={anyPlay ? () => (setSheet(null), anyPlay.other(open)) : undefined}
            onDownload={canDownload ? (s) => (setSheet(null), playerFor(s.kind)?.download?.(open, s)) : undefined}
            onDownloadLibrary={
              onDownloadLibrary
                ? (s) => {
                    const itemId = s.meta.itemId ?? data.title.itemId
                    if (!itemId) return
                    setSheet(null)
                    onDownloadLibrary(itemId, s.meta.fileId ?? 0)
                  }
                : undefined
            }
            onClose={() => setSheet(null)}
          />
        )}
      </section>
    </>
  )
}
