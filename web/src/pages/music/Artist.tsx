// An artist: a round avatar and the name in large type, «Слушать» (the top
// songs), «Перемешать» (the whole catalogue, drawn by the server),
// «Популярные песни» (up to 10), «Альбомы» and «Встречается в».
import { Artwork } from '../../ui/Artwork'
import { APP_NAME } from '../../brand'
import { useEffect, useState } from 'react'
import { useParams } from 'react-router'
import { ApiError, getArtist } from '../../api/client'
import { Button } from '../../ui/Button'
import { Icon } from '../../components/Icon'
import { Shelf } from '../../components/Shelf'
import { audio } from '../../player/audio/engine'
import { songItems } from '../../player/audio/queue'
import { playShuffled } from './shuffle'
import { SongMenu } from './SongMenu'
import { TrackList } from './TrackList'
import { useLoad } from '../../state/useLoad'
import styles from './Artist.module.css'
import { useT } from '../../i18n'


export function Artist() {
  const t = useT()
  const { id = '' } = useParams()
  const artistId = Number(id)
  const [shuffling, setShuffling] = useState(false)
  const [shuffleError, setShuffleError] = useState('')

  const { state, load } = useLoad((signal) => getArtist(artistId, signal), [artistId], {
    message: (e) => e instanceof ApiError ? (e.status === 404 ? t('music.artist.notFound') : e.message) : t('music.artist.loadFailed'),
  })

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
          <Artwork src={d.card.poster} width={320} eager
              fallback={
            <span className={styles.fallback}>
              <Icon name="person" size={56} strokeWidth={1.3} />
            </span>
              }
            />
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
