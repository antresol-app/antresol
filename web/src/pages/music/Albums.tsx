// «Альбомы»: every album as a grid of squares, a segmented sort control
// (kept in the URL) and more loaded as you scroll — the music twin of Browse.
import { APP_NAME } from '../../brand'
import { useEffect, useMemo } from 'react'
import { useSearchParams } from 'react-router'
import { getAlbums } from '../../api/client'
import type { AlbumSort, Card } from '../../api/types'
import { Button, ButtonLink } from '../../ui/Button'
import { RescanLink } from '../../components/RescanLink'
import { Icon } from '../../components/Icon'
import { PosterGrid, PosterGridSkeleton } from '../../components/PosterGrid'
import { LibraryTools, MineEmpty } from '../../components/LibraryScopeMenu'
import { SegmentedControl, type Segment } from '../../ui/SegmentedControl'
import { useLibraryScope } from '../../state/libraryScope'
import { usePendingCards, withPending } from '../../state/pendingCards'
import { useSession } from '../../state/session'
import { usePagedList } from '../../state/usePagedList'
import styles from './Albums.module.css'
import { useT, type Key } from '../../i18n'

const PAGE = 120
const ALBUM = ['album'] as const

type Sort = Extract<AlbumSort, 'added' | 'title' | 'artist' | 'year'>

const SORTS: { value: Sort; label: Key }[] = [
  { value: 'added', label: 'music.sort.recent' },
  { value: 'title', label: 'common.title' },
  { value: 'artist', label: 'common.kind.artist' },
  { value: 'year', label: 'music.sort.year' },
]

const isSort = (v: string | null): v is Sort => v === 'added' || v === 'title' || v === 'artist' || v === 'year'

export function Albums() {
  const t = useT()
  const title = t('common.albums')
  const { user } = useSession()
  const [params, setParams] = useSearchParams()
  const raw = params.get('sort')
  const sort: Sort = isSort(raw) ? raw : 'added'
  const { scope } = useLibraryScope()
  const { state, load, loadMore, moreError, sentinel } = usePagedList<Card>((offset, limit, signal) => getAlbums({ sort, limit, offset, scope }, signal), [sort, scope], {
    page: PAGE,
    failed: t('common.loadFailed'),
    moreFailed: t('music.loadMoreFailed'),
  })
  const pending = usePendingCards()
  const items = useMemo(() => (state.status === 'ready' ? withPending(state.items, pending, ALBUM) : []), [state, pending])

  useEffect(() => {
    document.title = `${title} — ${APP_NAME}`
  }, [title])

  const setSort = (v: Sort) => {
    const next = new URLSearchParams(params)
    if (v === 'added') next.delete('sort')
    else next.set('sort', v)
    setParams(next, { replace: true })
  }

  return (
    <div className={styles.page}>
      <header className={styles.header}>
        <h1 className={styles.title}>{title}</h1>
        <LibraryTools>
          <SegmentedControl label={t('common.sort')} segments={SORTS.map((s): Segment<Sort> => ({ value: s.value, label: t(s.label) }))} value={sort} onChange={setSort} />
        </LibraryTools>
      </header>

      {state.status === 'loading' && (
        <div aria-busy="true" aria-label={t('common.loading')}>
          <PosterGridSkeleton variant="square" count={18} />
        </div>
      )}

      {state.status === 'error' && (
        <div className={styles.empty} role="alert">
          <Icon name="cloud" size={44} strokeWidth={1.5} />
          <h2>{t('common.loadFailed')}</h2>
          <p>{state.message}</p>
          <Button variant="secondary" onClick={() => load()}>
            {t('music.retry')}
          </Button>
        </div>
      )}

      {state.status === 'ready' && items.length === 0 && scope === 'mine' && <MineEmpty className={styles.empty} />}

      {state.status === 'ready' && items.length === 0 && scope !== 'mine' && (
        <div className={styles.empty}>
          <Icon name="music" size={48} strokeWidth={1.4} />
          <h2>{t('music.emptyTitle')}</h2>
          {user?.isAdmin ? (
            <>
              <p>{t(__LIBRARY_EDITION__ ? 'music.emptyHint' : 'music.emptyHintFull')}</p>
              {__LIBRARY_EDITION__ ? (
                <RescanLink variant="secondary" />
              ) : (
                <ButtonLink to="/search" icon="search" variant="secondary">
                  {t('music.find')}
                </ButtonLink>
              )}
            </>
          ) : (
            <p>{t('music.emptySoon')}</p>
          )}
        </div>
      )}

      {state.status === 'ready' && items.length > 0 && (
        <>
          <PosterGrid cards={items} label={title} variant="square" />
          {moreError ? (
            <div className={styles.moreError} role="alert">
              <span>{moreError}</span>
              <Button variant="secondary" size="small" onClick={loadMore}>
                {t('music.retry')}
              </Button>
            </div>
          ) : (
            <div ref={sentinel} className={styles.sentinel} aria-hidden />
          )}
        </>
      )}
    </div>
  )
}
