// Главная: featured hero carousel and shelves («Продолжить», «Слушали
// недавно» (square tiles), «Читаю сейчас», «Недавно добавленные фильмы»,
// «Новые серии», «Хочу посмотреть») in the server's order. Refreshes itself
// when the library changes. In «Моя медиатека» the hero and the «recently
// added» shelves keep the profile's own titles (the others are its own
// anyway); the picker is in the profile menu and on the section pages.
import { APP_NAME } from '../brand'
import { useCallback, useEffect, useRef, useState } from 'react'
import { getHome, useEvents, errorText } from '../api/client'
import type { CardKind, Home as HomeData, Shelf as ShelfData } from '../api/types'
import { isSquareKind } from '../components/Card'
import { ButtonLink, Button } from '../ui/Button'
import { RescanLink } from '../components/RescanLink'
import { Hero } from '../components/Hero'
import { Icon } from '../components/Icon'
import { MineEmpty } from '../components/LibraryScopeMenu'
import { ScreenTimeChip } from '../components/ScreenTimeChip'
import { Shelf } from '../components/Shelf'
import { answerSlot, forgetAnswer, lastAnswer, rememberAnswer } from '../state/lastAnswer'
import { useLibraryScope } from '../state/libraryScope'
import { usePendingCards, withPending } from '../state/pendingCards'
import { useSession } from '../state/session'
import styles from './Home.module.css'
import { t as tr, useT } from '../i18n'

type State = { status: 'loading' } | { status: 'error'; message: string } | { status: 'ready'; data: HomeData }

const SEE_ALL: Record<string, string> = {
  'recent-movies': '/movies',
  'recent-series': '/series',
  'recently-played': '/music',
  reading: '/books',
}

/** The shelves that show titles still downloading, and of which kinds. */
const PENDING: Record<string, CardKind[]> = { 'recent-movies': ['movie'], 'recent-series': ['series'] }

/** The shelves that carry positions: «Продолжить» (an online card's link resumes at its second) and «Смотрят дома» (other profiles' progress, which they may hide at any time). */
const POSITION_SHELVES = new Set(['continue', 'family'])

/**
 * What is kept of an answer for the next visit (state/lastAnswer): nothing
 * that carries a position. Those shelves, and any card that would resume
 * at a point, come only with the fresh answer; the rest (the hero, the
 * library's shelves) links to pages that ask the server themselves.
 */
export function positionFree(data: HomeData): HomeData {
  return {
    ...data,
    shelves: data.shelves
      .filter((s) => !POSITION_SHELVES.has(s.id))
      .map((s) => ({ ...s, cards: s.cards.filter((c) => !c.online && !c.resumeSec) })),
  }
}

function shelfVariant(s: ShelfData) {
  if (s.id === 'continue' || s.id === 'family') return 'wide'
  if (s.id === 'recently-played') return 'square'
  return s.cards[0] && isSquareKind(s.cards[0].kind) ? 'square' : 'poster'
}

export function Home() {
  const t = useT()
  const { user } = useSession()
  const { scope } = useLibraryScope()
  // back to the tab: its last answer at once, then a quiet refresh (state/lastAnswer)
  const answerKey = `home ${scope}`
  const [state, setState] = useState<State>(() => {
    const hit = lastAnswer<HomeData>(answerKey)
    return hit ? { status: 'ready', data: hit } : { status: 'loading' }
  })
  const revisit = useRef(state.status === 'ready').current
  const abortRef = useRef<AbortController | null>(null)
  const pending = usePendingCards()

  // kept: the refresh of a kept answer (a revisit); when it fails the kept
  // answer is forgotten and the error shows, as on a first visit
  const load = useCallback((quiet = false, kept = false) => {
    abortRef.current?.abort()
    const ac = new AbortController()
    abortRef.current = ac
    if (!quiet) setState({ status: 'loading' })
    const slot = answerSlot(`home ${scope}`)
    getHome(ac.signal, scope)
      .then((data) => {
        rememberAnswer(slot, positionFree(data))
        setState({ status: 'ready', data })
      })
      .catch((e: unknown) => {
        if (ac.signal.aborted) return
        // A background refresh never replaces content fetched in this visit.
        if (quiet && !kept) return
        if (kept) forgetAnswer(`home ${scope}`)
        setState({ status: 'error', message: errorText(e, tr('browse.home.loadFailed')) })
      })
  }, [scope])

  useEffect(() => {
    const hit = lastAnswer<HomeData>(answerKey)
    if (hit) setState((s) => (s.status === 'ready' && s.data === hit ? s : { status: 'ready', data: hit }))
    load(!!hit, !!hit)
    return () => abortRef.current?.abort()
  }, [load, answerKey])

  useEvents(() => load(true), ['library.updated'])

  useEffect(() => {
    document.title = t('browse.home.documentTitle', { app: APP_NAME })
  }, [t])

  if (state.status === 'loading') return <HomeSkeleton />

  if (state.status === 'error') {
    return (
      <div className={styles.empty} role="alert">
        <Icon name="cloud" size={44} strokeWidth={1.5} />
        <h1>{t('browse.home.loadFailed')}</h1>
        <p>{state.message}</p>
        <Button variant="secondary" onClick={() => load()}>
          {t('common.retry')}
        </Button>
      </div>
    )
  }

  const { hero, shelves } = state.data
  const filled = shelves
    .map((s) => (PENDING[s.id] ? { ...s, cards: withPending(s.cards, pending, PENDING[s.id]!) } : s))
    .filter((s) => s.cards.length > 0)

  if (hero.length === 0 && filled.length === 0 && scope === 'mine') return <MineEmpty className={styles.empty} heading="h1" />

  if (hero.length === 0 && filled.length === 0) {
    return (
      <div className={styles.empty}>
        <Icon name="film" size={48} strokeWidth={1.4} />
        <h1>{t('browse.home.empty')}</h1>
        {user?.isAdmin ? (
          <>
            <p>{t(__LIBRARY_EDITION__ ? 'browse.home.emptyAdmin' : 'browse.home.emptyAdminFull')}</p>
            {__LIBRARY_EDITION__ ? (
              <RescanLink />
            ) : (
              <ButtonLink to="/search" icon="search">
                {t('browse.home.findMovie')}
              </ButtonLink>
            )}
          </>
        ) : (
          <p>{t('browse.home.emptyOthers')}</p>
        )}
      </div>
    )
  }

  return (
    <div className={styles.home}>
      <h1 className="visually-hidden">{t('common.home')}</h1>
      {hero.length > 0 ? <Hero items={hero} /> : <div className={styles.navSpacer} />}
      {/* the shelves fade in under the hero (the hero's picture is the page's
          first paint: never held back); a revisit draws them as they were */}
      <div className={[styles.shelves, hero.length > 0 ? styles.overlap : '', revisit ? '' : 'enter-fade'].join(' ')}>
        <ScreenTimeChip />
        {filled.map((s, i) => (
          <Shelf
            key={s.id}
            title={s.title}
            cards={s.cards}
            variant={shelfVariant(s)}
            seeAllTo={SEE_ALL[s.id]}
            priority={i === 0}
          />
        ))}
      </div>
    </div>
  )
}

function HomeSkeleton() {
  const t = useT()
  return (
    <div className={styles.home} aria-busy="true" aria-label={t('common.loading')}>
      <div className={styles.heroSkeleton} />
      <div className={`${styles.shelves} ${styles.overlap}`}>
        {[0, 1].map((r) => (
          <div key={r} className={styles.skeletonShelf}>
            <div className={styles.skeletonTitle} />
            <div className={styles.skeletonRow}>
              {Array.from({ length: 8 }, (_, i) => (
                <div key={i} className={styles.skeletonCard} />
              ))}
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}
