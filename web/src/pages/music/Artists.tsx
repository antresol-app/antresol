// «Артисты»: round avatars (the newest album's cover) in a grid, more
// loaded as you scroll.
import { APP_NAME } from '../../brand'
import { useCallback, useEffect, useRef, useState } from 'react'
import { ApiError, getArtists, useEvents } from '../../api/client'
import type { Card } from '../../api/types'
import { Button } from '../../ui/Button'
import { Icon } from '../../components/Icon'
import { LibraryTools, MineEmpty } from '../../components/LibraryScopeMenu'
import { PosterGrid, PosterGridSkeleton } from '../../components/PosterGrid'
import { useLibraryScope } from '../../state/libraryScope'
import styles from './Artists.module.css'
import { useT } from '../../i18n'

const PAGE = 120

type State = { status: 'loading' } | { status: 'error'; message: string } | { status: 'ready'; items: Card[]; total: number; offset: number }

export function Artists() {
  const t = useT()
  const [state, setState] = useState<State>({ status: 'loading' })
  const abortRef = useRef<AbortController | null>(null)
  const loadingMore = useRef(false)
  const sentinel = useRef<HTMLDivElement>(null)
  const [moreError, setMoreError] = useState('')
  const { scope } = useLibraryScope()

  const load = useCallback((quiet = false) => {
    abortRef.current?.abort()
    const ac = new AbortController()
    abortRef.current = ac
    if (!quiet) setState({ status: 'loading' })
    getArtists({ limit: PAGE, scope }, ac.signal)
      .then((p) => setState({ status: 'ready', items: p.items, total: p.total, offset: PAGE }))
      .catch((e: unknown) => {
        if (ac.signal.aborted || quiet) return
        setState({ status: 'error', message: e instanceof ApiError ? e.message : t('music.artists.loadFailed') })
      })
  }, [scope])

  useEffect(() => {
    load()
    return () => abortRef.current?.abort()
  }, [load])
  useEvents(() => load(true), ['library.updated'])
  useEffect(() => {
    document.title = `${t('common.artists')} — ${APP_NAME}`
  }, [])

  // The server pages over everything (the offset counts entries this profile cannot see).
  const more = state.status === 'ready' && state.offset < state.total
  const loadMore = useCallback(() => {
    if (state.status !== 'ready' || !more || loadingMore.current) return
    loadingMore.current = true
    setMoreError('')
    const ac = abortRef.current ?? new AbortController()
    getArtists({ limit: PAGE, offset: state.offset, scope }, ac.signal)
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
  }, [state, more, scope])

  useEffect(() => {
    const el = sentinel.current
    if (!el) return
    const io = new IntersectionObserver((es) => es.some((e) => e.isIntersecting) && loadMore(), { rootMargin: '800px' })
    io.observe(el)
    return () => io.disconnect()
  }, [loadMore])

  return (
    <div className={styles.page}>
      <header className={styles.header}>
        <h1 className={styles.title}>{t('common.artists')}</h1>
        <LibraryTools />
      </header>
      {state.status === 'loading' && <PosterGridSkeleton variant="square" count={14} />}
      {state.status === 'error' && (
        <div className={styles.empty} role="alert">
          <Icon name="cloud" size={44} strokeWidth={1.5} />
          <h2>{t('music.artists.loadFailed')}</h2>
          <p>{state.message}</p>
          <Button variant="secondary" onClick={() => load()}>
            {t('music.retry')}
          </Button>
        </div>
      )}
      {state.status === 'ready' && state.items.length === 0 && scope === 'mine' && <MineEmpty className={styles.empty} />}
      {state.status === 'ready' && state.items.length === 0 && scope !== 'mine' && (
        <div className={styles.empty}>
          <Icon name="person" size={48} strokeWidth={1.4} />
          <h2>{t('music.artists.empty')}</h2>
          <p>{t('music.artists.emptyHint')}</p>
        </div>
      )}
      {state.status === 'ready' && state.items.length > 0 && <PosterGrid cards={state.items} label={t('common.artists')} variant="square" />}
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
