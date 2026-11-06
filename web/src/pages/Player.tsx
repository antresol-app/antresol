// /watch/:id — resolves what to play (a movie, an episode, or the next
// episode of a series/season), where to start, and what comes next, then
// hands over to VideoPlayer. Closing returns to wherever the viewer came from.
import { Button } from '../ui/Button'
import { APP_NAME } from '../brand'
import { useCallback, useEffect, useState, useLayoutEffect } from 'react'
import { useLocation, useNavigate, useParams, useSearchParams } from 'react-router'
import { ApiError, getItem, getTrackPrefs, errorText } from '../api/client'
import type { Episode, ItemDetail, TrackPrefs } from '../api/types'
import { episodeToPlay } from '../components/ActionBar'
import { episodeCode, episodeTitle } from '../components/EpisodeList'
import { Icon } from '../components/Icon'
import { durationSec, playbackParts, resumeSec } from '../player/timeline'
import { VideoPlayer, type UpNext } from '../player/VideoPlayer'
import { asTrackPrefs } from '../player/trackPrefs'
import { rememberSettled } from '../player/rememberQueue'
import styles from './Player.module.css'
import { audio } from '../player/audio/engine'
import { useT } from '../i18n'

type State =
  | { status: 'loading' }
  | { status: 'error'; message: string }
  | { status: 'ready'; item: ItemDetail; series?: ItemDetail; tracks?: TrackPrefs }

/** Episode after `id`: the series' «next» when it lies ahead, else the next one in order. */
function nextEpisode(series: ItemDetail, id: number): Episode | undefined {
  const order = [...series.seasons]
    .sort((a, b) => (a.number === 0 ? 1 : b.number === 0 ? -1 : a.number - b.number))
    .flatMap((s) => [...s.episodes].sort((a, b) => a.episodeNo - b.episodeNo))
  const i = order.findIndex((e) => e.id === id)
  if (i < 0) return undefined
  const n = series.next
  if (n && n.id !== id && order.findIndex((e) => e.id === n.id) > i) return n
  return order[i + 1]
}

export function Player() {
  const t = useT()
  // Video and music never play over each other: the music pauses and hands
  // the system media controls (keys, lock screen) to the video. A layout
  // effect runs before the VideoPlayer's own (passive) Media Session effect.
  useLayoutEffect(() => audio.yieldSession(), [])
  const { id: raw } = useParams()
  const id = Number(raw)
  const [params] = useSearchParams()
  const navigate = useNavigate()
  const location = useLocation()
  const [state, setState] = useState<State>({ status: 'loading' })

  useEffect(() => {
    const ac = new AbortController()
    setState({ status: 'loading' })
    ;(async () => {
      const item = await getItem(id, ac.signal)
      // A series or season plays its next episode.
      if (item.kind === 'series' || item.kind === 'season') {
        const ep = episodeToPlay(item)
        if (!ep) throw new ApiError(404, t('player.page.noEpisodes'))
        navigate(`/watch/${ep.id}${location.search}`, { replace: true, state: location.state })
        return
      }
      // the profile's track defaults, its choices in the series and the
      // subtitle offsets; without them (an older server) the player still plays
      // after the choices still being sent (one made just before «Следующая серия»)
      const tracksReq = rememberSettled()
        .then(() => getTrackPrefs(item.id, ac.signal))
        .then(asTrackPrefs, () => undefined)
      let series: ItemDetail | undefined
      if (item.kind === 'episode' && item.seriesId) {
        series = await getItem(item.seriesId, ac.signal).catch(() => undefined)
      }
      if (item.files.length === 0) throw new ApiError(404, t('player.page.fileMissing'))
      const tracks = await tracksReq
      if (ac.signal.aborted) return
      setState({ status: 'ready', item, series, tracks })
    })().catch((e: unknown) => {
      if (ac.signal.aborted) return
      setState({ status: 'error', message: errorText(e, t('player.page.openFailedDot')) })
    })
    return () => ac.abort()
  }, [id, navigate, location.search, location.state])

  const close = useCallback(() => {
    // Opened from inside the app: go back. Opened by a link: go to the item page.
    if (location.key !== 'default' && window.history.length > 1) navigate(-1)
    else navigate(`/item/${id}`, { replace: true })
  }, [id, location.key, navigate])

  useEffect(() => {
    if (state.status === 'ready') document.title = `${state.item.title} — ${APP_NAME}`
  }, [state])

  if (state.status === 'loading') {
    return (
      <div className={styles.screen} aria-busy="true" aria-label={t('common.loading')}>
        <span className={styles.spinner} />
      </div>
    )
  }
  if (state.status === 'error') {
    return (
      <div className={styles.screen} role="alert">
        <Icon name="warning" size={40} strokeWidth={1.6} />
        <h1>{t('player.page.openFailed')}</h1>
        <p>{state.message}</p>
        <Button className={styles.button} onClick={close}>
          {t('common.close')}
        </Button>
      </div>
    )
  }

  const { item, series, tracks } = state
  const fileParam = Number(params.get('file')) || undefined
  const parts = playbackParts(item.files, fileParam)
  const total = parts.reduce((a, f) => a + durationSec(f), 0)
  const tParam = params.get('t')
  const start = tParam !== null ? Math.max(0, Number(tParam) || 0) : resumeSec(item.progress, total)

  const isEpisode = item.kind === 'episode'
  const ep = isEpisode ? series?.seasons.flatMap((s) => s.episodes).find((e) => e.id === item.id) : undefined
  const title = isEpisode ? (series?.title ?? item.subtitle.split(' · ')[0] ?? item.title) : item.title
  const subtitle = isEpisode && ep ? `${episodeCode(ep)} · ${episodeTitle(ep)}` : isEpisode ? item.subtitle : item.year ? String(item.year) : undefined

  const n = isEpisode && series ? nextEpisode(series, item.id) : undefined
  const next: UpNext | undefined = n
    ? {
        code: episodeCode(n),
        title: episodeTitle(n),
        image: n.backdrop || n.poster,
        play: () => navigate(`/watch/${n.id}`, { replace: true, state: location.state }),
      }
    : undefined

  return (
    <VideoPlayer
      key={`${item.id}-${parts.map((p) => p.id).join(',')}`}
      itemId={item.id}
      title={title}
      subtitle={subtitle}
      artwork={item.poster || item.backdrop}
      parts={parts}
      startSec={start}
      next={next}
      onClose={close}
      tracks={tracks}
      voiceOverItem={
        __LIBRARY_EDITION__ ? undefined : isEpisode ? (item.seriesId ? { id: item.seriesId, title } : undefined) : item.kind === 'movie' ? { id: item.id, title } : undefined
      }
    />
  )
}
