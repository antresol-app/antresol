// The action row of the core title page (/title/:kind/:tmdbId): a TMDB
// title that may not be in the library. «Смотреть ▾» offers the addons'
// `watchWay` item actions (way id `<addonId>:<label>`, useTitleWays:
// `useAvailable` applied, `?way=` run once), the one the profile chose last
// first; the other item actions are the row's round buttons (ActionBar). A
// series whose episode guide knows the next episode plays it. A kid gets
// «Попросить» and nothing of an addon.
import { useEffect, useLayoutEffect, useMemo, useRef, type ReactNode } from 'react'
import { useSearchParams } from 'react-router'
import type { ItemDetail as Detail, KidRequestState, SourceChoice, TitleEpisode } from '../api/types'
import { useAddonsSettledFor } from '../addons/registry'
import { useSourceKinds } from '../addons/slots'
import { useAddonWays } from '../addons/watchWays'
import { ActionBar } from '../components/ActionBar'
import { episodeCode } from '../components/EpisodeList'
import { MarkButton } from '../components/MarkButton'
import { RequestButton } from '../components/RequestButton'
import { WatchButton, type WatchWay } from '../components/WatchButton'
import type { EpisodeCtx } from '../components/episodes/episodeTypes'
import { useEpisodePlayers } from '../components/episodes/useEpisodePlayers'
import { useT } from '../i18n'
import { pickSource } from '../state/pickSource'
import { useSession } from '../state/session'
import { pickWay, useWatchWay, wayId, writeWatchWay } from '../state/watchWay'
import { Notice } from '../ui/Notice'
import styles from './ItemDetail.module.css'

/** The page's episode guide (EpisodeGuide's onLoaded). */
export type TitleGuide = { episodes: TitleEpisode[]; choice: SourceChoice | null } | null | undefined

const NO_EPISODES: TitleEpisode[] = []

/**
 * The episode «Смотреть» plays when the guide is known: the first unwatched
 * one that has a source, of the regular seasons first (specials only when
 * nothing else is left).
 */
export function nextGuideEpisode(episodes: TitleEpisode[]): TitleEpisode | undefined {
  const open = (e: TitleEpisode) => !e.watched && e.sources.length > 0
  return episodes.find((e) => e.season > 0 && open(e)) ?? episodes.find(open)
}

/**
 * The addons' `watchWay` item actions of `item` as ways of «Смотреть ▾» (id
 * `<addonId>:<label>`, labels resolved, `useAvailable` applied: a way whose
 * hook still asks is not offered yet, `pending`); `node` draws the hooks'
 * Hosts, what the ways open and what they say. None for a kid.
 */
export function useAddonWatchWays(item: Detail): { ways: WatchWay[]; node: ReactNode; pending: number } {
  const addon = useAddonWays(item)
  const said = addon.said
  const node = (
    <>
      {addon.node}
      {said?.failed && (
        <p className={styles.acquireError} role="alert">
          {said.text}
        </p>
      )}
      {said && !said.failed && <Notice tone="accent">{said.text}</Notice>}
    </>
  )
  return { ways: addon.ways, node, pending: addon.pending }
}

/**
 * The one way list of an item's and a title's «Смотреть ▾»: the addons'
 * ways. `?way=<addonId>:<label>` runs that way once, when it is offered (an addon
 * still asking is waited for), and leaves the URL (replace); a way the
 * profile's settled addons do not offer just leaves it. `menu`: keep the ▾
 * drawn, since a way may still come (or came and went): once drawn it stays
 * for the title, so the row never changes its shape. `base`: the ways the
 * button holds before these (a library item's «С сервера»).
 */
export function useTitleWays(item: Detail, base = 0): { ways: WatchWay[]; node: ReactNode; menu: boolean } {
  const addon = useAddonWatchWays(item)
  const ways = addon.ways
  const titleKey = `${item.kind}:${item.tmdbId ?? 0}:${item.id}`
  const kept = useRef({ key: titleKey, on: false })
  if (kept.current.key !== titleKey) kept.current = { key: titleKey, on: false }
  const menu = kept.current.on || addon.pending > 0 || base + ways.length > 1
  useLayoutEffect(() => {
    if (menu) kept.current.on = true
  })

  const { user } = useSession()
  const settled = useAddonsSettledFor()
  const known = Boolean(user) && (user?.addons !== true || settled === user?.id)
  const [params, setParams] = useSearchParams()
  const want = params.get('way')
  const done = useRef<string | null>(null)
  useEffect(() => {
    if (!want || done.current === want) return
    const way = ways.find((w) => w.id === wayId(want))
    if (!way && (addon.pending > 0 || !known)) return
    done.current = want
    setParams(
      (p) => {
        const next = new URLSearchParams(p)
        next.delete('way')
        return next
      },
      { replace: true },
    )
    way?.run()
  }, [want, ways, addon.pending, known, setParams])
  return { ways, node: addon.node, menu }
}

export interface TitleActionRowProps {
  /** The title as an item (`id` 0: fromTitle). */
  item: Detail
  tmdbId: number
  /** A kid's title page: «Попросить» in place of every other action. */
  kidRequest?: KidRequestState
  guide?: TitleGuide
}

export function TitleActionRow({ item, tmdbId, kidRequest, guide }: TitleActionRowProps) {
  const { user } = useSession()
  if (!user) return null
  if (user.isKid)
    return kidRequest ? (
      <div className={styles.acquire}>
        <RequestButton ask={{ kind: item.kind, tmdbId, title: item.title }} state={kidRequest} size="large" />
      </div>
    ) : null
  return <Row item={item} tmdbId={tmdbId} guide={guide} profileId={user.id} />
}

function Row({ item, tmdbId, guide, profileId }: { item: Detail; tmdbId: number; guide?: TitleGuide; profileId: number }) {
  const t = useT()
  const addon = useTitleWays(item)
  const remembered = useWatchWay(profileId)
  const series = item.kind === 'series'
  const titleKind: 'series' | 'movie' = series ? 'series' : 'movie'
  // the guide's next episode: «Смотреть» plays it through the addons' kinds'
  // episodeActions (as the guide does)
  const episodes = guide?.episodes ?? NO_EPISODES
  const choice = guide?.choice
  const ctx = useMemo<EpisodeCtx>(() => ({ kind: titleKind, tmdbId, title: item.title, poster: item.poster, episodes, choice }), [titleKind, tmdbId, item.title, item.poster, episodes, choice])
  const players = useEpisodePlayers(ctx)
  const { playerFor, anyPlay } = players
  const { order } = useSourceKinds()
  const guidePlay = anyPlay ? { prefer: players.prefer, other: anyPlay.other } : null
  const nextEp = series && guide && guidePlay ? nextGuideEpisode(guide.episodes) : undefined
  // only among what plays here: with no playable source the next episode is not offered
  const nextSources = nextEp ? players.playable(nextEp.sources) : []
  const next = nextSources.length > 0 ? nextEp : undefined
  const ways = addon.ways

  let primary: ReactNode
  if (next && guidePlay) {
    const label = t(next.positionSec ? 'comp.action.resumeEpisode' : 'comp.action.watchEpisode', { code: episodeCode({ seasonNo: next.season, episodeNo: next.number }) })
    const playNext = () => {
      const src = pickSource(nextSources, guide?.choice ?? null, guidePlay.prefer, order)
      if (src) playerFor(src.kind)?.play(next, src)
    }
    primary = (
      <WatchButton label={label} ways={[{ id: 'next', label, run: playNext }]} current="next" extra={[{ label: t('episodes.sheet.other'), run: () => guidePlay.other(next) }]} />
    )
  } else if (ways.length > 0) {
    primary = (
      <WatchButton
        label={t('comp.action.watch')}
        ways={ways}
        current={pickWay(remembered, ways.map((w) => w.id))}
        menu={addon.menu}
        // shared: every other «Смотреть ▾» of the profile follows the choice
        onChoose={(id) => writeWatchWay(profileId, id)}
      />
    )
  }
  const markable = (item.kind === 'series' || item.kind === 'movie') && tmdbId > 0
  return (
    <div className={styles.acquire}>
      <ActionBar
        item={item}
        onPlay={() => {}}
        primary={primary}
        // the mark is this page's one «Хочу посмотреть»
        watchlistButton={false}
        toggles={markable ? <MarkButton key={`${titleKind}-${tmdbId}`} kind={titleKind} tmdbId={tmdbId} name={item.title} poster={item.poster} /> : null}
      />
      {addon.node}
      {/* the addons' episode players (each Host draws its own sheets) */}
      {players.hosts}
    </div>
  )
}
