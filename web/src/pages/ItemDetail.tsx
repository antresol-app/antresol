// Item page in the Apple TV style: full-bleed backdrop, logo or title,
// one line of metadata, the description, actions; for series a season switcher
// and episodes; then the cast and details. `?play=1` opens the player at once.
import { APP_NAME } from '../brand'
import { useCallback, useEffect, useRef, useState } from 'react'
import { Link, Navigate, useNavigate, useParams, useSearchParams } from 'react-router'
import { ApiError, downloadUrl, getItem, getTitle, imageSrcSet, imageUrl, useEvents, errorText } from '../api/client'
import type { Episode, ItemDetail as Detail, MediaFile, TitleInfo } from '../api/types'
import { MarkButton } from '../components/MarkButton'
import { ActionBar, episodeToPlay, type PlayOptions } from '../components/ActionBar'
import type { WatchWay } from '../components/WatchButton'
import { ButtonLink, Button } from '../ui/Button'
import { IconButton } from '../ui/IconButton'
import { itemHref } from '../components/Card'
import { SubtitleSearchSheet } from '../player/SubtitleSearchSheet'
import { VoiceOverSheet } from '../components/VoiceOverSheet'
// the sources build's parts of the page: TMDB titles, «Следить», the «Онлайн»
// way, adding seasons, the pinned release (not in the core build: see docs/build-tags.md)
import { AddSeason, FollowButton, followTarget, fromTitle, OnlineWay, PinnedPanel, TitleActions } from './ItemDetailFull'
import { EpisodeList, episodesCount } from '../components/EpisodeList'
import { FamilyProgress, useFamily, type FamilyKind } from '../components/FamilyProgress'
import { Icon } from '../components/Icon'
import { useTrimmedLogo } from '../components/useTrimmedImage'
import { durationSec, humanMinutes, isMultiPart, languageName, playbackParts } from '../player/timeline'
import { useSession } from '../state/session'
import { useScreenLimits } from '../state/screenTime'
import { Badges } from '../ui/Badge'
import { useLeaveDeleted } from '../components/DeleteItem'
import { RatingChips, ratingRows, useRatings } from '../components/Ratings'
import { Rail } from '../components/Rail'
import { RelatedShelves, useRelated } from '../components/RelatedShelves'
import styles from './ItemDetail.module.css'
import { locale, t as tr, useT, type Key } from '../i18n'

type State = { status: 'loading' } | { status: 'error'; message: string; notFound: boolean; forbidden?: boolean } | { status: 'ready'; item: Detail }

const KIND: Record<string, Key> = { movie: 'common.kind.movie', series: 'common.kind.series', season: 'common.kind.season', episode: 'common.kind.episode' }
const VIDEO_KINDS = new Set(['movie', 'series', 'season', 'episode'])
const FAMILY_KIND: Record<string, FamilyKind> = { movie: 'movie', episode: 'episode', series: 'series', season: 'series' }

const seasonsCount = (n: number) => tr('item.seasons', { count: n })

function quality(item: Detail): string {
  const r = item.files.map((f) => f.resolution ?? '')
  if (r.includes('4K')) return '4K'
  if (r.some((x) => x === '1080p' || x === '720p' || x === '1440p')) return 'HD'
  return ''
}

function runtimeMinutes(item: Detail): number {
  if (item.runtime) return item.runtime
  const s = playbackParts(item.files).reduce((a, f) => a + durationSec(f), 0)
  return s / 60
}

/** Text part of the metadata line: «2024 · 2 ч 46 мин» or «2019 · 2 сезона». */
function metaText(item: Detail): string[] {
  const out: string[] = []
  if (item.year) out.push(String(item.year))
  if (item.kind === 'series') {
    const regular = item.seasons.filter((s) => s.number > 0).length
    if (regular > 0) out.push(seasonsCount(regular))
    else {
      const n = item.seasons.reduce((a, s) => a + s.episodes.length, 0)
      if (n) out.push(episodesCount(n))
    }
  } else if (item.kind === 'season') {
    const n = item.seasons.reduce((a, s) => a + s.episodes.length, 0)
    if (n) out.push(episodesCount(n))
  } else {
    const m = humanMinutes(runtimeMinutes(item)) // '' under half a minute
    if (m) out.push(m)
  }
  if (item.genres.length) out.push(item.genres.slice(0, 2).join(', '))
  return out
}

/** The file whose subtitles «Найти субтитры…» looks for: a movie or an episode with one playable file. */
function subtitleFile(it: Detail): MediaFile | undefined {
  if (it.kind !== 'movie' && it.kind !== 'episode') return undefined
  return playbackParts(it.files)[0] ?? it.files[0]
}

/** Starts a browser download of an episode's file. */
async function downloadEpisode(e: Episode) {
  const it = await getItem(e.id)
  const f = it.files[0]
  if (!f) throw new Error('no file')
  const a = document.createElement('a')
  a.href = downloadUrl(f.id)
  a.download = ''
  document.body.append(a)
  a.click()
  a.remove()
}

function initials(name: string) {
  return name
    .split(/\s+/)
    .filter(Boolean)
    .slice(0, 2)
    .map((w) => w.charAt(0).toLocaleUpperCase(locale()))
    .join('')
}

function uniqueLangs(langs: string[]): string[] {
  const out: string[] = []
  for (const l of langs.map(languageName)) if (l && !out.includes(l)) out.push(l)
  return out
}

export function ItemDetail() {
  const t = useT()
  const { id: rawId, kind: tmdbKind, tmdbId: rawTmdb } = useParams()
  const id = Number(rawId)
  // /title/:kind/:tmdbId: a TMDB title found by «Найти и добавить» (sources build).
  const tmdbId = Number(rawTmdb)
  const isTitle = !__LIBRARY_EDITION__ && Boolean(tmdbKind && tmdbId > 0)
  const [params] = useSearchParams()
  const navigate = useNavigate()
  const { user } = useSession()
  const limits = useScreenLimits(Boolean(user?.isKid))
  const leave = useLeaveDeleted()
  const [state, setState] = useState<State>({ status: 'loading' })
  const [expanded, setExpanded] = useState(false)
  const [titleSeasons, setTitleSeasons] = useState<TitleInfo['seasons']>([])
  // a kid's «Попросить» of the title (sources build)
  const [titleRequest, setTitleRequest] = useState<TitleInfo['kidRequest']>(undefined)
  const [problem, setProblem] = useState('')
  const [addingSeason, setAddingSeason] = useState(false)
  const [findingSubs, setFindingSubs] = useState(false)
  const [findingVoices, setFindingVoices] = useState(false)
  const abortRef = useRef<AbortController | null>(null)
  const autoplay = params.get('play') === '1'

  // «Смотреть» on Главная: straight into the player; closing it returns there.
  useEffect(() => {
    if (autoplay && id > 0) navigate(`/watch/${id}`, { replace: true })
  }, [autoplay, id, navigate])

  const load = useCallback(
    (quiet = false) => {
      abortRef.current?.abort()
      const ac = new AbortController()
      abortRef.current = ac
      if (!quiet) setState({ status: 'loading' })
      const req: Promise<Detail | null> = !__LIBRARY_EDITION__ && isTitle
        ? getTitle(tmdbKind!, tmdbId, ac.signal).then((ti) => {
            if (ti.inLibrary && ti.libraryItemId) {
              navigate(`/item/${ti.libraryItemId}`, { replace: true })
              return null
            }
            setTitleSeasons(ti.seasons ?? [])
            setTitleRequest(ti.kidRequest)
            return fromTitle(ti, Boolean(user?.isKid))
          })
        : getItem(id, ac.signal)
      req
        .then((item) => item && setState({ status: 'ready', item }))
        .catch((e: unknown) => {
          if (ac.signal.aborted || quiet) return
          const notFound = e instanceof ApiError && (e.status === 404 || e.status === 400)
          const forbidden = isTitle && e instanceof ApiError && e.status === 403
          setState({ status: 'error', notFound, forbidden, message: errorText(e, tr('common.loadFailed')) })
        })
    },
    [id, isTitle, tmdbKind, tmdbId, navigate, user?.isKid],
  )

  useEffect(() => {
    if (autoplay) return
    setExpanded(false)
    load()
    return () => abortRef.current?.abort()
  }, [load, autoplay])

  useEvents(() => !autoplay && load(true), ['library.updated'])

  const item = state.status === 'ready' ? state.item : undefined
  useEffect(() => {
    if (item) document.title = `${item.title} — ${APP_NAME}`
  }, [item])

  const logoSrc = imageUrl(item?.logo)
  const [logoFailed, setLogoFailed] = useState(false)
  useEffect(() => setLogoFailed(false), [logoSrc])
  const { url: trimmedLogo, dark: darkLogo } = useTrimmedLogo(logoSrc && !logoFailed ? logoSrc : undefined)
  // IMDb and Rotten Tomatoes: after the page, never holding it up
  const ratings = useRatings(item && (item.kind === 'movie' || item.kind === 'series') ? item.imdbId : undefined)
  // «Семья»: where the other profiles are (the server lists nobody for a kid)
  const family = useFamily(item && VIDEO_KINDS.has(item.kind) ? item.id : 0, !isTitle && !user?.isKid)
  // «Коллекция» and «Похожие»: library movies and series, after the page
  const related = useRelated(item?.id ?? 0, !isTitle && (item?.kind === 'movie' || item?.kind === 'series'), item)

  if (autoplay) return <div className={styles.blank} />
  if (state.status === 'loading') return <DetailSkeleton />
  if (state.status === 'error') {
    // a kid's title page above its age limit: «Недоступно для этого профиля», nothing of the title
    if (state.forbidden)
      return (
        <div className={styles.error} role="alert">
          <Icon name="lock" size={44} strokeWidth={1.5} />
          <h1>{state.message}</h1>
          <ButtonLink to="/" variant="secondary">
            {t('item.toHome')}
          </ButtonLink>
        </div>
      )
    return (
      <div className={styles.error} role="alert">
        <Icon name={state.notFound ? 'info' : 'cloud'} size={44} strokeWidth={1.5} />
        <h1>{state.notFound ? t('item.notFound') : t('common.loadFailed')}</h1>
        <p>{state.notFound && !isTitle ? t('item.notFoundText') : state.message}</p>
        {state.notFound ? (
          <ButtonLink to="/" variant="secondary">
            {t('item.toHome')}
          </ButtonLink>
        ) : (
          <Button variant="secondary" onClick={() => load()}>
            {t('common.retry')}
          </Button>
        )}
      </div>
    )
  }

  const it = state.item
  // music and books (an old /item link from Files or Stats): their own page
  if (!VIDEO_KINDS.has(it.kind)) return <Navigate to={it.kind === 'track' ? '/music' : itemHref(it.kind, it.id)} replace />
  const backdropRef = it.backdrop || it.poster
  const backdrop = imageUrl(backdropRef, 1280)
  const isShow = it.kind === 'series' || it.kind === 'season'
  const next = isShow ? episodeToPlay(it) : undefined
  const meta = metaText(it)
  const q = quality(it)
  const multiPart = isMultiPart(it.files)
  // A series on the server: missing and new seasons come through the same
  // per-season flow as a title that is not on the server.
  // (a library-only server has neither «Найти и добавить» nor «Следить»)
  const canAddSeason = !__LIBRARY_EDITION__ && it.kind === 'series' && Boolean(it.tmdbId) && !user?.isKid && !user?.libraryOnly
  const followOf = __LIBRARY_EDITION__ || user?.isKid || user?.libraryOnly ? null : followTarget(it, tmdbId)
  const followButton = !__LIBRARY_EDITION__ && followOf && (
    <FollowButton key={`${followOf.kind}-${followOf.tmdbId}`} kind={followOf.kind} tmdbId={followOf.tmdbId} initial={it.follow} />
  )
  const subsFile = user?.isAdmin && !isTitle ? subtitleFile(it) : undefined
  // «Найти озвучку…»: a film or series on the server, adult profiles of a
  // server with sources (not a kid, not library-only)
  const canFindVoices =
    !__LIBRARY_EDITION__ && !isTitle && it.id > 0 && (it.kind === 'movie' || it.kind === 'series') && Boolean(user) && !user?.isKid && !user?.libraryOnly
  // «Удалить»: admins, items on the server (a TMDB title has no files here)
  const canDelete = Boolean(user?.isAdmin && !user.isKid) && !isTitle && it.id > 0

  const play = (opts: PlayOptions = {}) => {
    const qs = new URLSearchParams()
    if (opts.fromStart) qs.set('t', '0')
    if (opts.fileId) qs.set('file', String(opts.fileId))
    const target = isShow && next ? next.id : it.id
    navigate(`/watch/${target}${qs.size ? `?${qs}` : ''}`)
  }

  const audioLangs = uniqueLangs(it.files.flatMap((f) => f.audio.map((a) => a.lang)))
  const subLangs = uniqueLangs(it.files.flatMap((f) => f.subs.filter((s) => !s.burnIn).map((s) => s.lang)))
  const info: [string, string][] = [
    [t('item.info.genre'), it.genres.join(', ')],
    [t('item.info.originalTitle'), it.originalTitle && it.originalTitle !== it.title ? it.originalTitle : ''],
    [t('item.info.year'), it.year ? String(it.year) : ''],
    [t('item.info.runtime'), !isShow && runtimeMinutes(it) > 0 ? humanMinutes(runtimeMinutes(it)) : ''],
    [t('item.info.rating'), it.rating ?? ''],
    ...ratingRows(t, ratings),
    [t('common.quality'), [...new Set(it.files.map((f) => f.resolution).filter(Boolean))].join(', ')],
    [t('item.info.audio'), audioLangs.join(', ')],
    [t('common.subtitles'), subLangs.join(', ')],
    [t('common.kind.file'), multiPart ? t('item.info.parts', { count: it.files.length }) : ''],
  ]
  const shownInfo = info.filter(([, v]) => v)

  // «Смотреть» plays the server's file; the sources build adds «Онлайн» to its ▾
  // the mark is the one «Хочу посмотреть» of a library movie or series (it keeps the watchlist in step)
  const marked = (it.kind === 'series' || it.kind === 'movie') && it.id > 0 && Boolean(it.tmdbId)
  const actionBar = (moreWays?: WatchWay[]) => (
    <ActionBar
      item={it}
      watchlistButton={!marked}
      onPlay={play}
      moreWays={moreWays}
      onChanged={() => load(true)}
      onFindSubtitles={subsFile ? () => setFindingSubs(true) : undefined}
      onFindVoiceOvers={canFindVoices ? () => setFindingVoices(true) : undefined}
      deletion={
        canDelete
          ? {
              itemId: it.id,
              title: it.title,
              onDeleted: (d) => {
                if (!d.whole) load(true) // one season went: the series stays
                else leave(it.kind === 'movie' ? '/movies' : it.seriesId ? `/item/${it.seriesId}` : '/series')
              },
            }
          : undefined
      }
      // «Добавить сезон…» is one of the round buttons (a tile on phones)
      toggles={
        <>
          {canAddSeason && (
            <IconButton
              size="lg"
              icon="plus"
              label={t('item.addSeason.aria')}
              tile={t('item.addSeason.tile')}
              aria-expanded={addingSeason}
              onClick={() => setAddingSeason((v) => !v)}
            />
          )}
          {/* «Отметка»: the profile's state on the title (tracking) */}
          {marked ? <MarkButton key={`${it.kind}-${it.tmdbId}`} kind={it.kind === 'movie' ? 'movie' : 'series'} tmdbId={it.tmdbId!} /> : null}
          {followButton}
        </>
      }
    />
  )

  return (
    <article className={styles.page} aria-labelledby="item-title">
      <div className={[styles.hero, isShow ? styles.heroShow : ''].join(' ')}>
        <div className={styles.backdrop} aria-hidden>
          {backdrop && <img src={backdrop} srcSet={imageSrcSet(backdropRef, 640, 1920)} sizes="100vw" alt="" fetchPriority="high" decoding="async" draggable={false} />}
        </div>
        <div className={styles.scrim} aria-hidden />
        <div className={styles.copy}>
          {it.kind === 'episode' && it.seriesId ? (
            <Link to={`/item/${it.seriesId}`} className={styles.eyebrowLink}>
              {it.subtitle}
              <Icon name="chevronRight" size={14} strokeWidth={2.6} />
            </Link>
          ) : (
            KIND[it.kind] && <p className={styles.eyebrow}>{t(KIND[it.kind]!)}</p>
          )}
          {logoSrc && !logoFailed ? (
            <h1 id="item-title" className={styles.logoTitle}>
              <img
                src={trimmedLogo ?? logoSrc}
                data-ready={trimmedLogo ? true : undefined}
                data-dark={darkLogo || undefined}
                alt={it.title}
                className={styles.logo}
                onError={() => setLogoFailed(true)}
              />
            </h1>
          ) : (
            <h1 id="item-title" className={styles.title}>
              {it.title}
            </h1>
          )}
          <p className={styles.meta}>
            {meta.map((m, i) => (
              <span key={m}>
                {i > 0 && <span className={styles.sep}>·</span>}
                {m}
              </span>
            ))}
            {it.rating && <span className={styles.badge}>{it.rating}</span>}
            {q && <span className={styles.badge}>{q}</span>}
            <RatingChips ratings={ratings} className={styles.badge} />
          </p>
          <Badges items={it.badges} className={styles.badges} />
          {it.overview && (
            <div className={styles.overviewWrap}>
              <p className={[styles.overview, expanded ? styles.expanded : ''].join(' ')}>{it.overview}</p>
              {!expanded && it.overview.length > 200 && (
                <Button variant="link" className={styles.more} onClick={() => setExpanded(true)}>
                  {t('item.readMore')}
                </Button>
              )}
            </div>
          )}
          {!__LIBRARY_EDITION__ && isTitle ? (
            <TitleActions
              item={it}
              tmdbId={tmdbId}
              seasons={titleSeasons}
              kidRequest={titleRequest}
              isKid={Boolean(user?.isKid)}
              isAdmin={Boolean(user?.isAdmin)}
              profileId={user?.id ?? 0}
              canWatchOnline={Boolean(user?.canWatchOnline)}
              follow={followButton}
            />
          ) : (
            <>
              {!__LIBRARY_EDITION__ && (it.kind === 'series' || it.kind === 'movie') && it.tmdbId ? (
                <OnlineWay
                  kind={it.kind}
                  tmdbId={it.tmdbId}
                  title={it.title}
                  poster={it.poster}
                  canWatchOnline={Boolean(user?.canWatchOnline)}
                  isKid={Boolean(user?.isKid)}
                  openFromUrl
                >
                  {actionBar}
                </OnlineWay>
              ) : (
                actionBar()
              )}
              <FamilyProgress
                members={family}
                kind={FAMILY_KIND[it.kind] ?? 'movie'}
                onOpen={(m) => navigate(`/watch/${m.itemId}?t=${m.next ? 0 : Math.floor(m.positionMs / 1000)}`)}
              />
              {findingSubs && subsFile && (
                <SubtitleSearchSheet fileId={subsFile.id} onClose={() => setFindingSubs(false)} onDownloaded={() => load(true)} />
              )}
              {!__LIBRARY_EDITION__ && findingVoices && canFindVoices && (
                <VoiceOverSheet itemId={it.id} title={it.title} onClose={() => setFindingVoices(false)} />
              )}
              {!__LIBRARY_EDITION__ && canAddSeason && addingSeason && <AddSeason item={it} tmdbId={it.tmdbId!} isAdmin={Boolean(user?.isAdmin)} />}
              {!__LIBRARY_EDITION__ && followOf && <PinnedPanel key={`${followOf.kind}-${followOf.tmdbId}`} kind={followOf.kind} tmdbId={followOf.tmdbId} name={it.title} poster={it.poster} />}
            </>
          )}
        </div>
      </div>

      <div className={styles.sections}>
        {problem && (
          <p className={styles.acquireError} role="alert">
            {problem}
          </p>
        )}
        {isShow && it.seasons.length > 0 && (
          <EpisodeList
            key={it.id}
            seasons={it.seasons}
            initialSeason={next?.seasonNo}
            nextId={next?.progress && next.progress > 0 ? next.id : it.next?.id}
            seriesArt={{ poster: it.poster, backdrop: it.backdrop }}
            family={family}
            onPlay={(e) => navigate(`/watch/${e.id}`)}
            // a kid whose video is limited does not download («Экранное время»)
            onDownload={
              limits.video
                ? undefined
                : (e) => {
                    setProblem('')
                    downloadEpisode(e).catch(() => setProblem(t('item.downloadEpisodeFailed')))
                  }
            }
          />
        )}

        {it.cast.length > 0 && (
          <section className={styles.cast} aria-labelledby="cast-title">
            <h2 id="cast-title" className={styles.sectionTitle}>
              {t('item.cast')}
            </h2>
            <Rail labelledBy="cast-title" listClassName={styles.castRow}>
              {it.cast.slice(0, 20).map((c) => (
                <li key={`${c.name}-${c.character ?? ''}`} className={styles.person}>
                  {c.photo ? (
                    <img className={styles.photo} src={imageUrl(c.photo, 240)} alt="" loading="lazy" />
                  ) : (
                    <span className={styles.initials} aria-hidden>
                      {initials(c.name)}
                    </span>
                  )}
                  <span className={styles.personName}>{c.name}</span>
                  {c.character && <span className={styles.character}>{c.character}</span>}
                </li>
              ))}
            </Rail>
          </section>
        )}

        <RelatedShelves itemId={it.id} rows={related} />

        {shownInfo.length > 0 && (
          <section className={styles.info} aria-labelledby="info-title">
            <h2 id="info-title" className={styles.sectionTitle}>
              {t('item.info.title')}
            </h2>
            <dl className={styles.infoGrid}>
              {shownInfo.map(([k, v]) => (
                <div key={k}>
                  <dt>{k}</dt>
                  <dd>{v}</dd>
                </div>
              ))}
            </dl>
          </section>
        )}
      </div>
    </article>
  )
}

function DetailSkeleton() {
  const t = useT()
  return (
    <div className={styles.page} aria-busy="true" aria-label={t('common.loading')}>
      <div className={styles.hero}>
        <div className={styles.skeletonBackdrop} />
        <div className={styles.copy}>
          <div className={styles.skeletonTitle} />
          <div className={styles.skeletonLine} />
          <div className={styles.skeletonLine} style={{ width: '60%' }} />
        </div>
      </div>
    </div>
  )
}
