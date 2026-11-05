// /read/:id: a comic volume opens in ComicReader, everything else in the
// book Reader (EPUB, FB2, PDF), which loads the book itself. The Reader is
// mounted right away (hidden until the kind is known), so books open as
// fast as before; for a volume it is replaced by ComicReader.
import { APP_NAME } from '../brand'
import { lazy, Suspense, useCallback, useEffect, useState } from 'react'
import { useLocation, useNavigate, useParams } from 'react-router'
import { ApiError, getBook } from '../api/client'
import { Button } from '../ui/Button'
import { Icon } from '../components/Icon'
import { GONE_TEXT, GONE_TITLE } from './comicText'
import type { BookDetail } from '../api/types'
import styles from './ComicReader.module.css'
import { useT } from '../i18n'

// both start loading with the book's kind, not after it
const loadReader = () => import('./Reader')
const loadComic = () => import('./ComicReader')
const Reader = lazy(() => loadReader().then((m) => ({ default: m.Reader })))
const ComicReader = lazy(() => loadComic().then((m) => ({ default: m.ComicReader })))

type State = { status: 'loading' } | { status: 'comic'; book: BookDetail } | { status: 'book' } | { status: 'gone' }

function Blank() {
  const t = useT()
  return (
    <div className={`${styles.reader} ${styles.blank}`} aria-busy="true" aria-label={t('reader.openingBook')}>
      <span className={styles.spinner} />
    </div>
  )
}

export function ReadRoute() {
  const t = useT()
  const { id = '' } = useParams()
  const bookId = Number(id)
  const navigate = useNavigate()
  const location = useLocation()
  const [state, setState] = useState<State>({ status: 'loading' })

  useEffect(() => {
    const ac = new AbortController()
    setState({ status: 'loading' })
    void loadReader().catch(() => {})
    void loadComic().catch(() => {})
    getBook(bookId, ac.signal)
      .then((book) => setState(book.kind === 'comic_volume' ? { status: 'comic', book } : { status: 'book' }))
      .catch((e: unknown) => {
        if (ac.signal.aborted) return
        // removed or hidden for this profile: nothing to retry; other errors: the book reader shows them with «Повторить»
        setState(e instanceof ApiError && e.status === 404 ? { status: 'gone' } : { status: 'book' })
      })
    return () => ac.abort()
  }, [bookId])

  const seriesId = state.status === 'comic' ? state.book.seriesId : 0
  // back to where the volume was opened from, or to its series
  const exit = useCallback(() => {
    if (location.key !== 'default') navigate(-1)
    else
      navigate(seriesId ? `/books/comic/${seriesId}` : '/books', {
        replace: true,
      })
  }, [location.key, navigate, seriesId])

  const title = state.status === 'comic' ? [state.book.series?.name, state.book.card.subtitle].filter(Boolean).join(' · ') : ''
  useEffect(() => {
    if (title) document.title = `${title} — ${APP_NAME}`
  }, [title])

  if (state.status === 'gone') {
    return (
      <div className={`${styles.reader} ${styles.blank}`} role="alert">
        <div className={styles.overlay}>
          <Icon name="book" size={44} strokeWidth={1.4} />
          <h2>{t(GONE_TITLE)}</h2>
          <p>{t(GONE_TEXT)}</p>
          <Button variant="secondary" onClick={exit}>
            {t('common.back')}
          </Button>
        </div>
      </div>
    )
  }
  if (state.status === 'comic') {
    return (
      <Suspense fallback={<Blank />}>
        <ComicReader key={state.book.card.id} book={state.book} onExit={exit} />
      </Suspense>
    )
  }
  // The book reader starts at once (its own loading takes as long as
  // before); it stays invisible until the volume check says it is a book.
  return (
    <>
      {state.status === 'loading' && <Blank />}
      <div style={state.status === 'loading' ? { visibility: 'hidden' } : undefined}>
        <Suspense fallback={null}>
          <Reader key={bookId} />
        </Suspense>
      </div>
    </>
  )
}
