// A book: the cover like a printed book on the left (on top on phones),
// title, authors, «Классика · книга 2», «EPUB · 2,4 МБ · русский», then
// «Читать» / «Продолжить · 34%», «Хочу прочитать», «Отметить как
// прочитанное», «На это устройство» («Сохранить офлайн», one item per format) and the
// description. Audiobooks and comics have their own pages.
import { Artwork } from '../../ui/Artwork'
import { APP_NAME } from '../../brand'
import { useCallback, useEffect, useRef, useState } from 'react'
import { Navigate, useNavigate, useParams } from 'react-router'
import { addToWatchlist, ApiError, downloadUrl, getBook, removeFromWatchlist, setWatched, useEvents, errorText } from '../../api/client'
import type { BookDetail as BookDetailData } from '../../api/types'
import { formatSize } from '../../components/ActionBar'
import { Button, ButtonLink } from '../../ui/Button'
import { IconButton } from '../../ui/IconButton'
import { Icon } from '../../components/Icon'
import { MenuItem, MenuSeparator, Menu } from '../../ui/Menu'
import { OfflineMenuItems, OfflineStatus } from '../../offline/OfflineMenu'
import { discardProgress } from '../../offline/store'
import { useIsCompact } from '../../state/useMediaQuery'
import { Badges } from '../../ui/Badge'
import { FamilyProgress, useFamily } from '../../components/FamilyProgress'
import { useSession } from '../../state/session'
import { KindleButton } from './KindleButton'
import { DeleteItem, useLeaveDeleted } from '../../components/DeleteItem'
import styles from './BookDetail.module.css'
import { locale, t, useT } from '../../i18n'

type State = { status: 'loading' } | { status: 'error'; message: string } | { status: 'ready'; book: BookDetailData }


/** «Классика · книга 2». */
export function seriesLine(b: BookDetailData): string {
  if (!b.series?.name) return ''
  return b.series.index > 0 ? t('books.book.seriesIndex', { series: b.series.name, n: new Intl.NumberFormat(locale(), { maximumFractionDigits: 2 }).format(b.series.index) }) : b.series.name
}

/** «русский» for «ru»; the code itself when the browser does not know it. */
export function languageName(code: string): string {
  if (!code) return ''
  try {
    return new Intl.DisplayNames([locale()], { type: 'language' }).of(code) ?? code
  } catch {
    return code
  }
}

/** «EPUB · 2,4 МБ · русский» for the file the reader opens. */
export function metaLine(b: BookDetailData): string {
  const f = b.files.find((x) => x.id === b.readFileId) ?? b.files[0]
  return [f?.format, f && f.size > 0 ? formatSize(f.size) : '', languageName(b.lang)].filter(Boolean).join(' · ')
}

/** Long descriptions start folded to a few lines. */
const FOLD_CHARS = 280

export function BookDetail() {
  const t = useT()
  const { id = '' } = useParams()
  const bookId = Number(id)
  const compact = useIsCompact()
  const [state, setState] = useState<State>({ status: 'loading' })
  const [expanded, setExpanded] = useState(false)
  const [busy, setBusy] = useState<'want' | 'read' | null>(null)
  const [actionError, setActionError] = useState('')
  const abortRef = useRef<AbortController | null>(null)
  const navigate = useNavigate()
  const { user } = useSession()
  const leave = useLeaveDeleted()
  const family = useFamily(bookId, !user?.isKid)

  const load = useCallback(
    (quiet = false) => {
      abortRef.current?.abort()
      const ac = new AbortController()
      abortRef.current = ac
      if (!quiet) setState({ status: 'loading' })
      getBook(bookId, ac.signal)
        .then((book) => setState({ status: 'ready', book }))
        .catch((e: unknown) => {
          if (ac.signal.aborted || quiet) return
          setState({
            status: 'error',
            message: e instanceof ApiError ? (e.status === 404 ? t('books.notInLibrary') : e.message) : t('books.loadFailed'),
          })
        })
    },
    [bookId, t],
  )

  useEffect(() => {
    load()
    return () => abortRef.current?.abort()
  }, [load])

  useEvents(() => load(true), ['library.updated'])

  const title = state.status === 'ready' ? state.book.card.title : ''
  useEffect(() => {
    document.title = `${title || t('common.kind.book')} — ${APP_NAME}`
  }, [title, t])

  if (state.status === 'loading') {
    return (
      <div className={styles.page} aria-busy="true" aria-label={t('common.loading')}>
        <div className={styles.top}>
          <div className={`${styles.cover} ${styles.skeleton}`} />
        </div>
      </div>
    )
  }

  if (state.status === 'error') {
    return (
      <div className={`${styles.page} ${styles.empty}`} role="alert">
        <Icon name="book" size={44} strokeWidth={1.5} />
        <h1>{t('books.book.openFailed')}</h1>
        <p>{state.message}</p>
        <Button variant="secondary" onClick={() => load()}>
          {t('common.retry')}
        </Button>
      </div>
    )
  }

  const b = state.book
  if (b.kind === 'audiobook') return <Navigate to={`/books/audiobook/${b.card.id}`} replace />
  if (b.kind === 'comic') return <Navigate to={`/books/comic/${b.card.id}`} replace />
  if (b.kind === 'comic_volume') return <Navigate to={`/read/${b.card.id}`} replace />

  const pct = b.progress && !b.progress.finished ? Math.round(b.progress.fraction * 100) : 0
  const finished = !!b.progress?.finished
  const series = seriesLine(b)
  const meta = metaLine(b)
  const long = b.description.length > FOLD_CHARS
  const update = (patch: Partial<BookDetailData>) =>
    setState((s) => (s.status === 'ready' ? { status: 'ready', book: { ...s.book, ...patch } } : s))

  const toggleWant = () => {
    const next = !b.want
    setBusy('want')
    setActionError('')
    update({ want: next })
    ;(next ? addToWatchlist(b.card.id) : removeFromWatchlist(b.card.id))
      .catch((e: unknown) => {
        update({ want: !next })
        setActionError(errorText(e, t('books.listChangeFailed')))
      })
      .finally(() => setBusy(null))
  }

  const toggleRead = () => {
    setBusy('read')
    setActionError('')
    const markedAt = Date.now()
    setWatched(b.card.id, !finished)
      .then(() => {
        discardProgress([b.card.id], markedAt)
        return load(true)
      })
      .catch((e: unknown) => setActionError(errorText(e, t('books.book.markFailed'))))
      .finally(() => setBusy(null))
  }

  return (
    <div className={styles.page}>
      <header className={styles.top}>
        <div className={styles.cover}>
          <Artwork src={b.card.poster} width={280} sizes="clamp(200px, 20vw, 280px)" eager
              fallback={
            <span className={styles.coverFallback}>
              <span>{b.card.title}</span>
            </span>
              }
            />
        </div>
        <div className={styles.info}>
          {series && <p className={styles.series}>{series}</p>}
          <h1 className={styles.title}>{b.card.title}</h1>
          {b.authors.length > 0 && <p className={styles.authors}>{b.authors.join(', ')}</p>}
          {meta && <p className={styles.meta}>{meta}</p>}
          <Badges items={b.card.badges} />

          <div className={styles.actions}>
            {b.readFileId > 0 && (
              <ButtonLink to={`/read/${b.card.id}`} icon="book" className={styles.read}>
                {b.progress?.locator && !finished ? t('books.book.resumePercent', { pct: Math.max(1, pct) }) : t('common.read')}
              </ButtonLink>
            )}
            <Button
              variant="secondary"
              icon={b.want ? 'check' : 'plus'}
              aria-pressed={b.want}
              className={styles.want}
              disabled={busy === 'want'}
              onClick={toggleWant}
            >
              {b.want ? t('books.inList') : t('books.wantToRead')}
            </Button>
            <Button variant="secondary" icon={finished ? 'restart' : 'checkCircle'} className={styles.mark} loading={busy === 'read'} onClick={toggleRead}>
              {finished ? t('books.book.markUnread') : t('books.book.markRead')}
            </Button>
            {b.files.length > 0 && (
              <Menu
                label={t('common.toDevice')}
                placement={compact ? 'bottom-end' : 'bottom-start'}
                trigger={(p) =>
                  compact ? (
                    <IconButton {...p} icon="download" label={t('common.toDevice')} />
                  ) : (
                    <Button {...p} variant="secondary" icon="download">
                      {t('common.toDevice')}
                    </Button>
                  )
                }
              >
                {(close) => (
                  <>
                    {b.readFileId > 0 && <OfflineMenuItems target={{ kind: 'book', id: b.card.id }} close={close} />}
                    {b.readFileId > 0 && <MenuSeparator />}
                    {b.files.map((f) => (
                      <MenuItem
                        key={f.id}
                        label={f.format || t('common.kind.file')}
                        detail={f.size > 0 ? formatSize(f.size) : undefined}
                        href={downloadUrl(f.id)}
                        download
                        onSelect={() => close(false)}
                      />
                    ))}
                  </>
                )}
              </Menu>
            )}
            {b.canSendToKindle && <KindleButton bookId={b.card.id} sent={!!b.sentToKindle} compact={compact} onError={setActionError} />}
            {user?.isAdmin && !user.isKid && b.files.length > 0 && (
              <DeleteItem itemId={b.card.id} title={b.card.title} onDeleted={() => leave('/books')} />
            )}
          </div>
          <FamilyProgress members={family} kind="book" onOpen={(m) => navigate(`/read/${m.itemId}`)} />
          <OfflineStatus target={{ kind: 'book', id: b.card.id }} />
          {actionError && (
            <p className={styles.error} role="alert">
              {actionError}
            </p>
          )}
        </div>
      </header>

      {b.description && (
        <section className={styles.about} aria-label={t('books.description')}>
          <p className={`${styles.description} ${long && !expanded ? styles.folded : ''}`}>{b.description}</p>
          {long && (
            <Button variant="link" className={styles.more} aria-expanded={expanded} onClick={() => setExpanded(!expanded)}>
              {expanded ? t('common.collapse') : t('common.more')}
            </Button>
          )}
        </section>
      )}
    </div>
  )
}
