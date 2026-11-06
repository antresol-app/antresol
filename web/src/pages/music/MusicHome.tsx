// «Музыка»: «Перемешать все», the «Жанры» row and shelves of square tiles
// from /api/music/home (recently added, recently played, artists,
// playlists). Refreshes when the library changes.
import { APP_NAME } from '../../brand'
import { useCallback, useEffect, useRef, useState } from 'react'
import { getGenres, getMusicHome, useEvents, errorText } from '../../api/client'
import type { Genre, MusicHome as MusicHomeData } from '../../api/types'
import { Button, ButtonLink } from '../../ui/Button'
import { RescanLink } from '../../components/RescanLink'
import { Icon } from '../../components/Icon'
import { LibraryTools, MineEmpty } from '../../components/LibraryScopeMenu'
import { Shelf } from '../../components/Shelf'
import { answerSlot, forgetAnswer, lastAnswer, rememberAnswer } from '../../state/lastAnswer'
import { useLibraryScope } from '../../state/libraryScope'
import { shelvesWithPending, usePendingCards } from '../../state/pendingCards'
import { useSession } from '../../state/session'
import { GenreRow } from './GenreRow'
import { playShuffled } from './shuffle'
import styles from './MusicHome.module.css'
import { useT } from '../../i18n'

type State = { status: 'loading' } | { status: 'error'; message: string } | { status: 'ready'; data: MusicHomeData }

const SEE_ALL: Record<string, string> = {
  'recent-albums': '/music/albums',
  artists: '/music/artists',
  playlists: '/music/playlists',
}

/** The library sections under the title. */
const LINKS = [
  { to: '/music/albums', label: 'music.allAlbums' },
  { to: '/music/artists', label: 'common.artists' },
  { to: '/music/songs', label: 'common.songs' },
  { to: '/music/playlists', label: 'common.playlists' },
] as const

export function MusicHome() {
  const t = useT()
  const { user } = useSession()
  const { scope } = useLibraryScope()
  // back to the tab: its last answer at once, then a quiet refresh (state/lastAnswer)
  const answerKey = `music ${scope}`
  const [state, setState] = useState<State>(() => {
    const hit = lastAnswer<MusicHomeData>(answerKey)
    return hit ? { status: 'ready', data: hit } : { status: 'loading' }
  })
  const [genres, setGenres] = useState<Genre[]>(() => lastAnswer<Genre[]>('music genres') ?? [])
  const [shuffling, setShuffling] = useState(false)
  const [shuffleError, setShuffleError] = useState('')
  const abortRef = useRef<AbortController | null>(null)
  const pending = usePendingCards()

  // kept: the refresh of a kept answer (a revisit); when it fails the kept
  // answer is forgotten and the error shows, as on a first visit
  const load = useCallback((quiet = false, kept = false) => {
    abortRef.current?.abort()
    const ac = new AbortController()
    abortRef.current = ac
    if (!quiet) setState({ status: 'loading' })
    const slot = answerSlot(`music ${scope}`)
    const genreSlot = answerSlot('music genres')
    getMusicHome(ac.signal, scope)
      .then((data) => {
        rememberAnswer(slot, data)
        setState({ status: 'ready', data })
      })
      .catch((e: unknown) => {
        if (ac.signal.aborted || (quiet && !kept)) return
        if (kept) forgetAnswer(`music ${scope}`)
        setState({ status: 'error', message: errorText(e, t('music.home.loadFailed')) })
      })
    // The genre row is extra: when it fails, the row stays as it was (hidden at first).
    getGenres(ac.signal)
      .then((g) => {
        rememberAnswer(genreSlot, g)
        setGenres(g)
      })
      .catch(() => {})
  }, [scope])

  const shuffleAll = () => {
    setShuffling(true)
    setShuffleError('')
    void playShuffled({ kind: 'all' }).then((err) => {
      setShuffling(false)
      setShuffleError(err ?? '')
    })
  }

  useEffect(() => {
    const hit = lastAnswer<MusicHomeData>(answerKey)
    if (hit) setState((s) => (s.status === 'ready' && s.data === hit ? s : { status: 'ready', data: hit }))
    load(!!hit, !!hit)
    return () => abortRef.current?.abort()
  }, [load, answerKey])

  useEvents(() => load(true), ['library.updated'])

  useEffect(() => {
    document.title = `${t('common.music')} — ${APP_NAME}`
  }, [])

  const shelves =
    state.status === 'ready'
      ? shelvesWithPending(state.data.shelves, pending, [{ id: 'recent-albums', kinds: ['album'], title: t('browse.sort.recent') }]).filter(
          (s) => s.cards.length > 0,
        )
      : []

  return (
    <div className={styles.page}>
      <header className={styles.header}>
        <h1 className={styles.title}>{t('common.music')}</h1>
        <div className={styles.headerEnd}>
          {state.status === 'ready' && shelves.length > 0 && (
            <>
              <nav className={styles.links} aria-label={t('common.library')}>
                {LINKS.map((l) => (
                  <ButtonLink key={l.to} to={l.to} variant="plain" size="small">
                    {t(l.label)}
                  </ButtonLink>
                ))}
              </nav>
              <Button variant="secondary" size="small" icon="shuffle" loading={shuffling} onClick={shuffleAll}>
                {t('music.shuffleAll')}
              </Button>
            </>
          )}
          <LibraryTools />
        </div>
      </header>
      {shuffleError && (
        <p className={styles.shuffleError} role="alert">
          {shuffleError}
        </p>
      )}

      {state.status === 'loading' && (
        <div className={styles.skeleton} aria-busy="true" aria-label={t('common.loading')}>
          {[0, 1].map((r) => (
            <div key={r}>
              <div className={styles.skeletonTitle} />
              <div className={styles.skeletonRow}>
                {Array.from({ length: 7 }, (_, i) => (
                  <div key={i} className={styles.skeletonCard} />
                ))}
              </div>
            </div>
          ))}
        </div>
      )}

      {state.status === 'error' && (
        <div className={styles.empty} role="alert">
          <Icon name="cloud" size={44} strokeWidth={1.5} />
          <h2>{t('music.home.loadFailed')}</h2>
          <p>{state.message}</p>
          <Button variant="secondary" onClick={() => load()}>
            {t('music.retry')}
          </Button>
        </div>
      )}

      {state.status === 'ready' && shelves.length === 0 && scope === 'mine' && <MineEmpty className={styles.empty} />}

      {state.status === 'ready' && shelves.length === 0 && scope !== 'mine' && (
        <div className={styles.empty}>
          <Icon name="music" size={48} strokeWidth={1.4} />
          <h2>{t('music.emptyTitle')}</h2>
          {user?.isKid ? (
            <p>{t('music.home.emptyKid')}</p>
          ) : (
            <>
              <p>{t(__LIBRARY_EDITION__ ? 'music.emptyHint' : 'music.emptyHintFull')}</p>
              {__LIBRARY_EDITION__ ? (
                <RescanLink variant="secondary" />
              ) : (
                <ButtonLink to="/search?dk=music" icon="search" variant="secondary">
                  {t('music.find')}
                </ButtonLink>
              )}
            </>
          )}
        </div>
      )}

      {shelves.length > 0 && (
        <div className={styles.shelves}>
          <GenreRow genres={genres} onError={setShuffleError} />
          {shelves.map((s, i) => (
            <Shelf key={s.id} title={s.title} cards={s.cards} variant="square" seeAllTo={SEE_ALL[s.id]} priority={i === 0} />
          ))}
        </div>
      )}
    </div>
  )
}
