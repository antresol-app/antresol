// An audiobook: square cover, title, author, «Читает: …», the length, then
// «Слушать» / «Продолжить · осталось 5 ч 12 мин», «Хочу прочитать», the
// speed, the sleep timer (while it plays), «На это устройство» (the files)
// and the chapters: number, title, length; the current one is marked and a
// click plays from the start of a chapter.
import { Artwork } from '../../ui/Artwork'
import { APP_NAME } from '../../brand'
import { useCallback, useEffect, useRef, useState } from 'react'
import { Navigate, useParams } from 'react-router'
import { addToWatchlist, ApiError, downloadUrl, getBook, removeFromWatchlist, useEvents, errorText } from '../../api/client'
import type { BookDetail } from '../../api/types'
import { formatSize } from '../../components/ActionBar'
import { Button, ButtonLink } from '../../ui/Button'
import { IconButton } from '../../ui/IconButton'
import { Icon } from '../../components/Icon'
import { MenuItem, MenuSeparator, Menu } from '../../ui/Menu'
import { OfflineMenuItems, OfflineStatus } from '../../offline/OfflineMenu'
import { bookHeard, bookPositionMs, bookResumeMs, chapterIndexAt, playBook, seekAbsolute } from '../../player/audio/audiobook'
import { audio } from '../../player/audio/engine'
import { useAudioSelect } from '../../player/audio/useAudio'
import { SleepMenu } from '../../player/SleepMenu'
import { SpeedMenu } from '../../player/SpeedMenu'
import { clock, humanMinutes } from '../../player/timeline'
import { useIsCompact } from '../../state/useMediaQuery'
import { Badges } from '../../ui/Badge'
import { FamilyProgress, useFamily } from '../../components/FamilyProgress'
import { useSession } from '../../state/session'
import { useScreenLimits } from '../../state/screenTime'
import { DeleteItem, useLeaveDeleted } from '../../components/DeleteItem'
import styles from './AudiobookDetail.module.css'
import { useT } from '../../i18n'

type State = { status: 'loading' } | { status: 'error'; message: string; gone?: boolean } | { status: 'ready'; book: BookDetail }

/** «5 ч 12 мин» left of `totalMs` after `posMs` (at least «1 мин»). */
export function leftLabel(totalMs: number, posMs: number): string {
  return humanMinutes(Math.max(1, (totalMs - posMs) / 60_000))
}

/** Long descriptions start folded to a few lines. */
const FOLD_CHARS = 280

export function AudiobookDetail() {
  const t = useT()
  const { id = '' } = useParams()
  const bookId = Number(id)
  const compact = useIsCompact()
  const [state, setState] = useState<State>({ status: 'loading' })
  const [expanded, setExpanded] = useState(false)
  const [busy, setBusy] = useState(false)
  const [actionError, setActionError] = useState('')
  const abortRef = useRef<AbortController | null>(null)
  const { user } = useSession()
  const limits = useScreenLimits(Boolean(user?.isKid))
  const leave = useLeaveDeleted()
  const family = useFamily(bookId, !user?.isKid)

  // this book in the engine: playing or paused, and where (whole seconds)
  const loaded = useAudioSelect((s) => s.kind === 'audiobook' && s.bookId === bookId && s.queue.length > 0)
  const playing = useAudioSelect((s) => s.kind === 'audiobook' && s.bookId === bookId && s.playing)
  const liveMs = useAudioSelect((s) => (s.kind === 'audiobook' && s.bookId === bookId ? Math.floor(bookPositionMs(s) / 1000) * 1000 : -1))

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
            gone: e instanceof ApiError && e.status === 404,
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
    document.title = `${title || t('common.kind.audiobook')} — ${APP_NAME}`
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
        <Icon name="headphones" size={44} strokeWidth={1.5} />
        <h1>{t('books.audiobook.openFailed')}</h1>
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
  if (b.kind === 'comic') return <Navigate to={`/books/comic/${b.card.id}`} replace />
  if (b.kind === 'comic_volume') return <Navigate to={`/read/${b.card.id}`} replace />

  const total = b.durationMs || b.files.reduce((n, f) => n + f.durationMs, 0)
  const saved = bookResumeMs(b)
  const pos = loaded && liveMs >= 0 ? liveMs : saved
  const started = loaded || saved > 0
  const current = started && b.chapters.length > 0 ? chapterIndexAt(b.chapters, pos) : -1
  const length = humanMinutes(total / 60_000)
  const meta = [length, b.year > 0 ? String(b.year) : ''].filter(Boolean).join(' · ')
  const long = b.description.length > FOLD_CHARS
  const canPlay = b.files.length > 0

  const listen = () => {
    if (loaded) audio.toggle()
    else playBook(b, saved)
  }
  // heard to the end: say that it starts over instead of doing it silently
  const again = !loaded && bookHeard(b)
  const label = playing ? t('common.pause') : again ? t('books.audiobook.startOver') : started && pos > 0 ? t('books.audiobook.resumeLeft', { time: leftLabel(total, pos) }) : t('common.listen')

  const playChapter = (startMs: number) => {
    if (loaded) {
      seekAbsolute(startMs)
      if (!audio.get().playing) audio.toggle()
    } else playBook(b, startMs)
  }

  const toggleWant = () => {
    const next = !b.want
    setBusy(true)
    setActionError('')
    setState({ status: 'ready', book: { ...b, want: next } })
    ;(next ? addToWatchlist(b.card.id) : removeFromWatchlist(b.card.id))
      .catch((e: unknown) => {
        setState((s) => (s.status === 'ready' ? { status: 'ready', book: { ...s.book, want: !next } } : s))
        setActionError(errorText(e, t('books.listChangeFailed')))
      })
      .finally(() => setBusy(false))
  }

  return (
    <div className={styles.page}>
      <header className={styles.top}>
        <div className={styles.cover}>
          <Artwork src={b.card.poster} width={300} sizes="clamp(220px, 22vw, 300px)" eager
              fallback={
            <span className={styles.coverFallback}>
              <Icon name="headphones" size={40} strokeWidth={1.4} />
              <span>{b.card.title}</span>
            </span>
              }
            />
        </div>
        <div className={styles.info}>
          <p className={styles.eyebrow}>{t('common.kind.audiobook')}</p>
          <h1 className={styles.title}>{b.card.title}</h1>
          {b.authors.length > 0 && <p className={styles.authors}>{b.authors.join(', ')}</p>}
          {b.narrator && <p className={styles.narrator}>{t('books.audiobook.narrator', { name: b.narrator })}</p>}
          {meta && <p className={styles.meta}>{meta}</p>}
          <Badges items={b.card.badges} />
          <div className={styles.actions}>
            {canPlay && (
              <Button icon={playing ? 'pause' : again ? 'restart' : 'play'} className={styles.listen} onClick={listen}>
                {label}
              </Button>
            )}
            <Button variant="secondary" icon={b.want ? 'check' : 'plus'} aria-pressed={b.want} className={styles.want} disabled={busy} onClick={toggleWant}>
              {b.want ? t('books.inList') : t('books.wantToRead')}
            </Button>
            {canPlay && <SpeedMenu />}
            {loaded && <SleepMenu placement="bottom-start" />}
            {b.files.length > 0 && (
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
                {(close) => (
                  <>
                    <OfflineMenuItems target={{ kind: 'audiobook', id: b.card.id }} close={close} />
                    <MenuSeparator />
                    {/* a kid whose audio is limited does not download («Экранное время») */}
                    {!limits.audio && b.files.map((f, i) => (
                      <MenuItem
                        key={f.id}
                        label={b.files.length > 1 ? t('books.audiobook.fileN', { n: i + 1, format: f.format || t('books.audiobook.audio') }) : f.format || t('common.kind.file')}
                        detail={
                          [f.size > 0 ? formatSize(f.size) : '', f.durationMs > 0 ? clock(f.durationMs / 1000) : ''].filter(Boolean).join(' · ') || undefined
                        }
                        href={downloadUrl(f.id)}
                        download
                        onSelect={() => close(false)}
                      />
                    ))}
                  </>
                )}
              </Menu>
            )}
            {user?.isAdmin && !user.isKid && b.files.length > 0 && (
              <DeleteItem
                itemId={b.card.id}
                title={b.card.title}
                onDeleted={() => {
                  if (loaded) audio.stop() // its files are gone
                  leave('/books')
                }}
              />
            )}
          </div>
          <FamilyProgress
            members={family}
            kind="audiobook"
            chapters={b.chapters}
            onOpen={(m) => {
              // the position is this profile's now: play from it
              if (loaded) {
                seekAbsolute(m.positionMs)
                if (!audio.get().playing) audio.toggle()
              } else playBook(b, m.positionMs)
              load(true)
            }}
          />
          <OfflineStatus target={{ kind: 'audiobook', id: b.card.id }} />
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

      {b.chapters.length > 0 && (
        <section className={styles.chapters} aria-labelledby="chapters-heading">
          <h2 id="chapters-heading" className={styles.heading}>
            {t('books.audiobook.chapters')}
          </h2>
          <ol className={styles.list} aria-label={t('books.audiobook.chapters')}>
            {b.chapters.map((c, i) => {
              const now = i === current
              return (
                <li key={c.idx}>
                  <button
                    type="button"
                    className={`${styles.row} ${now ? styles.now : ''}`}
                    aria-current={now ? 'true' : undefined}
                    onClick={() => playChapter(c.startMs)}
                  >
                    <span className="visually-hidden">{now ? t('books.audiobook.chapterNow') : t('books.audiobook.chapterListen')}</span>
                    <span className={styles.num} aria-hidden>
                      {now && playing ? <Icon name="speaker" size={16} /> : i + 1}
                    </span>
                    <span className={styles.name}>{c.title}</span>
                    <span className={styles.dur}>{clock(Math.max(0, c.endMs - c.startMs) / 1000)}</span>
                  </button>
                </li>
              )
            })}
          </ol>
        </section>
      )}
    </div>
  )
}
