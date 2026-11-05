// An artist: a round avatar and the name in large type, «Слушать» (the top
// songs), «Перемешать» (the whole catalogue, drawn by the server),
// «Популярные песни» (up to 10), «Альбомы» and «Встречается в».
import { APP_NAME } from '../../brand'
import { useCallback, useEffect, useRef, useState } from 'react'
import { useParams } from 'react-router'
import { ApiError, getArtist, imageUrl, useEvents } from '../../api/client'
import type { ArtistDetail } from '../../api/types'
import { Button } from '../../ui/Button'
import { Icon } from '../../components/Icon'
import { Shelf } from '../../components/Shelf'
import { audio } from '../../player/audio/engine'
import { songItems } from '../../player/audio/queue'
import { playShuffled } from './shuffle'
import { SongMenu } from './SongMenu'
import { TrackList } from './TrackList'
import styles from './Artist.module.css'
import { useT } from '../../i18n'

type State = { status: 'loading' } | { status: 'error'; message: string } | { status: 'ready'; data: ArtistDetail }

export function Artist() {
  const t = useT()
  const { id = '' } = useParams()
  const artistId = Number(id)
  const [state, setState] = useState<State>({ status: 'loading' })
  const [failed, setFailed] = useState(false)
  const [shuffling, setShuffling] = useState(false)
  const [shuffleError, setShuffleError] = useState('')
  const abortRef = useRef<AbortController | null>(null)

  const load = useCallback(
    (quiet = false) => {
      abortRef.current?.abort()
      const ac = new AbortController()
      abortRef.current = ac
      if (!quiet) setState({ status: 'loading' })
      getArtist(artistId, ac.signal)
        .then((data) => setState({ status: 'ready', data }))
        .catch((e: unknown) => {
          if (ac.signal.aborted || quiet) return
          setState({
            status: 'error',
            message: e instanceof ApiError ? (e.status === 404 ? t('music.artist.notFound') : e.message) : t('music.artist.loadFailed'),
          })
        })
    },
    [artistId],
  )
  useEffect(() => {
    load()
    return () => abortRef.current?.abort()
  }, [load])
  useEvents(() => load(true), ['library.updated'])

  const title = state.status === 'ready' ? state.data.card.title : ''
  useEffect(() => {
    document.title = `${title || t('common.kind.artist')} — ${APP_NAME}`
  }, [title])

  if (state.status === 'loading')
    return (
      <div className={styles.page} aria-busy="true" aria-label={t('common.loading')}>
        <div className={styles.top}>
          <div className={`${styles.avatar} ${styles.skeleton}`} />
        </div>
      </div>
    )
  if (state.status === 'error')
    return (
      <div className={`${styles.page} ${styles.empty}`} role="alert">
        <Icon name="person" size={44} strokeWidth={1.5} />
        <h1>{t('music.artist.openFailed')}</h1>
        <p>{state.message}</p>
        <Button variant="secondary" onClick={() => load()}>
          {t('music.retry')}
        </Button>
      </div>
    )

  const d = state.data
  const cover = imageUrl(d.card.poster, 960)
  const top = d.topTracks
  const listen = () => audio.play(songItems(top), 0, { shuffle: false })
  const shuffle = () => {
    setShuffling(true)
    setShuffleError('')
    void playShuffled({ kind: 'artist', artistId }).then((err) => {
      setShuffling(false)
      setShuffleError(err ?? '')
    })
  }
  const meta = [d.albums.length ? t('music.albumCount', { count: d.albums.length }) : ''].filter(Boolean).join(' · ')

  return (
    <div className={styles.page}>
      <header className={styles.top}>
        <div className={styles.avatar}>
          {cover && !failed ? (
            <img src={cover} alt="" decoding="async" draggable={false} onError={() => setFailed(true)} />
          ) : (
            <span className={styles.fallback}>
              <Icon name="person" size={56} strokeWidth={1.3} />
            </span>
          )}
        </div>
        <div className={styles.info}>
          <h1 className={styles.name}>{d.card.title}</h1>
          {meta && <p className={styles.meta}>{meta}</p>}
          <div className={styles.actions}>
            <Button icon="play" onClick={listen} disabled={top.length === 0}>
              {t('common.listen')}
            </Button>
            <Button
              variant="secondary"
              icon="shuffle"
              loading={shuffling}
              disabled={top.length === 0 && d.albums.length === 0}
              onClick={shuffle}
            >
              {t('music.shuffle')}
            </Button>
          </div>
          {shuffleError && (
            <p className={styles.shuffleError} role="alert">
              {shuffleError}
            </p>
          )}
        </div>
      </header>

      {top.length > 0 && (
        <section className={styles.section} aria-labelledby="artist-top">
          <h2 id="artist-top" className={styles.heading}>
            {t('music.artist.topSongs')}
          </h2>
          <div className={styles.top10}>
            <TrackList
              tracks={top.slice(0, 10)}
              albumArtist={d.card.title}
              variant="songs"
              subtitle="album"
              label={t('music.artist.topSongs')}
              onPlay={(i) => audio.play(songItems(top), i, { shuffle: false })}
              menu={(tk) => <SongMenu song={tk} hide={{ artist: tk.artistId === d.card.id }} />}
            />
          </div>
        </section>
      )}
      <div className={styles.shelves}>
        {d.albums.length > 0 && <Shelf title={t('common.albums')} cards={d.albums} variant="square" priority />}
        {d.appearsOn.length > 0 && <Shelf title={t('music.artist.appearsOn')} cards={d.appearsOn} variant="square" />}
      </div>
    </div>
  )
}
