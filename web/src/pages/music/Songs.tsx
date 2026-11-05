// «Песни»: every song in one list, «Перемешать все» (up to 500 random songs
// of the library, drawn by the server), more loaded as you scroll.
import { APP_NAME } from '../../brand'
import { useCallback, useEffect, useRef, useState } from 'react'
import { ApiError, getSongs, useEvents } from '../../api/client'
import type { Song } from '../../api/types'
import { Button } from '../../ui/Button'
import { Icon } from '../../components/Icon'
import { audio } from '../../player/audio/engine'
import { songItems } from '../../player/audio/queue'
import { playShuffled } from './shuffle'
import { TrackList } from './TrackList'
import styles from './Songs.module.css'
import { useT } from '../../i18n'

const PAGE = 200

type State = { status: 'loading' } | { status: 'error'; message: string } | { status: 'ready'; items: Song[]; total: number; offset: number }

export function Songs() {
  const t = useT()
  const [state, setState] = useState<State>({ status: 'loading' })
  const [shuffling, setShuffling] = useState(false)
  const [shuffleError, setShuffleError] = useState('')
  const abortRef = useRef<AbortController | null>(null)
  const loadingMore = useRef(false)
  const sentinel = useRef<HTMLDivElement>(null)
  const [moreError, setMoreError] = useState('')

  const load = useCallback((quiet = false) => {
    abortRef.current?.abort()
    const ac = new AbortController()
    abortRef.current = ac
    if (!quiet) setState({ status: 'loading' })
    getSongs({ limit: PAGE, offset: 0 }, ac.signal)
      .then((p) => setState({ status: 'ready', items: p.items, total: p.total, offset: PAGE }))
      .catch((e: unknown) => {
        if (ac.signal.aborted || quiet) return
        setState({ status: 'error', message: e instanceof ApiError ? e.message : t('music.songs.loadFailed') })
      })
  }, [])

  useEffect(() => {
    load()
    return () => abortRef.current?.abort()
  }, [load])
  useEvents(() => load(true), ['library.updated'])
  useEffect(() => {
    document.title = `${t('common.songs')} — ${APP_NAME}`
  }, [])

  // The server pages over everything (the offset counts entries this profile cannot see).
  const more = state.status === 'ready' && state.offset < state.total
  const loadMore = useCallback(() => {
    if (state.status !== 'ready' || !more || loadingMore.current) return
    loadingMore.current = true
    setMoreError('')
    const ac = abortRef.current ?? new AbortController()
    getSongs({ limit: PAGE, offset: state.offset }, ac.signal)
      .then((p) =>
        setState((s) => (s.status === 'ready' ? { ...s, items: [...s.items, ...p.items], total: p.total, offset: s.offset + PAGE } : s)),
      )
      .catch((e: unknown) => {
        if (ac.signal.aborted) return
        setMoreError(e instanceof ApiError ? e.message : t('music.loadMoreFailed'))
      })
      .finally(() => {
        loadingMore.current = false
      })
  }, [state, more])

  useEffect(() => {
    const el = sentinel.current
    if (!el) return
    const io = new IntersectionObserver((es) => es.some((e) => e.isIntersecting) && loadMore(), { rootMargin: '800px' })
    io.observe(el)
    return () => io.disconnect()
  }, [loadMore])

  const shuffleAll = () => {
    setShuffling(true)
    setShuffleError('')
    void playShuffled({ kind: 'all' }).then((err) => {
      setShuffling(false)
      setShuffleError(err ?? '')
    })
  }

  const items = state.status === 'ready' ? state.items : []

  return (
    <div className={styles.page}>
      <header className={styles.header}>
        <h1 className={styles.title}>{t('common.songs')}</h1>
        {items.length > 0 && (
          <Button variant="secondary" size="small" icon="shuffle" loading={shuffling} onClick={shuffleAll}>
            {t('music.shuffleAll')}
          </Button>
        )}
      </header>
      {shuffleError && (
        <p className={styles.shuffleError} role="alert">
          {shuffleError}
        </p>
      )}
      {state.status === 'loading' && <div className={styles.skeleton} aria-busy="true" aria-label={t('common.loading')} />}
      {state.status === 'error' && (
        <div className={styles.empty} role="alert">
          <Icon name="cloud" size={44} strokeWidth={1.5} />
          <h2>{t('music.songs.loadFailed')}</h2>
          <p>{state.message}</p>
          <Button variant="secondary" onClick={() => load()}>
            {t('music.retry')}
          </Button>
        </div>
      )}
      {state.status === 'ready' && items.length === 0 && (
        <div className={styles.empty}>
          <Icon name="music" size={48} strokeWidth={1.4} />
          <h2>{t('music.songs.empty')}</h2>
          <p>{t('music.songs.emptyHint')}</p>
        </div>
      )}
      {items.length > 0 && (
        <div className={styles.list}>
          <TrackList tracks={items} albumArtist="" variant="songs" label={t('common.songs')} onPlay={(i) => audio.play(songItems(items), i, { shuffle: false })} />
        </div>
      )}
      {more &&
        (moreError ? (
          <div className={styles.moreError} role="alert">
            <span>{moreError}</span>
            <Button variant="secondary" size="small" onClick={loadMore}>
              {t('music.retry')}
            </Button>
          </div>
        ) : (
          <div ref={sentinel} className={styles.sentinel} aria-hidden />
        ))}
    </div>
  )
}
