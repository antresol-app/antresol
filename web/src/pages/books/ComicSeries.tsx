// A manga or comic series: the series cover, title, author, «N томов»,
// «Читать» / «Продолжить: Том 3», «На это устройство» (every volume saved
// offline), then the volumes as a grid of covers with
// a read bar. A volume opens in the reader (/read/:id).
import { APP_NAME } from '../../brand'
import { useCallback, useEffect, useRef, useState } from 'react'
import { Link, Navigate, useNavigate, useParams } from 'react-router'
import { addToWatchlist, ApiError, getBook, imageUrl, removeFromWatchlist, useEvents } from '../../api/client'
import type { BookDetail, VolumeCard } from '../../api/types'
import { Button, ButtonLink } from '../../ui/Button'
import { IconButton } from '../../ui/IconButton'
import { Icon } from '../../components/Icon'
import { Menu } from '../../ui/Menu'
import { OfflineMenuItems, OfflineStatus } from '../../offline/OfflineMenu'
import { useIsCompact } from '../../state/useMediaQuery'
import { FamilyProgress, useFamily } from '../../components/FamilyProgress'
import { useSession } from '../../state/session'
import { DeleteItem, useLeaveDeleted } from '../../components/DeleteItem'
import styles from './ComicSeries.module.css'
import { formatPercent, useT } from '../../i18n'

type State = { status: 'loading' } | { status: 'error'; message: string; gone?: boolean } | { status: 'ready'; book: BookDetail }

/** A volume counts as read from this share on (the server's «прочитано»). */
const READ = 0.98

/**
 * The volume to open and whether it continues: the last one started and not
 * finished; else the one after the last finished; else the first.
 */
export function nextVolume(volumes: VolumeCard[]): { volume: VolumeCard; resume: boolean } | null {
  if (volumes.length === 0) return null
  for (let i = volumes.length - 1; i >= 0; i--) {
    const p = volumes[i]!.progress
    if (p > 0 && p < READ) return { volume: volumes[i]!, resume: true }
  }
  let last = -1
  volumes.forEach((v, i) => {
    if (v.progress >= READ) last = i
  })
  if (last >= 0 && last + 1 < volumes.length) return { volume: volumes[last + 1]!, resume: true }
  return { volume: volumes[0]!, resume: false }
}

const volumeName = (v: VolumeCard) => v.subtitle || v.title

function Volume({ v }: { v: VolumeCard }) {
  const t = useT()
  const [failed, setFailed] = useState(false)
  const src = imageUrl(v.poster, 480)
  const pct = Math.round(Math.min(1, Math.max(0, v.progress)) * 100)
  const read = v.progress >= READ
  return (
    <li>
      <Link to={`/read/${v.id}`} className={styles.volume}>
        <span className={styles.art}>
          {src && !failed ? (
            <img src={src} alt="" loading="lazy" decoding="async" draggable={false} onError={() => setFailed(true)} />
          ) : (
            <span className={styles.artFallback}>{volumeName(v)}</span>
          )}
          {pct > 0 && !read && (
            <span className={styles.bar} aria-hidden>
              <span style={{ width: `${pct}%` }} />
            </span>
          )}
        </span>
        <span className={styles.vname}>{volumeName(v)}</span>
        <span className={styles.vmeta}>
          {read ? t('books.comic.read') : pct > 0 ? formatPercent(pct) : v.pages > 0 ? t('books.comic.pages', { count: v.pages }) : ''}
        </span>
      </Link>
    </li>
  )
}

export function ComicSeries() {
  const t = useT()
  const { id = '' } = useParams()
  const seriesId = Number(id)
  const compact = useIsCompact()
  const [state, setState] = useState<State>({ status: 'loading' })
  const [coverFailed, setCoverFailed] = useState(false)
  const [busy, setBusy] = useState(false)
  const [actionError, setActionError] = useState('')
  const abortRef = useRef<AbortController | null>(null)
  const navigate = useNavigate()
  const { user } = useSession()
  const leave = useLeaveDeleted()
  const family = useFamily(seriesId, !user?.isKid)

  const load = useCallback(
    (quiet = false) => {
      abortRef.current?.abort()
      const ac = new AbortController()
      abortRef.current = ac
      if (!quiet) setState({ status: 'loading' })
      getBook(seriesId, ac.signal)
        .then((book) => setState({ status: 'ready', book }))
        .catch((e: unknown) => {
          if (ac.signal.aborted || quiet) return
          setState({
            status: 'error',
            gone: e instanceof ApiError && e.status === 404,
            message: e instanceof ApiError ? (e.status === 404 ? t('books.comic.notInLibrary') : e.message) : t('books.comic.loadFailed'),
          })
        })
    },
    [seriesId, t],
  )

  useEffect(() => {
    load()
    return () => abortRef.current?.abort()
  }, [load])

  useEvents(() => load(true), ['library.updated'])

  const title = state.status === 'ready' ? state.book.card.title : ''
  useEffect(() => {
    document.title = `${title || t('common.mangaComics')} — ${APP_NAME}`
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
        <h1>{t('books.comic.openFailed')}</h1>
        <p>{state.message}</p>
        {state.gone ? (
          <ButtonLink to="/books" variant="secondary">
            {t('books.toBooks')}
          </ButtonLink>
        ) : (
          <Button variant="secondary" onClick={() => load()}>
            {t('common.retry')}
          </Button>
        )}
      </div>
    )
  }

  const b = state.book
  if (b.kind === 'book') return <Navigate to={`/books/${b.card.id}`} replace />
  if (b.kind === 'audiobook') return <Navigate to={`/books/audiobook/${b.card.id}`} replace />
  if (b.kind === 'comic_volume') return <Navigate to={`/read/${b.card.id}`} replace />

  const cover = imageUrl(b.card.poster, 960)
  const next = nextVolume(b.volumes)
  const n = b.volumes.length

  const toggleWant = () => {
    const want = !b.want
    setBusy(true)
    setActionError('')
    setState({ status: 'ready', book: { ...b, want } })
    ;(want ? addToWatchlist(b.card.id) : removeFromWatchlist(b.card.id))
      .catch((e: unknown) => {
        setState((s) => (s.status === 'ready' ? { status: 'ready', book: { ...s.book, want: !want } } : s))
        setActionError(e instanceof ApiError ? e.message : t('books.listChangeFailed'))
      })
      .finally(() => setBusy(false))
  }

  return (
    <div className={styles.page}>
      <header className={styles.top}>
        <div className={styles.cover}>
          {cover && !coverFailed ? (
            <img src={cover} alt="" decoding="async" draggable={false} onError={() => setCoverFailed(true)} />
          ) : (
            <span className={styles.coverFallback}>
              <span>{b.card.title}</span>
            </span>
          )}
        </div>
        <div className={styles.info}>
          <p className={styles.eyebrow}>{b.direction === 'ltr' ? t('common.kind.comic') : t('common.kind.manga')}</p>
          <h1 className={styles.title}>{b.card.title}</h1>
          {b.authors.length > 0 && <p className={styles.authors}>{b.authors.join(', ')}</p>}
          <p className={styles.meta}>{[t('books.comic.volumes', { count: n }), b.year > 0 ? String(b.year) : ''].filter(Boolean).join(' · ')}</p>
          <div className={styles.actions}>
            {next && (
              <ButtonLink to={`/read/${next.volume.id}`} icon="book" className={styles.read}>
                {next.resume ? t('books.comic.continueVolume', { volume: volumeName(next.volume) }) : t('common.read')}
              </ButtonLink>
            )}
            <Button variant="secondary" icon={b.want ? 'check' : 'plus'} aria-pressed={b.want} className={styles.want} disabled={busy} onClick={toggleWant}>
              {b.want ? t('books.inList') : t('books.wantToRead')}
            </Button>
            {n > 0 && (
              <Menu
                label={t('common.toDevice')}
                placement="bottom-end"
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
                {(close) => <OfflineMenuItems target={{ kind: 'comic', id: b.card.id }} close={close} />}
              </Menu>
            )}
            {user?.isAdmin && !user.isKid && n > 0 && <DeleteItem itemId={b.card.id} title={b.card.title} onDeleted={() => leave('/books')} />}
          </div>
          <FamilyProgress members={family} kind="comic" onOpen={(m) => navigate(`/read/${m.itemId}`)} />
          <OfflineStatus target={{ kind: 'comic', id: b.card.id }} />
          {actionError && (
            <p className={styles.error} role="alert">
              {actionError}
            </p>
          )}
          {b.description && <p className={styles.description}>{b.description}</p>}
        </div>
      </header>

      {n > 0 && (
        <section aria-labelledby="volumes-heading">
          <h2 id="volumes-heading" className={styles.heading}>
            {t('books.comic.volumesHeading')}
          </h2>
          <ul className={styles.grid} aria-label={t('books.comic.volumesHeading')}>
            {b.volumes.map((v) => (
              <Volume key={v.id} v={v} />
            ))}
          </ul>
        </section>
      )}
    </div>
  )
}
