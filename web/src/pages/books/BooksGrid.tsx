// «Все книги» / «Аудиокниги» / «Манга и комиксы»: every item of one kind as
// a grid of covers, sorted «Недавние / Название / Автор» (kept in the URL),
// more loaded as you scroll.
import { APP_NAME } from '../../brand'
import { useEffect, useMemo } from 'react'
import { Navigate, useParams, useSearchParams } from 'react-router'
import { getBooks } from '../../api/client'
import type { BookKind, BookSort, Card } from '../../api/types'
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
import styles from './BooksGrid.module.css'
import { useT, type Key, type TFunction } from '../../i18n'

const PAGE = 120

const sorts = (t: TFunction): Segment<BookSort>[] => [
  { value: 'added', label: t('books.sort.recent') },
  { value: 'title', label: t('common.title') },
  { value: 'author', label: t('books.sort.author') },
]

const TITLES: Record<BookKind, Key> = {
  book: 'books.allBooks',
  audiobook: 'common.audiobooks',
  comic: 'common.mangaComics',
}

const isKind = (v: string | undefined): v is BookKind => v === 'book' || v === 'audiobook' || v === 'comic'
const isSort = (v: string | null): v is BookSort => v === 'added' || v === 'title' || v === 'author'

export function BooksGrid() {
  const { kind } = useParams()
  if (!isKind(kind)) return <Navigate to="/books" replace />
  return <Grid key={kind} kind={kind} />
}

function Grid({ kind }: { kind: BookKind }) {
  const t = useT()
  const title = t(TITLES[kind])
  const { user } = useSession()
  const [params, setParams] = useSearchParams()
  const raw = params.get('sort')
  const sort: BookSort = isSort(raw) ? raw : 'added'
  const { scope } = useLibraryScope()
  const { state, load, loadMore, moreError, sentinel } = usePagedList<Card>((offset, limit, signal) => getBooks(kind, { sort, limit, offset, scope }, signal), [kind, sort, scope], {
    page: PAGE,
    failed: t('common.loadFailed'),
    moreFailed: t('books.loadMoreFailed'),
  })
  const pending = usePendingCards()
  const items = useMemo(() => (state.status === 'ready' ? withPending(state.items, pending, [kind]) : []), [state, pending, kind])

  useEffect(() => {
    document.title = `${title} — ${APP_NAME}`
  }, [title])


  const setSort = (v: BookSort) => {
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
          <SegmentedControl label={t('common.sort')} segments={sorts(t)} value={sort} onChange={setSort} />
        </LibraryTools>
      </header>

      {state.status === 'loading' && (
        <div aria-busy="true" aria-label={t('common.loading')}>
          <PosterGridSkeleton count={16} />
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
          <Icon name="book" size={48} strokeWidth={1.4} />
          <h2>{t('books.empty.title')}</h2>
          {user?.isKid ? (
            <p>{t('books.empty.kid')}</p>
          ) : (
            <>
              <p>{t(__LIBRARY_EDITION__ ? 'books.empty.hint' : 'books.empty.hintFull')}</p>
              {__LIBRARY_EDITION__ ? (
                <RescanLink variant="secondary" />
              ) : (
                <ButtonLink to={`/search?dk=${kind}`} icon="search" variant="secondary">
                  {t('books.empty.find')}
                </ButtonLink>
              )}
            </>
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
