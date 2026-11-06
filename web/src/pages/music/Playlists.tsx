// «Плейлисты»: the profile's playlists as mosaic tiles and «Новый плейлист».
import { APP_NAME } from '../../brand'
import { useCallback, useEffect, useRef, useState } from 'react'
import { Link, useNavigate } from 'react-router'
import { createPlaylist, getPlaylists, errorText } from '../../api/client'
import type { Playlist } from '../../api/types'
import { Button } from '../../ui/Button'
import { Icon } from '../../components/Icon'
import { Sheet } from '../../ui/Sheet'
import { TextField } from '../../ui/TextField'
import { PlaylistCover } from './PlaylistCover'
import styles from './Playlists.module.css'
import { useT } from '../../i18n'

type State = { status: 'loading' } | { status: 'error'; message: string } | { status: 'ready'; items: Playlist[] }

export function Playlists() {
  const t = useT()
  const navigate = useNavigate()
  const [state, setState] = useState<State>({ status: 'loading' })
  const [creating, setCreating] = useState(false)
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const abortRef = useRef<AbortController | null>(null)

  const load = useCallback(() => {
    abortRef.current?.abort()
    const ac = new AbortController()
    abortRef.current = ac
    setState({ status: 'loading' })
    getPlaylists(ac.signal)
      .then((items) => setState({ status: 'ready', items }))
      .catch((e: unknown) => {
        if (!ac.signal.aborted) setState({ status: 'error', message: errorText(e, t('music.playlists.loadFailed')) })
      })
  }, [])

  useEffect(() => {
    load()
    return () => abortRef.current?.abort()
  }, [load])
  useEffect(() => {
    document.title = `${t('common.playlists')} — ${APP_NAME}`
  }, [])

  const create = () => {
    const n = name.trim()
    if (!n) return setError(t('music.enterPlaylistName'))
    setBusy(true)
    setError(null)
    createPlaylist(n, [])
      .then((p) => navigate(`/music/playlist/${p.id}`))
      .catch((e: unknown) => {
        setBusy(false)
        setError(errorText(e, t('music.createPlaylistFailed')))
      })
  }
  const closeSheet = () => {
    setCreating(false)
    setName('')
    setError(null)
  }

  return (
    <div className={styles.page}>
      <header className={styles.header}>
        <h1 className={styles.title}>{t('common.playlists')}</h1>
        <Button variant="secondary" size="small" icon="plus" onClick={() => setCreating(true)}>
          {t('music.newPlaylist')}
        </Button>
      </header>

      {state.status === 'loading' && (
        <ul className={styles.grid} aria-hidden>
          {Array.from({ length: 6 }, (_, i) => (
            <li key={i}>
              <div className={styles.skeleton} />
            </li>
          ))}
        </ul>
      )}
      {state.status === 'error' && (
        <div className={styles.empty} role="alert">
          <Icon name="cloud" size={44} strokeWidth={1.5} />
          <h2>{t('music.playlists.loadFailed')}</h2>
          <p>{state.message}</p>
          <Button variant="secondary" onClick={load}>
            {t('music.retry')}
          </Button>
        </div>
      )}
      {state.status === 'ready' && state.items.length === 0 && (
        <div className={styles.empty}>
          <Icon name="list" size={48} strokeWidth={1.4} />
          <h2>{t('music.playlists.empty')}</h2>
          <p>{t('music.playlists.emptyHint')}</p>
        </div>
      )}
      {state.status === 'ready' && state.items.length > 0 && (
        <ul className={styles.grid} aria-label={t('common.playlists')}>
          {state.items.map((p) => (
            <li key={p.id}>
              <Link to={`/music/playlist/${p.id}`} className={styles.tile}>
                <PlaylistCover covers={p.covers} name={p.name} />
                <span className={styles.name}>{p.name}</span>
                <span className={styles.count}>{t('music.songCount', { count: p.trackCount })}</span>
              </Link>
            </li>
          ))}
        </ul>
      )}

      {creating && (
        <Sheet
          title={t('music.newPlaylist')}
          role="dialog"
          size="wide"
          busy={busy}
          onCancel={closeSheet}
          actions={[
            { label: t('common.cancelAction'), kind: 'cancel', run: closeSheet },
            { label: t('common.create'), kind: 'primary', run: create },
          ]}
        >
          <TextField
            label={t('common.title')}
            className={styles.field}
            value={name}
            maxLength={200}
            autoComplete="off"
            autoFocus
            error={error}
            onChange={(e) => setName(e.target.value)}
          />
        </Sheet>
      )}
    </div>
  )
}
