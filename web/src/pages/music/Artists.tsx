// «Артисты»: round avatars (the newest album's cover) in a grid, more
// loaded as you scroll.
import { APP_NAME } from '../../brand'
import { useEffect } from 'react'
import { getArtists } from '../../api/client'
import type { Card } from '../../api/types'
import { Button } from '../../ui/Button'
import { Icon } from '../../components/Icon'
import { LibraryTools, MineEmpty } from '../../components/LibraryScopeMenu'
import { PosterGrid, PosterGridSkeleton } from '../../components/PosterGrid'
import { useLibraryScope } from '../../state/libraryScope'
import { usePagedList } from '../../state/usePagedList'
import styles from './Artists.module.css'
import { useT } from '../../i18n'

const PAGE = 120

export function Artists() {
  const t = useT()
  const { scope } = useLibraryScope()
  const { state, load, loadMore, moreError, sentinel } = usePagedList<Card>((offset, limit, signal) => getArtists({ limit, offset, scope }, signal), [scope], {
    page: PAGE,
    failed: t('music.artists.loadFailed'),
    moreFailed: t('music.loadMoreFailed'),
    step: 'page',
  })

  useEffect(() => {
    document.title = `${t('common.artists')} — ${APP_NAME}`
  }, [])

  return (
    <div className={styles.page}>
      <header className={styles.header}>
        <h1 className={styles.title}>{t('common.artists')}</h1>
        <LibraryTools />
      </header>
      {state.status === 'loading' && <PosterGridSkeleton variant="square" count={14} />}
      {state.status === 'error' && (
        <div className={styles.empty} role="alert">
          <Icon name="cloud" size={44} strokeWidth={1.5} />
          <h2>{t('music.artists.loadFailed')}</h2>
          <p>{state.message}</p>
          <Button variant="secondary" onClick={() => load()}>
            {t('music.retry')}
          </Button>
        </div>
      )}
      {state.status === 'ready' && state.items.length === 0 && scope === 'mine' && <MineEmpty className={styles.empty} />}
      {state.status === 'ready' && state.items.length === 0 && scope !== 'mine' && (
        <div className={styles.empty}>
          <Icon name="person" size={48} strokeWidth={1.4} />
          <h2>{t('music.artists.empty')}</h2>
          <p>{t('music.artists.emptyHint')}</p>
        </div>
      )}
      {state.status === 'ready' && state.items.length > 0 && <PosterGrid cards={state.items} label={t('common.artists')} variant="square" />}
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
