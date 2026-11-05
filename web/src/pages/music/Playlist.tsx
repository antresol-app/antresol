// A playlist: the cover mosaic, the name (renamed in place), «Слушать» /
// «Перемешать», the songs (each menu can remove or move one; positions are
// the stored `idx`) and «Удалить плейлист» behind a confirmation. A playlist
// being imported also shows the songs not downloaded yet, dimmed, in their
// places («Ещё не скачано»).
//
// Edits are optimistic and go to the server one at a time: every PATCH
// renumbers the stored positions, so the local list renumbers `idx` the same
// way and each request is built from the state the previous one left. A
// failed edit drops the queued ones and reloads the server's list.
import { APP_NAME } from '../../brand'
import { useCallback, useEffect, useRef, useState } from 'react'
import { useNavigate, useParams } from 'react-router'
import { ApiError, deletePlaylist, getPlaylist, getPlaylistPending, updatePlaylist, useEvents } from '../../api/client'
import type { PendingEntry, PlaylistDetail, PlaylistPatch, PlaylistTrack } from '../../api/types'
import { Button } from '../../ui/Button'
import { IconButton } from '../../ui/IconButton'
import { Icon } from '../../components/Icon'
import { MenuItem } from '../../ui/Menu'
import { Sheet } from '../../ui/Sheet'
import { audio } from '../../player/audio/engine'
import { songItems } from '../../player/audio/queue'
import { humanMinutes } from '../../player/timeline'
import { PlaylistCover } from './PlaylistCover'
import { SongMenu } from './SongMenu'
import { TrackList } from './TrackList'
import styles from './Playlist.module.css'
import { t as tr, useT } from '../../i18n'

type State = { status: 'loading' } | { status: 'error'; message: string } | { status: 'ready'; data: PlaylistDetail }

/** «12 песен, 46 мин». */
export function playlistFooter(d: PlaylistDetail): string {
  const ms = d.tracks.reduce((s, t) => s + t.durationMs, 0)
  const minutes = humanMinutes(ms / 60000) || (ms > 0 ? tr('music.oneMinute') : '')
  return [tr('music.songCount', { count: d.tracks.length }), minutes].filter(Boolean).join(', ')
}

export function Playlist() {
  const t = useT()
  const { id = '' } = useParams()
  const playlistId = Number(id)
  const navigate = useNavigate()
  const [state, setState] = useState<State>({ status: 'loading' })
  const [renaming, setRenaming] = useState(false)
  const [name, setName] = useState('')
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const abortRef = useRef<AbortController | null>(null)
  const nameRef = useRef<HTMLInputElement>(null)
  /** The chain of PATCH requests (one at a time). */
  const chain = useRef<Promise<void>>(Promise.resolve())
  const pending = useRef(0)
  /** Bumped by a failed edit: edits queued before it are dropped. */
  const generation = useRef(0)
  /** The rename was already saved or cancelled (Enter, then the blur of the unmounting input). */
  const renameDone = useRef(false)

  const [notYet, setNotYet] = useState<PendingEntry[]>([])

  const load = useCallback(
    (quiet = false) => {
      abortRef.current?.abort()
      const ac = new AbortController()
      abortRef.current = ac
      if (!quiet) setState({ status: 'loading' })
      // the import's songs not in the playlist yet (or not in the library); none when it fails
      getPlaylistPending(playlistId, ac.signal).then(
        (p) => setNotYet(p),
        () => {
          if (!ac.signal.aborted) setNotYet([])
        },
      )
      getPlaylist(playlistId, ac.signal)
        .then((data) => setState({ status: 'ready', data }))
        .catch((e: unknown) => {
          if (ac.signal.aborted || quiet) return
          setState({
            status: 'error',
            message: e instanceof ApiError ? (e.status === 404 ? t('music.playlist.notFound') : e.message) : t('music.playlist.loadFailed'),
          })
        })
    },
    [playlistId],
  )

  useEffect(() => {
    load()
    return () => abortRef.current?.abort()
  }, [load])
  useEvents(() => load(true), ['library.updated'])

  const title = state.status === 'ready' ? state.data.playlist.name : ''
  useEffect(() => {
    document.title = `${title || t('common.kind.playlist')} — ${APP_NAME}`
  }, [title])
  useEffect(() => {
    if (renaming) nameRef.current?.select()
  }, [renaming])

  /** Queues a PATCH after the ones in flight. */
  const edit = (patch: PlaylistPatch) => {
    setError('')
    const gen = generation.current
    pending.current++
    chain.current = chain.current
      .then(async () => {
        if (gen !== generation.current) return
        await updatePlaylist(playlistId, patch)
        // in step with the server: refresh covers and counts once idle
        if (pending.current === 1 && gen === generation.current) load(true)
      })
      .catch((e: unknown) => {
        generation.current++
        setError(
          e instanceof ApiError && e.status !== 0
            ? t('music.playlist.editFailedWith', { message: e.message })
            : t('music.playlist.editFailed'),
        )
        load(true)
      })
      .finally(() => {
        pending.current--
      })
  }

  if (state.status === 'loading')
    return (
      <div className={styles.page} aria-busy="true" aria-label={t('common.loading')}>
        <div className={styles.top}>
          <div className={`${styles.art} ${styles.skeleton}`} />
        </div>
      </div>
    )
  if (state.status === 'error')
    return (
      <div className={`${styles.page} ${styles.empty}`} role="alert">
        <Icon name="music" size={44} strokeWidth={1.5} />
        <h1>{t('music.playlist.openFailed')}</h1>
        <p>{state.message}</p>
        <Button variant="secondary" onClick={() => load()}>
          {t('music.retry')}
        </Button>
      </div>
    )

  const d = state.data
  const p = d.playlist
  const tracks = d.tracks
  const empty = tracks.length === 0
  const listen = () => audio.play(songItems(tracks), 0, { shuffle: false })
  const shuffle = () => audio.play(songItems(tracks), Math.floor(Math.random() * tracks.length), { shuffle: true })

  const saveName = () => {
    if (renameDone.current) return
    renameDone.current = true
    const n = name.trim()
    setRenaming(false)
    if (n && n !== p.name) {
      setState({ status: 'ready', data: { ...d, playlist: { ...p, name: n } } })
      edit({ name: n })
    }
  }

  const setTracks = (next: PlaylistTrack[]) => setState({ status: 'ready', data: { ...d, tracks: next } })

  /** Swaps rows i and i+by; the stored positions stay ascending in display order, as the server stores them. */
  const move = (i: number, by: -1 | 1) => {
    const j = i + by
    if (j < 0 || j >= tracks.length) return
    const order = tracks.map((t) => t.idx)
    ;[order[i], order[j]] = [order[j]!, order[i]!]
    const next = [...tracks]
    next[i] = { ...tracks[j]!, idx: tracks[i]!.idx }
    next[j] = { ...tracks[i]!, idx: tracks[j]!.idx }
    setTracks(next)
    edit({ order })
  }

  /** Drops row i; later stored positions shift down by one, as on the server. */
  const remove = (i: number) => {
    const gone = tracks[i]!.idx
    setTracks(tracks.filter((_, k) => k !== i).map((t) => (t.idx > gone ? { ...t, idx: t.idx - 1 } : t)))
    edit({ remove: [gone] })
  }

  const doDelete = () => {
    setBusy(true)
    deletePlaylist(playlistId)
      .then(() => navigate('/music/playlists', { replace: true }))
      .catch((e: unknown) => {
        setBusy(false)
        setConfirmDelete(false)
        setError(e instanceof ApiError ? e.message : t('music.playlist.deleteFailed'))
      })
  }

  return (
    <div className={styles.page}>
      <header className={styles.top}>
        <div className={styles.art}>
          <PlaylistCover covers={p.covers} name={p.name} />
        </div>
        <div className={styles.info}>
          <span className={styles.eyebrow}>{t('common.kind.playlist')}</span>
          {renaming ? (
            <form
              className={styles.renameForm}
              onSubmit={(e) => {
                e.preventDefault()
                saveName()
              }}
            >
              <input
                ref={nameRef}
                className={styles.renameInput}
                aria-label={t('common.title')}
                value={name}
                maxLength={200}
                onChange={(e) => setName(e.target.value)}
                onBlur={saveName}
                onKeyDown={(e) => {
                  if (e.key === 'Escape') {
                    e.preventDefault()
                    renameDone.current = true
                    setRenaming(false)
                  }
                }}
              />
            </form>
          ) : (
            <h1 className={styles.title}>
              <button
                type="button"
                className={styles.titleButton}
                title={t('common.rename')}
                aria-description={t('music.playlist.renameHint')}
                onClick={() => {
                  setName(p.name)
                  renameDone.current = false
                  setRenaming(true)
                }}
              >
                {p.name}
                <span className={styles.pencil} aria-hidden>
                  <Icon name="pencil" size={18} />
                </span>
              </button>
            </h1>
          )}
          <p className={styles.meta}>{playlistFooter(d)}</p>
          <div className={styles.actions}>
            <Button icon="play" onClick={listen} disabled={empty} className={styles.action}>
              {t('common.listen')}
            </Button>
            <Button variant="secondary" icon="shuffle" onClick={shuffle} disabled={empty} className={styles.action}>
              {t('music.shuffle')}
            </Button>
            <IconButton icon="trash" label={t('music.playlist.delete')} onClick={() => setConfirmDelete(true)} />
          </div>
          {error && (
            <p className={styles.error} role="alert">
              {error}
            </p>
          )}
        </div>
      </header>

      {empty && notYet.length === 0 ? (
        <div className={styles.emptyList}>
          <p>{t('music.playlist.empty')}</p>
          <p className={styles.hint}>{t('music.playlist.emptyHint')}</p>
        </div>
      ) : (
        <TrackList
          tracks={tracks}
          albumArtist=""
          variant="songs"
          pending={notYet}
          label={t('music.playlist.songsLabel', { name: p.name })}
          onPlay={(i) => audio.play(songItems(tracks), i)}
          menu={(tk, i) => (
            <SongMenu
              song={tk}
              onAdded={(to) => to.id === playlistId && load(true)}
              extra={(close) => (
                <>
                  {i > 0 && (
                    <MenuItem
                      label={t('music.playlist.moveUp')}
                      icon={<Icon name="arrowUp" size={17} />}
                      onSelect={() => {
                        close()
                        move(i, -1)
                      }}
                    />
                  )}
                  {i < tracks.length - 1 && (
                    <MenuItem
                      label={t('music.playlist.moveDown')}
                      icon={<Icon name="arrowDown" size={17} />}
                      onSelect={() => {
                        close()
                        move(i, 1)
                      }}
                    />
                  )}
                  <MenuItem
                    label={t('music.playlist.removeFrom')}
                    icon={<Icon name="trash" size={17} />}
                    onSelect={() => {
                      close()
                      remove(i)
                    }}
                  />
                </>
              )}
            />
          )}
        />
      )}

      {confirmDelete && (
        <Sheet
          title={t('music.playlist.deleteConfirm', { name: p.name })}
          message={t('music.playlist.deleteMessage')}
          busy={busy}
          onCancel={() => setConfirmDelete(false)}
          actions={[
            { label: t('common.cancelAction'), kind: 'cancel', run: () => setConfirmDelete(false) },
            { label: t('common.delete'), kind: 'destructive', run: doDelete },
          ]}
        />
      )}
    </div>
  )
}
