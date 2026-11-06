// «Добавить в плейлист»: the user's playlists (a tap appends the songs) and
// «Новый плейлист…», which turns the sheet into a name field.
import { useEffect, useState } from 'react'
import { createPlaylist, getPlaylists, updatePlaylist, errorText } from '../../api/client'
import type { Playlist } from '../../api/types'
import { Icon } from '../../components/Icon'
import { Sheet } from '../../ui/Sheet'
import { TextField } from '../../ui/TextField'
import { PlaylistCover } from './PlaylistCover'
import styles from './AddToPlaylistSheet.module.css'
import { useT } from '../../i18n'

export interface AddToPlaylistSheetProps {
  trackIds: number[]
  onClose: () => void
  /** After the songs were added (or the playlist created). */
  onDone?: (p: Playlist) => void
}

type List = { status: 'loading' } | { status: 'error'; message: string } | { status: 'ready'; items: Playlist[] }

const errText = (e: unknown, fallback: string) => errorText(e, fallback)

export function AddToPlaylistSheet({ trackIds, onClose, onDone }: AddToPlaylistSheetProps) {
  const t = useT()
  const [list, setList] = useState<List>({ status: 'loading' })
  const [creating, setCreating] = useState(false)
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    const ac = new AbortController()
    getPlaylists(ac.signal)
      .then((items) => setList({ status: 'ready', items }))
      .catch((e: unknown) => !ac.signal.aborted && setList({ status: 'error', message: errText(e, t('music.playlists.loadFailed')) }))
    return () => ac.abort()
  }, [])

  const addTo = (p: Playlist) => {
    if (busy) return
    setBusy(true)
    setError(null)
    updatePlaylist(p.id, { append: trackIds })
      .then((res) => {
        onDone?.(res)
        onClose()
      })
      .catch((e: unknown) => {
        setBusy(false)
        setError(errText(e, t('music.addSongFailed')))
      })
  }

  const create = () => {
    const n = name.trim()
    if (!n) {
      setError(t('music.enterPlaylistName'))
      return
    }
    setBusy(true)
    setError(null)
    createPlaylist(n, trackIds)
      .then((res) => {
        onDone?.(res)
        onClose()
      })
      .catch((e: unknown) => {
        setBusy(false)
        setError(errText(e, t('music.createPlaylistFailed')))
      })
  }

  if (creating)
    return (
      <Sheet
        title={t('music.newPlaylist')}
        role="dialog"
        size="wide"
        busy={busy}
        onCancel={onClose}
        actions={[
          { label: t('common.cancelAction'), kind: 'cancel', run: onClose },
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
    )

  return (
    <Sheet
      title={t('music.addToPlaylist')}
      role="dialog"
      size="wide"
      dismissOnOutside
      onCancel={onClose}
      actions={[{ label: t('common.cancelAction'), kind: 'cancel', run: onClose }]}
    >
      <ul className={styles.list}>
        <li>
          <button type="button" className={styles.row} onClick={() => setCreating(true)} data-autofocus>
            <span className={`${styles.thumb} ${styles.plus}`}>
              <Icon name="plus" size={22} />
            </span>
            <span className={styles.name}>{t('music.newPlaylistMenu')}</span>
          </button>
        </li>
        {list.status === 'ready' &&
          list.items.map((p) => (
            <li key={p.id}>
              <button type="button" className={styles.row} disabled={busy} onClick={() => addTo(p)}>
                <PlaylistCover covers={p.covers} name={p.name} className={styles.thumb} />
                <span className={styles.text}>
                  <span className={styles.name}>{p.name}</span>
                  <span className={styles.count}>{t('music.songCount', { count: p.trackCount })}</span>
                </span>
              </button>
            </li>
          ))}
      </ul>
      {list.status === 'loading' && <p className={styles.note}>{t('common.loadingEllipsis')}</p>}
      {list.status === 'error' && <p className={styles.note}>{list.message}</p>}
      {error && (
        <p className={styles.error} role="alert">
          {error}
        </p>
      )}
    </Sheet>
  )
}
