// The action row of the core title page (/title/:kind/:tmdbId): a TMDB
// title that may not be in the library. «Смотреть ▾» offers the online ways
// (OnlineWay, the sources build's) and the addons' `watchWay` item actions
// (way id `<addonId>:<label>`), the one the profile chose last first; the
// other item actions are the row's round buttons (ActionBar). A series whose
// episode guide knows the next episode plays it. A kid gets «Попросить» and
// nothing of an addon.
import { useMemo, useState, type ReactNode } from 'react'
import type { ItemDetail as Detail, KidRequestState, SourceChoice, TitleEpisode, TitleInfo } from '../api/types'
import { errorText } from '../api/client'
import { addonString, useRegistryVersion } from '../addons/registry'
import { useAddonSheet, useAddonSlot, useSourceKinds } from '../addons/slots'
import { ActionBar } from '../components/ActionBar'
import { episodeCode } from '../components/EpisodeList'
import { MarkButton } from '../components/MarkButton'
import { RequestButton } from '../components/RequestButton'
import { WatchButton, type WatchWay } from '../components/WatchButton'
import type { EpisodeCtx } from '../components/episodes/episodeTypes'
import { useEpisodePlayers } from '../components/episodes/useEpisodePlayers'
import { useLang, useT } from '../i18n'
import { pickSource } from '../state/pickSource'
import { useSession } from '../state/session'
import { pickWay, useWatchWay, writeWatchWay } from '../state/watchWay'
import { Notice } from '../ui/Notice'
// the sources build's «Онлайн» way (none in the core build)
import { OnlineWay } from './ItemDetailFull'
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
 * `<addonId>:<label>`, labels resolved); `node` draws what they open (an
 * element a run returns) and what they say. None for a kid.
 */
export function useAddonWatchWays(item: Detail): { ways: WatchWay[]; node: ReactNode } {
  const t = useT()
  const lang = useLang()
  const v = useRegistryVersion()
  const actions = useAddonSlot('itemActions')
  const [said, setSaid] = useState<{ text: string; failed: boolean } | null>(null)
  const sheet = useAddonSheet(() => setSaid({ text: t('addons.actionFailed'), failed: true }))
  const run = sheet.run
  const ways = useMemo(() => {
    void v
    const out: WatchWay[] = []
    for (const a of actions) {
      if (!a.watchWay) continue
      try {
        if (a.visible && !a.visible(item)) continue
      } catch (e) {
        console.warn(`antresol: addon ${a.addonId} watch way failed`, e)
        continue
      }
      out.push({
        id: `${a.addonId}:${a.label}`,
        label: addonString(a.addonId, lang, a.label),
        run: () => {
          setSaid(null)
          let answer: ReturnType<typeof a.run> | undefined
          try {
            answer = run(a.addonId, (close) => a.run(item, close))
          } catch (e) {
            console.warn(`antresol: addon ${a.addonId} watch way failed`, e)
            setSaid({ text: errorText(e, t('addons.actionFailed')), failed: true })
            return
          }
          void Promise.resolve(answer).then(
            (text) => typeof text === 'string' && text && setSaid({ text, failed: false }),
            (e: unknown) => {
              console.warn(`antresol: addon ${a.addonId} watch way failed`, e)
              setSaid({ text: errorText(e, t('addons.actionFailed')), failed: true })
            },
          )
        },
      })
    }
    return out
  }, [actions, item, lang, v, run, t])
  const node = (
    <>
      {sheet.node}
      {said?.failed && (
        <p className={styles.acquireError} role="alert">
          {said.text}
        </p>
      )}
      {said && !said.failed && <Notice tone="accent">{said.text}</Notice>}
    </>
  )
  return { ways, node }
}

export interface TitleActionRowProps {
  /** The title as an item (`id` 0: fromTitle). */
  item: Detail
  tmdbId: number
  seasons: TitleInfo['seasons']
  /** A kid's title page: «Попросить» in place of every other action. */
  kidRequest?: KidRequestState
  guide?: TitleGuide
}

export function TitleActionRow({ item, tmdbId, seasons, kidRequest, guide }: TitleActionRowProps) {
  const { user } = useSession()
  if (!user) return null
  if (user.isKid)
    return kidRequest ? (
      <div className={styles.acquire}>
        <RequestButton ask={{ kind: item.kind, tmdbId, title: item.title }} state={kidRequest} size="large" />
      </div>
    ) : null
  return (
    <OnlineWay
      kind={item.kind}
      tmdbId={tmdbId}
      title={item.title}
      poster={item.poster}
      seasons={seasons}
      canWatchOnline={Boolean(user.canWatchOnline)}
      isKid={false}
      openFromUrl
      warm
    >
      {(online) => <Row item={item} tmdbId={tmdbId} online={online} guide={guide} profileId={user.id} />}
    </OnlineWay>
  )
}

function Row({ item, tmdbId, online, guide, profileId }: { item: Detail; tmdbId: number; online: WatchWay[]; guide?: TitleGuide; profileId: number }) {
  const t = useT()
  const addon = useAddonWatchWays(item)
  const remembered = useWatchWay(profileId)
  const series = item.kind === 'series'
  const titleKind: 'series' | 'movie' = series ? 'series' : 'movie'
  // the guide's next episode: «Смотреть» plays it — online by the build's
  // player, the addons' kinds by their episodeActions (as the guide does)
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
  const ways = [...online, ...addon.ways]

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
      {players.core?.node}
      {/* the addons' episode players (each Host draws its own sheets) */}
      {players.hosts}
    </div>
  )
}
