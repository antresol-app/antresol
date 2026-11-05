// /read/:id — the full-window reader (outside the Shell, so the music engine
// keeps playing). Loads the book, downloads its reading file (EPUB > FB2 >
// MOBI > AZW3 > PDF) and hands it to EpubReader, or PdfReader for PDF.
import { APP_NAME } from '../brand'
import { lazy, Suspense, useCallback, useEffect, useState } from 'react'
import { Navigate, useLocation, useNavigate, useParams } from 'react-router'
import { ApiError, getBook, getFile } from '../api/client'
import type { BookDetail } from '../api/types'
import { Button } from '../ui/Button'
import { Icon } from '../components/Icon'
import { bookFile } from './bookFile'
import styles from './Reader.module.css'
import { useT } from '../i18n'

const EpubReader = lazy(() => import('./EpubReader').then((m) => ({ default: m.EpubReader })))
const PdfReader = lazy(() => import('./PdfReader').then((m) => ({ default: m.PdfReader })))

type State =
  | { status: 'loading' }
  | { status: 'error'; message: string }
  | { status: 'ready'; book: BookDetail; file: File | null }

/** The reading file's format (EPUB, FB2, PDF…). */
export function readFormat(b: BookDetail): string {
  return (b.files.find((f) => f.id === b.readFileId)?.format ?? '').toUpperCase()
}

export function Reader() {
  const t = useT()
  const { id = '' } = useParams()
  const bookId = Number(id)
  const navigate = useNavigate()
  const location = useLocation()
  const [state, setState] = useState<State>({ status: 'loading' })
  const [attempt, setAttempt] = useState(0)

  useEffect(() => {
    const ac = new AbortController()
    setState({ status: 'loading' })
    ;(async () => {
      const book = await getBook(bookId, ac.signal)
      if (book.kind !== 'book' || !book.readFileId) return setState({ status: 'ready', book, file: null })
      const format = readFormat(book)
      if (format === 'PDF') return setState({ status: 'ready', book, file: null })
      const blob = await getFile(book.readFileId, ac.signal)
      setState({ status: 'ready', book, file: await bookFile(format, blob) })
    })().catch((e: unknown) => {
      if (ac.signal.aborted) return
      setState({
        status: 'error',
        message: e instanceof ApiError ? (e.status === 404 ? t('books.notInLibrary') : e.message) : t('books.loadFailed'),
      })
    })
    return () => ac.abort()
  }, [bookId, attempt])

  // sheets over the book are opaque (a backdrop filter over its iframe shows the text through)
  useEffect(() => {
    document.body.classList.add(styles.reading ?? 'reading')
    return () => document.body.classList.remove(styles.reading ?? 'reading')
  }, [])

  const title = state.status === 'ready' ? state.book.card.title : ''
  useEffect(() => {
    document.title = `${title || t('reader.title')} — ${APP_NAME}`
  }, [title, t])

  // Back to where the reader was opened from (the book page), or to it.
  const exit = useCallback(() => {
    if (location.key !== 'default') navigate(-1)
    else navigate(`/books/${bookId}`, { replace: true })
  }, [location.key, navigate, bookId])

  if (state.status === 'loading') {
    return (
      <div className={`${styles.reader} ${styles.blank}`} aria-busy="true" aria-label={t('reader.openingBook')}>
        <span className={styles.spinner} />
      </div>
    )
  }

  if (state.status === 'error') {
    return (
      <div className={`${styles.reader} ${styles.blank}`} role="alert">
        <div className={styles.overlay}>
          <Icon name="book" size={44} strokeWidth={1.4} />
          <h2>{t('books.book.openFailed')}</h2>
          <p>{state.message}</p>
          <div className={styles.row}>
            <Button variant="secondary" onClick={exit}>
              {t('common.back')}
            </Button>
            <Button onClick={() => setAttempt((n) => n + 1)}>{t('common.retry')}</Button>
          </div>
        </div>
      </div>
    )
  }

  const { book, file } = state
  if (book.kind === 'audiobook') return <Navigate to={`/books/audiobook/${book.card.id}`} replace />
  if (book.kind === 'comic') return <Navigate to={`/books/comic/${book.card.id}`} replace />
  if (book.kind !== 'book' || !book.readFileId) {
    return (
      <div className={`${styles.reader} ${styles.blank}`} role="alert">
        <div className={styles.overlay}>
          <Icon name="book" size={44} strokeWidth={1.4} />
          <h2>{t('reader.cantOpenHere')}</h2>
          <p>{t('reader.cantOpenHereHint')}</p>
          <Button variant="secondary" onClick={exit}>
            {t('common.back')}
          </Button>
        </div>
      </div>
    )
  }

  return (
    <Suspense
      fallback={
        <div className={`${styles.reader} ${styles.blank}`} aria-busy="true" aria-label={t('reader.openingBook')}>
          <span className={styles.spinner} />
        </div>
      }
    >
      {file ? <EpubReader book={book} file={file} onExit={exit} /> : <PdfReader book={book} onExit={exit} />}
    </Suspense>
  )
}
