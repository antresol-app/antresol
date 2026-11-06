// «Песни»: every song in one list, «Перемешать все» (up to 500 random songs
// of the library, drawn by the server), more loaded as you scroll.
import { APP_NAME } from '../../brand'
import { useEffect, useState } from 'react'
import { getSongs } from '../../api/client'
import type { Song } from '../../api/types'
import { Button } from '../../ui/Button'
import { Icon } from '../../components/Icon'
import { audio } from '../../player/audio/engine'
import { songItems } from '../../player/audio/queue'
import { playShuffled } from './shuffle'
import { TrackList } from './TrackList'
import { usePagedList } from '../../state/usePagedList'
import styles from './Songs.module.css'
import { useT } from '../../i18n'

const PAGE = 200

export function Songs() {
  const t = useT()
  const { state, load, loadMore, moreError, sentinel } = usePagedList<Song>((offset, limit, signal) => getSongs({ limit, offset }, signal), [], {
    page: PAGE,
    failed: t('music.songs.loadFailed'),
    moreFailed: t('music.loadMoreFailed'),
    step: 'page',
  })
  const [shuffling, setShuffling] = useState(false)
  const [shuffleError, setShuffleError] = useState('')

  useEffect(() => {
    document.title = `${t('common.songs')} — ${APP_NAME}`
  }, [])

  const shuffleAll = () => {
    setShuffling(true)
    setShuffleError('')
    void playShuffled({ kind: 'all' }).then((err) => {
      setShuffling(false)
      setShuffleError(err ?? '')
    })
  }

  const items = state.status === 'ready' ? state.items : []

  return (
    <div className={styles.page}>
      <header className={styles.header}>
        <h1 className={styles.title}>{t('common.songs')}</h1>
        {items.length > 0 && (
          <Button variant="secondary" size="small" icon="shuffle" loading={shuffling} onClick={shuffleAll}>
            {t('music.shuffleAll')}
          </Button>
        )}
      </header>
      {shuffleError && (
        <p className={styles.shuffleError} role="alert">
          {shuffleError}
        </p>
      )}
      {state.status === 'loading' && <div className={styles.skeleton} aria-busy="true" aria-label={t('common.loading')} />}
      {state.status === 'error' && (
        <div className={styles.empty} role="alert">
          <Icon name="cloud" size={44} strokeWidth={1.5} />
          <h2>{t('music.songs.loadFailed')}</h2>
          <p>{state.message}</p>
          <Button variant="secondary" onClick={() => load()}>
            {t('music.retry')}
          </Button>
        </div>
      )}
      {state.status === 'ready' && items.length === 0 && (
        <div className={styles.empty}>
          <Icon name="music" size={48} strokeWidth={1.4} />
          <h2>{t('music.songs.empty')}</h2>
          <p>{t('music.songs.emptyHint')}</p>
        </div>
      )}
      {items.length > 0 && (
        <div className={styles.list}>
          <TrackList tracks={items} albumArtist="" variant="songs" label={t('common.songs')} onPlay={(i) => audio.play(songItems(items), i, { shuffle: false })} />
        </div>
      )}
      {state.status === 'ready' && state.more &&
        (moreError ? (
          <div className={styles.moreError} role="alert">
            <span>{moreError}</span>
            <Button variant="secondary" size="small" onClick={loadMore}>
              {t('music.retry')}
            </Button>
          </div>
        ) : (
          <div ref={sentinel} className={styles.sentinel} aria-hidden />
        ))}
    </div>
  )
}
