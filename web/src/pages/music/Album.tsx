// An album: cover on the left (on top on phones), title, artist, «Рок ·
// 1988 · Lossless», «Слушать» / «Перемешать» / «На это устройство» («Сохранить
// офлайн», ZIP) and the track list. Playing a track queues the whole album from it.
import { APP_NAME } from '../../brand'
import { useCallback, useEffect, useRef, useState } from 'react'
import { Link, useParams } from 'react-router'
import { ApiError, albumZipUrl, getAlbum, imageUrl, useEvents } from '../../api/client'
import type { AlbumDetail } from '../../api/types'
import { Button } from '../../ui/Button'
import { IconButton } from '../../ui/IconButton'
import { Icon } from '../../components/Icon'
import { MenuItem, MenuSeparator, Menu } from '../../ui/Menu'
import { OfflineMenuItems, OfflineStatus } from '../../offline/OfflineMenu'
import { audio } from '../../player/audio/engine'
import { songItems } from '../../player/audio/queue'
import { humanMinutes } from '../../player/timeline'
import { useIsCompact } from '../../state/useMediaQuery'
import { useSession } from '../../state/session'
import { DeleteItem, DeleteItemSheet, useLeaveDeleted } from '../../components/DeleteItem'
import { TrackList } from './TrackList'
import styles from './Album.module.css'
import { t as tr, useT } from '../../i18n'

type State = { status: 'loading' } | { status: 'error'; message: string } | { status: 'ready'; album: AlbumDetail }

/** A year from file tags can be garbage (1 for a zero date): only 1000-2999 is shown. */
export function plausibleYear(y: number | undefined): number | undefined {
  return typeof y === 'number' && Number.isInteger(y) && y >= 1000 && y <= 2999 ? y : undefined
}

/** «Рок · 1988 · Lossless». */
export function albumMeta(a: AlbumDetail): string {
  return [a.genres[0], plausibleYear(a.year), a.quality.split(' · ')[0]].filter(Boolean).join(' · ')
}

/** «12 песен, 46 мин». */
export function albumFooter(a: AlbumDetail): string {
  const ms = a.durationMs || a.tracks.reduce((s, t) => s + t.durationMs, 0)
  const minutes = humanMinutes(ms / 60000) || (ms > 0 ? tr('music.oneMinute') : '')
  return [tr('music.songCount', { count: a.tracks.length }), minutes].filter(Boolean).join(', ')
}

export function Album() {
  const t = useT()
  const { id = '' } = useParams()
  const albumId = Number(id)
  const compact = useIsCompact()
  const { user } = useSession()
  const leave = useLeaveDeleted()
  const [state, setState] = useState<State>({ status: 'loading' })
  const [coverFailed, setCoverFailed] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const abortRef = useRef<AbortController | null>(null)

  const load = useCallback(
    (quiet = false) => {
      abortRef.current?.abort()
      const ac = new AbortController()
      abortRef.current = ac
      if (!quiet) setState({ status: 'loading' })
      getAlbum(albumId, ac.signal)
        .then((album) => setState({ status: 'ready', album }))
        .catch((e: unknown) => {
          if (ac.signal.aborted || quiet) return
          setState({
            status: 'error',
            message: e instanceof ApiError ? (e.status === 404 ? t('music.album.notFound') : e.message) : t('music.album.loadFailed'),
          })
        })
    },
    [albumId],
  )

  useEffect(() => {
    load()
    return () => abortRef.current?.abort()
  }, [load])

  useEvents(() => load(true), ['library.updated'])

  const title = state.status === 'ready' ? state.album.card.title : ''
  useEffect(() => {
    document.title = `${title || t('common.kind.album')} — ${APP_NAME}`
  }, [title])

  if (state.status === 'loading') {
    return (
      <div className={styles.page} aria-busy="true" aria-label={t('common.loading')}>
        <div className={styles.top}>
          <div className={`${styles.art} ${styles.skeleton}`} />
        </div>
      </div>
    )
  }

  if (state.status === 'error') {
    return (
      <div className={`${styles.page} ${styles.empty}`} role="alert">
        <Icon name="music" size={44} strokeWidth={1.5} />
        <h1>{t('music.album.openFailed')}</h1>
        <p>{state.message}</p>
        <Button variant="secondary" onClick={() => load()}>
          {t('music.retry')}
        </Button>
      </div>
    )
  }

  const a = state.album
  const cover = imageUrl(a.card.poster, 960)
  const meta = albumMeta(a)
  const items = () => songItems(a.tracks)
  const playFrom = (i: number) => audio.play(items(), i)
  const listen = () => audio.play(items(), 0, { shuffle: false })
  const shuffle = () => audio.play(items(), Math.floor(Math.random() * a.tracks.length), { shuffle: true })
  const empty = a.tracks.length === 0

  // «На это устройство»: offline copies and the ZIP (its own menu on a wide screen, in «⋯» on a phone)
  const toDevice = (close: (focus?: boolean) => void) => (
    <>
      {!empty && <OfflineMenuItems target={{ kind: 'album', id: a.card.id }} close={close} />}
      {!empty && <MenuSeparator />}
      <MenuItem label={t('music.album.downloadZip')} detail={t('music.album.downloadZipDetail')} href={albumZipUrl(a.card.id)} download onSelect={() => close(false)} />
    </>
  )

  return (
    <div className={styles.page}>
      <header className={styles.top}>
        <div className={styles.art}>
          {cover && !coverFailed ? (
            <img src={cover} alt="" decoding="async" draggable={false} onError={() => setCoverFailed(true)} />
          ) : (
            <span className={styles.artFallback}>
              <Icon name="music" size={56} strokeWidth={1.3} />
            </span>
          )}
        </div>
        <div className={styles.info}>
          <h1 className={styles.title}>{a.card.title}</h1>
          {a.artist.name &&
            (a.artist.id > 0 ? (
              <Link to={`/music/artist/${a.artist.id}`} className={styles.artist}>
                {a.artist.name}
              </Link>
            ) : (
              <span className={styles.artist}>{a.artist.name}</span>
            ))}
          {meta && <p className={styles.meta}>{meta}</p>}
          <div className={styles.actions}>
            <Button icon="play" onClick={listen} disabled={empty} className={styles.action}>
              {t('common.listen')}
            </Button>
            <Button variant="secondary" icon="shuffle" onClick={shuffle} disabled={empty} className={styles.action}>
              {t('music.shuffle')}
            </Button>
            {!compact && (
              <Menu
                label={t('common.toDevice')}
                placement="bottom-start"
                trigger={(p) => (
                  <Button {...p} variant="secondary" icon="download">
                    {t('common.toDevice')}
                  </Button>
                )}
              >
                {(close) => toDevice(close)}
              </Menu>
            )}
            {!compact && user?.isAdmin && !user.isKid && <DeleteItem itemId={a.card.id} title={a.card.title} onDeleted={() => leave('/music/albums')} />}
          </div>
          {/* a phone: two capsules, everything else in «⋯» at the header's
              corner, delete last (as on a title page) */}
          {compact && (
            <div className={styles.more}>
            <Menu
              label={t('music.album.actions', { title: a.card.title })}
              placement="bottom-end"
              trigger={(p) => <IconButton {...p} icon="ellipsis" label={t('music.album.actions', { title: a.card.title })} />}
            >
              {(close) => (
                <>
                  {toDevice(close)}
                  {user?.isAdmin && !user.isKid && (
                    <>
                      <MenuSeparator />
                      <MenuItem
                        destructive
                        icon={<Icon name="trash" size={16} />}
                        label={t('comp.delete.menu')}
                        onSelect={() => (close(false), setDeleting(true))}
                      />
                    </>
                  )}
                </>
              )}
            </Menu>
            </div>
          )}
          {deleting && <DeleteItemSheet itemId={a.card.id} title={a.card.title} onClose={() => setDeleting(false)} onDeleted={() => leave('/music/albums')} />}
          <OfflineStatus target={{ kind: 'album', id: a.card.id }} />
        </div>
      </header>

      {!empty && <TrackList tracks={a.tracks} albumArtist={a.artist.name} onPlay={playFrom} />}
      <p className={styles.footer}>{albumFooter(a)}</p>
    </div>
  )
}
