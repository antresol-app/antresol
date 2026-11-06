// Shared body of «Фильмы» and «Сериалы»: a large title, a segmented sort
// control (kept in the URL) and a poster grid that loads more as you scroll.
import { APP_NAME } from '../brand'
import { useEffect, useMemo } from 'react'
import { useSearchParams } from 'react-router'
import { getItems } from '../api/client'
import type { Card, ItemSort } from '../api/types'
import { Button, ButtonLink } from '../ui/Button'
import { RescanLink } from '../components/RescanLink'
import { Icon } from '../components/Icon'
import { PosterGrid, PosterGridSkeleton } from '../components/PosterGrid'
import { LibraryTools, MineEmpty } from '../components/LibraryScopeMenu'
import { SegmentedControl, type Segment } from '../ui/SegmentedControl'
import { useLibraryScope } from '../state/libraryScope'
import { usePendingCards, withPending } from '../state/pendingCards'
import { useSession } from '../state/session'
import { usePagedList } from '../state/usePagedList'
import styles from './Browse.module.css'
import { t as tr, useT } from '../i18n'

const PAGE = 120

const sorts = (): Segment<ItemSort>[] => [
  { value: 'recent', label: tr('browse.sort.recent'), short: tr('browse.sort.recentShort') },
  { value: 'title', label: tr('browse.sort.title'), short: tr('browse.sort.titleShort') },
  { value: 'year', label: tr('browse.sort.year'), short: tr('browse.sort.year') },
]

const isSort = (v: string | null): v is ItemSort => v === 'recent' || v === 'title' || v === 'year'

export interface BrowseProps {
  kind: 'movie' | 'series'
  title: string
  emptyTitle: string
}

export function Browse({ kind, title, emptyTitle }: BrowseProps) {
  const t = useT()
  const { user } = useSession()
  const [params, setParams] = useSearchParams()
  const raw = params.get('sort')
  const sort: ItemSort = isSort(raw) ? raw : 'recent'
  const { scope } = useLibraryScope()
  const { state, load, loadMore, moreError, sentinel } = usePagedList<Card>((offset, limit, signal) => getItems(kind, { sort, limit, offset, scope }, signal), [kind, sort, scope], {
    page: PAGE,
    failed: tr('common.loadFailed'),
    moreFailed: tr('browse.loadMoreFailed'),
  })
  const pending = usePendingCards()
  const items = useMemo(() => (state.status === 'ready' ? withPending(state.items, pending, [kind]) : []), [state, pending, kind])

  useEffect(() => {
    document.title = `${title} — ${APP_NAME}`
  }, [title])


  const setSort = (v: ItemSort) => {
    const next = new URLSearchParams(params)
    if (v === 'recent') next.delete('sort')
    else next.set('sort', v)
    setParams(next, { replace: true })
  }

  return (
    <div className={styles.page}>
      <header className={styles.header}>
        <h1 className={styles.title}>{title}</h1>
        <LibraryTools>
          <SegmentedControl label={t('common.sort')} segments={sorts()} value={sort} onChange={setSort} />
        </LibraryTools>
      </header>

      {state.status === 'loading' && (
        <div aria-busy="true" aria-label={t('common.loading')}>
          <PosterGridSkeleton />
        </div>
      )}

      {state.status === 'error' && (
        <div className={styles.empty} role="alert">
          <Icon name="cloud" size={44} strokeWidth={1.5} />
          <h2>{t('common.loadFailed')}</h2>
          <p>{state.message}</p>
          <Button variant="secondary" onClick={() => load()}>
            {t('common.retry')}
          </Button>
        </div>
      )}

      {state.status === 'ready' && items.length === 0 && scope === 'mine' && <MineEmpty className={styles.empty} />}

      {state.status === 'ready' && items.length === 0 && scope !== 'mine' && (
        <div className={styles.empty}>
          <Icon name={kind === 'movie' ? 'film' : 'tv'} size={48} strokeWidth={1.4} />
          <h2>{emptyTitle}</h2>
          {user?.isAdmin ? (
            <>
              <p>{t(__LIBRARY_EDITION__ ? 'browse.emptyAdmin' : 'browse.emptyAdminFull')}</p>
              {__LIBRARY_EDITION__ ? (
                <RescanLink variant="secondary" />
              ) : (
                <ButtonLink to="/search" icon="search" variant="secondary">
                  {t('browse.find')}
                </ButtonLink>
              )}
            </>
          ) : (
            <p>{t('browse.emptyOthers')}</p>
          )}
        </div>
      )}

      {state.status === 'ready' && items.length > 0 && (
        <>
          <PosterGrid cards={items} label={title} />
          {moreError ? (
            <div className={styles.moreError} role="alert">
              <span>{moreError}</span>
              <Button variant="secondary" size="small" onClick={loadMore}>
                {t('common.retry')}
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
