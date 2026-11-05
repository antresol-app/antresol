// «Книги»: shelves of book covers from /api/books/home — «Читаю сейчас»
// (with the read fraction), «Хочу прочитать», «Недавно добавленные»,
// «Аудиокниги», «Манга и комиксы», «Прочитано». Refreshes when the library
// changes.
import { APP_NAME } from '../../brand'
import { useCallback, useEffect, useRef, useState } from 'react'
import { ApiError, getBooksHome, useEvents } from '../../api/client'
import type { BooksHome as BooksHomeData } from '../../api/types'
import { Button, ButtonLink } from '../../ui/Button'
import { RescanLink } from '../../components/RescanLink'
import { Icon } from '../../components/Icon'
import { LibraryTools, MineEmpty } from '../../components/LibraryScopeMenu'
import { BookDropZone, BookUploadButton, BookUploadStatus, useBookUpload } from './BookUpload'
import { Shelf } from '../../components/Shelf'
import { answerSlot, forgetAnswer, lastAnswer, rememberAnswer } from '../../state/lastAnswer'
import { useLibraryScope } from '../../state/libraryScope'
import { shelvesWithPending, usePendingCards } from '../../state/pendingCards'
import { useSession } from '../../state/session'
import styles from './BooksHome.module.css'
import { useT } from '../../i18n'

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
        setState({ status: 'error', message: e instanceof ApiError ? e.message : t('books.homeLoadFailed') })
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
          <h2>{t('books.homeLoadFailed')}</h2>
          <p>{state.message}</p>
          <Button variant="secondary" onClick={() => load()}>
            {t('common.retry')}
          </Button>
        </div>
      )}

      {state.status === 'ready' && shelves.length === 0 && scope === 'mine' && <MineEmpty className={styles.empty} />}

      {state.status === 'ready' && shelves.length === 0 && scope !== 'mine' && (
        <div className={styles.empty}>
          <Icon name="book" size={48} strokeWidth={1.4} />
          <h2>{t('books.empty.title')}</h2>
          {user?.isKid ? (
            <p>{t('books.empty.kid')}</p>
          ) : (
            <>
              <p>{t(__LIBRARY_EDITION__ ? 'books.empty.hint' : 'books.empty.hintFull')}</p>
              {__LIBRARY_EDITION__ ? (
                <RescanLink variant="secondary" />
              ) : (
                <ButtonLink to="/search?dk=book" icon="search" variant="secondary">
                  {t('books.empty.find')}
                </ButtonLink>
              )}
            </>
          )}
        </div>
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
