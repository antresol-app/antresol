// What is saved on this device. Without the server it is the whole app
// (the Gate shows it instead of «Нет связи»): a banner, shelves «Музыка»,
// «Книги», «Аудиокниги», «Манга»; music and audiobooks play here, books and
// volumes open in the reader. With the server it is /offline, the list of
// downloads with the space they take.
import { APP_NAME } from '../brand'
import { useCallback, useEffect, useState } from 'react'
import { useNavigate } from 'react-router'
import { ApiError, getAlbum, getBook } from '../api/client'
import { formatSize } from '../components/ActionBar'
import { Button } from '../ui/Button'
import { Icon } from '../components/Icon'
import { LogoMark } from '../components/Logo'
import { OfflineShelf } from '../offline/OfflineShelf'
import { offlineBytes, savedBytes, useOfflineList, type OfflineEntry } from '../offline/store'
import { MiniPlayer } from '../player/MiniPlayer'
import { NowPlaying } from '../player/NowPlaying'
import { playBook } from '../player/audio/audiobook'
import { audio } from '../player/audio/engine'
import { songItems } from '../player/audio/queue'
import { nextVolume } from './books/ComicSeries'
import { session, useSession } from '../state/session'
import styles from './OfflineHome.module.css'
import { useT, type Key } from '../i18n'

const SHELVES: { title: Key; kind: OfflineEntry['kind'] }[] = [
  { title: 'common.music', kind: 'album' },
  { title: 'common.books', kind: 'book' },
  { title: 'common.audiobooks', kind: 'audiobook' },
  { title: 'common.kind.manga', kind: 'comic' },
]

export function OfflineHome() {
  const { status } = useSession()
  const t = useT()
  const offline = status !== 'signed-in'
  const list = useOfflineList()
  const navigate = useNavigate()
  const [bytes, setBytes] = useState<number | null>(null)
  const [busy, setBusy] = useState<string | null>(null)
  const [error, setError] = useState('')
  const [retrying, setRetrying] = useState(false)
  const [full, setFull] = useState(false)
  const closeFull = useCallback(() => setFull(false), [])

  useEffect(() => {
    let live = true
    void offlineBytes().then((b) => live && setBytes(b))
    return () => {
      live = false
    }
  }, [list])

  useEffect(() => {
    document.title = offline ? t('app.offlineHome.docTitleOffline', { app: APP_NAME }) : t('app.offlineHome.docTitle', { app: APP_NAME })
  }, [offline, t])

  const open = async (e: OfflineEntry) => {
    setError('')
    if (!offline) {
      const to = { album: `/music/album/${e.id}`, book: `/books/${e.id}`, audiobook: `/books/audiobook/${e.id}`, comic: `/books/comic/${e.id}` }[e.kind]
      navigate(to)
      return
    }
    if (e.kind === 'book') {
      navigate(`/read/${e.id}`)
      return
    }
    setBusy(`${e.kind}:${e.id}`)
    try {
      if (e.kind === 'album') {
        const a = await getAlbum(e.id)
        audio.play(songItems(a.tracks), 0, { shuffle: false })
      } else {
        const b = await getBook(e.id)
        if (e.kind === 'audiobook') playBook(b)
        else {
          const next = nextVolume(b.volumes)
          if (next) navigate(`/read/${next.volume.id}`)
        }
      }
    } catch (err) {
      setError(err instanceof ApiError && err.status !== 0 ? err.message : t('app.offlineHome.openFailed'))
    } finally {
      setBusy(null)
    }
  }

  const retry = () => {
    setRetrying(true)
    void session.bootstrap().finally(() => setRetrying(false))
  }

  const shelves = SHELVES.map((s) => (
    <OfflineShelf key={s.kind} title={t(s.title)} entries={list.filter((e) => e.kind === s.kind)} onOpen={(e) => void open(e)} busy={busy} />
  ))
  // the saved items themselves; the browser's figure also counts the app
  const saved = list.length > 0 ? savedBytes() : 0
  const used = saved > 0 ? [t('app.offlineHome.saved', { size: formatSize(saved) }), bytes ? t('app.offlineHome.appOnDevice', { app: APP_NAME, size: formatSize(bytes) }) : ''].filter(Boolean).join(' · ') : ''

  if (!offline) {
    return (
      <div className={styles.page}>
        <h1 className={styles.heading}>{t('app.offlineHome.title')}</h1>
        <p className={styles.used}>{used || t('app.offlineHome.hint')}</p>
        {list.length === 0 ? (
          <div className={styles.empty}>
            <Icon name="download" size={44} strokeWidth={1.4} />
            <h2>{t('app.offlineHome.nothingSaved')}</h2>
            <p>{t('app.offlineHome.nothingSavedHint')}</p>
          </div>
        ) : (
          shelves
        )}
      </div>
    )
  }

  return (
    <div className={styles.offline}>
      <header className={styles.brand}>
        <LogoMark size={28} />
        <span>{APP_NAME}</span>
      </header>
      <div className={styles.page}>
        <div className={`${styles.banner} glass`} role="status">
          <Icon name="cloud" size={22} strokeWidth={1.8} />
          <p>{t('app.offlineHome.banner')}</p>
          <Button variant="secondary" size="small" loading={retrying} onClick={retry}>
            {t('common.retry')}
          </Button>
        </div>
        {error && (
          <p className={styles.error} role="alert">
            {error}
          </p>
        )}
        {list.length === 0 ? (
          <div className={styles.empty}>
            <Icon name="cloud" size={44} strokeWidth={1.4} />
            <h2>{t('app.offlineHome.nothingOnDevice')}</h2>
            <p>{t('app.offline.hint')}</p>
          </div>
        ) : (
          shelves
        )}
        {used && <p className={styles.used}>{used}</p>}
      </div>
      <MiniPlayer onExpand={() => setFull(true)} />
      <NowPlaying open={full} onClose={closeFull} />
    </div>
  )
}
