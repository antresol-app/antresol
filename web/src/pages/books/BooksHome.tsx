// «Книги»: shelves of book covers from /api/books/home — «Читаю сейчас»
// (with the read fraction), «Хочу прочитать», «Недавно добавленные»,
// «Аудиокниги», «Манга и комиксы», «Прочитано». Refreshes when the library
// changes.
import { APP_NAME } from '../../brand'
import { useCallback, useEffect, useRef, useState } from 'react'
import { getBooksHome, useEvents, errorText } from '../../api/client'
import type { BooksHome as BooksHomeData } from '../../api/types'
import { ButtonLink } from '../../ui/Button'
import { EmptyState } from '../../ui/EmptyState'
import { Skeleton } from '../../ui/Skeleton'
import { RescanLink } from '../../components/RescanLink'
import { EmptyStateActions } from '../../components/EmptyStateActions'
import { Icon } from '../../components/Icon'
import { LoadFailed } from '../../components/LoadFailed'
import { LibraryTools, MineEmpty } from '../../components/LibraryScopeMenu'
import { BookDropZone, BookUploadButton, BookUploadStatus, useBookUpload } from './BookUpload'
import { Shelf } from '../../components/Shelf'
import { answerSlot, forgetAnswer, lastAnswer, rememberAnswer } from '../../state/lastAnswer'
import { useLibraryScope } from '../../state/libraryScope'
import { shelvesWithPending, usePendingCards } from '../../state/pendingCards'
import { useSession } from '../../state/session'
import styles from './BooksHome.module.css'
import { useT } from '../../i18n'
import { MediaSwitch } from '../../components/MediaSwitch'

type State = { status: 'loading' } | { status: 'error'; message: string } | { status: 'ready'; data: BooksHomeData }

const SEE_ALL: Record<string, string> = {
  recent: '/books/all/book',
  audiobooks: '/books/all/audiobook',
  comics: '/books/all/comic',
}

const LINKS = [
  { to: '/books/all/book', label: 'books.allBooks' },
  { to: '/books/all/audiobook', label: 'common.audiobooks' },
  { to: '/books/all/comic', label: 'common.mangaComics' },
] as const

export function BooksHome() {
  const t = useT()
  const { user } = useSession()
  const { scope } = useLibraryScope()
  // back to the tab: its last answer at once, then a quiet refresh (state/lastAnswer)
  const answerKey = `books ${scope}`
  const [state, setState] = useState<State>(() => {
    const hit = lastAnswer<BooksHomeData>(answerKey)
    return hit ? { status: 'ready', data: hit } : { status: 'loading' }
  })
  const abortRef = useRef<AbortController | null>(null)
  const pending = usePendingCards()

  // kept: the refresh of a kept answer (a revisit); when it fails the kept
  // answer is forgotten and the error shows, as on a first visit
  const load = useCallback((quiet = false, kept = false) => {
    abortRef.current?.abort()
    const ac = new AbortController()
    abortRef.current = ac
    if (!quiet) setState({ status: 'loading' })
    const slot = answerSlot(`books ${scope}`)
    getBooksHome(ac.signal, scope)
      .then((data) => {
        rememberAnswer(slot, data)
        setState({ status: 'ready', data })
      })
      .catch((e: unknown) => {
        if (ac.signal.aborted || (quiet && !kept)) return
        if (kept) forgetAnswer(`books ${scope}`)
        setState({ status: 'error', message: errorText(e, t('books.homeLoadFailed')) })
      })
  }, [t, scope])

  useEffect(() => {
    const hit = lastAnswer<BooksHomeData>(answerKey)
    if (hit) setState((s) => (s.status === 'ready' && s.data === hit ? s : { status: 'ready', data: hit }))
    load(!!hit, !!hit)
    return () => abortRef.current?.abort()
  }, [load, answerKey])

  useEvents(() => load(true), ['library.updated'])
  const canUpload = !user?.isKid
  const { upload, progress, result } = useBookUpload(() => load(true))

  useEffect(() => {
    document.title = `${t('common.books')} — ${APP_NAME}`
  }, [t])

  const shelves =
    state.status === 'ready'
      ? shelvesWithPending(
          state.data.shelves,
          pending,
          [
            { id: 'recent', kinds: ['book'], title: t('browse.sort.recent') },
            { id: 'audiobooks', kinds: ['audiobook'], title: t('common.audiobooks') },
            { id: 'comics', kinds: ['comic'], title: t('common.mangaComics') },
          ],
          ['reading', 'want'],
        ).filter((s) => s.cards.length > 0)
      : []

  return (
    <BookDropZone enabled={canUpload} onFiles={(f) => void upload(f)}>
    <div className={styles.page}>
      <MediaSwitch value="/books" />
      <header className={styles.header}>
        <h1 className={styles.title}>{t('common.books')}</h1>
        <div className={styles.headerEnd}>
          {shelves.length > 0 && (
            <nav className={styles.links} aria-label={t('common.library')}>
              {LINKS.map((l) => (
                <ButtonLink key={l.to} to={l.to} variant="plain" size="small">
                  {t(l.label)}
                </ButtonLink>
              ))}
            </nav>
          )}
          <div className={styles.tools}>
            {canUpload && <BookUploadButton onFiles={(f) => void upload(f)} busy={progress !== null} />}
            <LibraryTools />
          </div>
        </div>
      </header>
      <BookUploadStatus progress={progress} result={result} />

      {state.status === 'loading' && (
        <div className={styles.skeleton} aria-busy="true" aria-label={t('common.loading')}>
          {[0, 1].map((r) => (
            <div key={r}>
              <Skeleton tone="soft" className={styles.skeletonTitle} />
              <div className={styles.skeletonRow}>
                {Array.from({ length: 7 }, (_, i) => (
                  <Skeleton key={i} className={styles.skeletonCard} />
                ))}
              </div>
            </div>
          ))}
        </div>
      )}

      {state.status === 'error' && (
        <LoadFailed inset title={t('books.homeLoadFailed')} message={state.message} retry={t('common.retry')} onRetry={() => load()} />
      )}

      {state.status === 'ready' && shelves.length === 0 && scope === 'mine' && <MineEmpty inset />}

      {state.status === 'ready' && shelves.length === 0 && scope !== 'mine' && (
        <EmptyState
          inset
          icon={<Icon name="book" size={48} strokeWidth={1.4} />}
          title={t('books.empty.title')}
          hint={user?.isKid ? t('books.empty.kid') : t('books.empty.hint')}
          actions={
            !user?.isKid && (
              <>
                <RescanLink variant="secondary" />
                <EmptyStateActions scope="book" variant="secondary" />
              </>
            )
          }
        />
      )}

      {shelves.length > 0 && (
        <div className={styles.shelves}>
          {shelves.map((s, i) => (
            <Shelf key={s.id} title={s.title} cards={s.cards} seeAllTo={SEE_ALL[s.id]} priority={i === 0} />
          ))}
        </div>
      )}
    </div>
    </BookDropZone>
  )
}
