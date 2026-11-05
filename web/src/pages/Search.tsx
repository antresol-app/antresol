// «Поиск»: one centred column — the title, one field and one filter (Всё,
// Фильмы, Сериалы, Музыка, Книги, Аудиокниги, Манга) centred in it. The
// library answers at once: the top row puts «Лучшее совпадение» (a compact
// card with one action: «Слушать», «Смотреть» or «Открыть») beside the first
// five songs (or «Ещё совпадения»), then a rail per
// kind, kinds with a few results sharing a row. «Найти и добавить» (for a kid
// «Попросить» only) searches the catalog the filter picks (TMDB, MusicBrainz,
// Open Library, the manga name itself) for what is not on the server yet:
// films get «Смотреть ▾» and the download button, albums and books «На
// сервер»; admins can pick the release by hand. The query and the filter
// live in the URL (?q=, ?scope=).
import { APP_NAME } from '../brand'
import { useEffect, useId, useMemo, useRef, useState, type ReactNode } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router'
import { ApiError, getAlbum, getArtist, getHome, imageUrl, search } from '../api/client'
import { SEARCH_SCOPES, type SearchScope } from '../app/sections'
import type { Card, SearchResult, Song } from '../api/types'
import { Button, ButtonLink } from '../ui/Button'
import { Card as Surface } from '../ui/Card'
import { Card as MediaCard, cardHref, isBookKind, isSquareKind } from '../components/Card'
import { Icon, type IconName } from '../components/Icon'
import { SegmentedControl, type Segment } from '../ui/SegmentedControl'
import { Shelf } from '../components/Shelf'
import { audio } from '../player/audio/engine'
import { songItems } from '../player/audio/queue'
import { useSession } from '../state/session'
// «Найти и добавить» (the sources build's; see docs/build-tags.md)
import { FindAndAdd } from './SearchDiscover'
import { TrackList } from './music/TrackList'
import { t as tr, useT, type Key } from '../i18n'
import styles from './Search.module.css'

export type Found<T> = { status: 'idle' } | { status: 'loading' } | { status: 'error'; message: string } | { status: 'ready'; items: T }

const KIND: Record<string, Key> = {
  movie: 'common.kind.movie',
  series: 'common.kind.series',
  artist: 'common.kind.artist',
  album: 'common.kind.album',
  playlist: 'common.kind.playlist',
  music: 'common.kind.album',
  book: 'common.kind.book',
  audiobook: 'common.kind.audiobook',
  comic: 'common.kind.manga',
  comic_volume: 'common.kind.manga',
}
export const kindLabel = (kind: string) => (KIND[kind] ? tr(KIND[kind]) : '')

export const KIND_ICON: Record<string, IconName> = { movie: 'film', series: 'tv', music: 'music', book: 'book', audiobook: 'headphones', comic: 'book' }

/** Card sections of «В медиатеке», in page order (songs sit between albums and books). */
const SECTIONS: { key: Exclude<keyof SearchResult, 'songs'>; title: Key }[] = [
  { key: 'library', title: 'search.section.library' },
  { key: 'artists', title: 'common.artists' },
  { key: 'albums', title: 'common.albums' },
  { key: 'books', title: 'common.books' },
  { key: 'audiobooks', title: 'common.audiobooks' },
  { key: 'comics', title: 'common.mangaComics' },
]

const fold = (s: string) => s.toLowerCase().replaceAll('ё', 'е').trim()

/** «Лучшее совпадение»: the first card (sections in page order, songs aside) whose title is the query. */
export function bestMatch(r: SearchResult, q: string): Card | undefined {
  const want = fold(q)
  if (!want) return undefined
  for (const s of SECTIONS) {
    const hit = (r[s.key] ?? []).find((c) => fold(c.title) === want)
    if (hit) return hit
  }
  return undefined
}

export function useDebounced<T>(v: T, ms: number): T {
  const [d, setD] = useState(v)
  useEffect(() => {
    const id = window.setTimeout(() => setD(v), ms)
    return () => window.clearTimeout(id)
  }, [v, ms])
  return d
}

const normalized = (r: Partial<SearchResult>): SearchResult => ({
  library: r.library ?? [],
  artists: r.artists ?? [],
  albums: r.albums ?? [],
  songs: r.songs ?? [],
  books: r.books ?? [],
  audiobooks: r.audiobooks ?? [],
  comics: r.comics ?? [],
})
const isEmpty = (r: SearchResult) => r.songs.length === 0 && SECTIONS.every((s) => r[s.key].length === 0)

/** The page's one filter: it narrows the library's answer and picks the catalog of «Найти и добавить». */
const scopeSegments = (): Segment<SearchScope>[] => [
  { value: 'all', label: tr('search.scope.all') },
  { value: 'movies', label: tr('common.movies') },
  { value: 'series', label: tr('common.series') },
  { value: 'music', label: tr('common.music') },
  { value: 'books', label: tr('common.books') },
  { value: 'audiobooks', label: tr('common.audiobooks'), short: tr('search.scope.audiobooksShort') },
  { value: 'comics', label: tr('common.kind.manga') },
]
const scopeParam = (v: string | null): SearchScope => SEARCH_SCOPES.find((s) => s === v) ?? 'all'

/** Narrow a library answer to a scope (each kind of book is a scope of its own). */
export function inScope(r: SearchResult, scope: SearchScope): SearchResult {
  const none = normalized({})
  switch (scope) {
    case 'movies':
      return { ...none, library: r.library.filter((c) => c.kind === 'movie') }
    case 'series':
      return { ...none, library: r.library.filter((c) => c.kind === 'series') }
    case 'music':
      return { ...none, artists: r.artists, albums: r.albums, songs: r.songs }
    case 'books':
      return { ...none, books: r.books }
    case 'audiobooks':
      return { ...none, audiobooks: r.audiobooks }
    case 'comics':
      return { ...none, comics: r.comics }
    default:
      return r
  }
}

/** «Лучшее совпадение», else the first card of the first non-empty section. */
export function topResult(r: SearchResult, q: string): Card | undefined {
  return bestMatch(r, q) ?? SECTIONS.map((s) => r[s.key][0]).find(Boolean)
}

const RECENT_KEY = 'vault.recentSearches'
function loadRecent(): string[] {
  try {
    const v: unknown = JSON.parse(localStorage.getItem(RECENT_KEY) ?? '[]')
    return Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string').slice(0, 8) : []
  } catch {
    return []
  }
}
function saveRecent(list: string[]) {
  try {
    localStorage.setItem(RECENT_KEY, JSON.stringify(list))
  } catch {
    /* private mode: recent searches just do not persist */
  }
}

interface Tile {
  key: string
  title: Key
  scope: SearchScope
  grad: string
  icon: IconName
  kinds: string[]
}
const TILES: Tile[] = [
  { key: 'movies', title: 'common.movies', scope: 'movies', grad: 'var(--grad-3)', icon: 'film', kinds: ['movie'] },
  { key: 'series', title: 'common.series', scope: 'series', grad: 'var(--grad-1)', icon: 'tv', kinds: ['series'] },
  { key: 'music', title: 'common.music', scope: 'music', grad: 'var(--grad-6)', icon: 'music', kinds: ['album', 'artist'] },
  { key: 'books', title: 'common.books', scope: 'books', grad: 'var(--grad-5)', icon: 'book', kinds: ['book'] },
  { key: 'audiobooks', title: 'common.audiobooks', scope: 'audiobooks', grad: 'var(--grad-4)', icon: 'headphones', kinds: ['audiobook'] },
  { key: 'comics', title: 'common.mangaComics', scope: 'comics', grad: 'var(--grad-7)', icon: 'book', kinds: ['comic'] },
]

/** Artwork for the browse tiles: the first poster of each kind the home screen shows. */
function useTileArt(): Record<string, string> {
  const [art, setArt] = useState<Record<string, string>>({})
  useEffect(() => {
    const ac = new AbortController()
    getHome(ac.signal).then(
      (h) => {
        const out: Record<string, string> = {}
        for (const c of [...(h.hero ?? []), ...(h.shelves ?? []).flatMap((s) => s.cards ?? [])]) {
          const p = c.poster || c.backdrop
          for (const tile of TILES) if (!out[tile.key] && p && tile.kinds.includes(c.kind)) out[tile.key] = p
        }
        setArt(out)
      },
      () => {},
    )
    return () => ac.abort()
  }, [])
  return art
}

export function Search() {
  const t = useT()
  const { user } = useSession()
  // «Найти и добавить»: the sources build's (not on a library-only server); a
  // kid's, where the server has it (canRequest), only asks («Попросить»)
  const kid = Boolean(user?.isKid)
  const canAcquire = !__LIBRARY_EDITION__ && Boolean(user && !user.libraryOnly && (!kid || user.canRequest))
  const [params, setParams] = useSearchParams()
  const [text, setText] = useState(params.get('q') ?? '')
  const scope = scopeParam(params.get('scope'))
  const [recent, setRecent] = useState(loadRecent)
  const art = useTileArt()
  const q = useDebounced(text.trim(), 250)
  const [library, setLibrary] = useState<Found<SearchResult>>({ status: 'idle' })
  const inputRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    document.title = t('search.documentTitle', { app: APP_NAME })
    inputRef.current?.focus()
  }, [])

  useEffect(() => {
    setParams(
      (p) => {
        const n = new URLSearchParams(p)
        if (q) n.set('q', q)
        else n.delete('q')
        return n
      },
      { replace: true },
    )
  }, [q, setParams])

  const setScope = (v: SearchScope) =>
    setParams(
      (p) => {
        const n = new URLSearchParams(p)
        if (v === 'all') n.delete('scope')
        else n.set('scope', v)
        n.delete('dk')
        return n
      },
      { replace: true },
    )

  const remember = (s: string) => {
    const next = [s, ...recent.filter((r) => r.toLowerCase() !== s.toLowerCase())].slice(0, 8)
    setRecent(next)
    saveRecent(next)
  }
  const forget = () => (setRecent([]), saveRecent([]))

  useEffect(() => {
    if (!q) return setLibrary({ status: 'idle' })
    const ac = new AbortController()
    setLibrary((s) => (s.status === 'ready' ? s : { status: 'loading' }))
    search(q, ac.signal).then(
      (r) => setLibrary({ status: 'ready', items: normalized(r) }),
      (e: unknown) => !ac.signal.aborted && setLibrary({ status: 'error', message: e instanceof ApiError ? e.message : t('search.failed') }),
    )
    return () => ac.abort()
  }, [q])

  const scoped = useMemo(() => (library.status === 'ready' ? inScope(library.items, scope) : null), [library, scope])
  const nothing = Boolean(q) && scoped !== null && isEmpty(scoped)
  const placeholder = {
    all: t('search.placeholder.all'),
    movies: t('common.movies'),
    series: t('common.series'),
    music: t('search.placeholder.music'),
    books: t('search.placeholder.books'),
    audiobooks: t('common.audiobooks'),
    comics: t('common.kind.manga'),
  }[scope]
  const commit = () => text.trim().length >= 2 && remember(text.trim())

  return (
    <div className={styles.page}>
      <header className={styles.header}>
        <h1 className={styles.heading}>{t('common.search')}</h1>
        <form
          className={styles.field}
          role="search"
          onSubmit={(e) => {
            e.preventDefault()
            commit()
            inputRef.current?.blur()
          }}
        >
          <Icon name="search" size={20} className={styles.fieldIcon} />
          <input
            ref={inputRef}
            type="search"
            className={styles.input}
            aria-label={placeholder}
            placeholder={placeholder}
            value={text}
            onChange={(e) => setText(e.target.value)}
            onBlur={commit}
            autoComplete="off"
            spellCheck={false}
            enterKeyHint="search"
          />
          {text && (
            <button type="button" className={styles.clear} aria-label={t('search.clear')} onClick={() => (setText(''), inputRef.current?.focus())}>
              <Icon name="close" size={12} strokeWidth={2.8} />
            </button>
          )}
        </form>
        <div className={styles.scope}>
          <SegmentedControl label={t('search.scopeLabel')} segments={scopeSegments()} value={scope} onChange={setScope} row />
        </div>
      </header>

      {!q && (
        <div className={styles.browse}>
          {recent.length > 0 && (
            <section aria-labelledby="recent-title" className={styles.recent}>
              <div className={styles.recentHead}>
                <h2 id="recent-title" className={styles.sectionTitle}>
                  {t('search.recent')}
                </h2>
                <Button variant="plain" size="small" onClick={forget}>
                  {t('search.clear')}
                </Button>
              </div>
              <ul className={styles.recentList}>
                {recent.map((r) => (
                  <li key={r}>
                    <button type="button" className={styles.recentItem} onClick={() => (setText(r), inputRef.current?.focus())}>
                      <Icon name="search" size={16} />
                      <span>{r}</span>
                    </button>
                  </li>
                ))}
              </ul>
            </section>
          )}
          <section aria-labelledby="browse-title">
            <h2 id="browse-title" className={styles.sectionTitle}>
              {t('search.browse')}
            </h2>
            <ul className={styles.tiles}>
              {TILES.map((tile, i) => {
                const src = imageUrl(art[tile.key], 480)
                return (
                  <li key={tile.key} style={{ ['--i' as string]: i }}>
                    <button
                      type="button"
                      className={`${styles.tile} ${scope === tile.scope ? styles.tileOn : ''}`}
                      style={{ background: tile.grad }}
                      aria-pressed={scope === tile.scope}
                      onClick={() => (setScope(tile.scope), inputRef.current?.focus())}
                    >
                      {src && <img className={styles.tileArt} src={src} alt="" loading="lazy" decoding="async" draggable={false} />}
                      <span className={styles.tileTitle}>{t(tile.title)}</span>
                      <Icon name={tile.icon} size={22} strokeWidth={1.8} className={styles.tileIcon} />
                    </button>
                  </li>
                )
              })}
            </ul>
          </section>
          <p className={styles.hint}>{canAcquire ? (kid ? t('search.hintAsk') : t('search.hintAcquire')) : t('search.hint')}</p>
        </div>
      )}

      {q && library.status === 'loading' && <TopSkeleton />}
      {scoped && q && !nothing && <LibraryResults result={scoped} q={q} scope={scope} />}
      {library.status === 'error' && <p className={styles.problem}>{library.message}</p>}

      {/* nothing in the library: the same empty state, shorter when «Найти и добавить» follows */}
      {nothing && (
        <div className={canAcquire ? `${styles.empty} ${styles.emptyCompact}` : styles.empty} role="status">
          <Icon name="search" size={canAcquire ? 32 : 44} strokeWidth={1.4} />
          <h2>{t('search.nothing')}</h2>
          <p>{t('search.nothingHint')}</p>
        </div>
      )}

      {!__LIBRARY_EDITION__ && canAcquire && q.length >= 2 && (
        <FindAndAdd text={text} q={q} scope={scope} nothing={nothing} admin={Boolean(user?.isAdmin)} kid={kid} />
      )}
    </div>
  )
}

function LibraryResults({ result, q, scope }: { result: SearchResult; q: string; scope: SearchScope }) {
  const t = useT()
  const best = topResult(result, q)
  // «Фильмы» / «Сериалы» name the films-and-series section after themselves
  const titleOf = (s: (typeof SECTIONS)[number]) =>
    s.key === 'library' && scope === 'movies' ? t('common.movies') : s.key === 'library' && scope === 'series' ? t('common.series') : t(s.title)
  const shelf = (s: (typeof SECTIONS)[number], cards: Card[]) => {
    const square = cards[0] && isSquareKind(cards[0].kind)
    return <Shelf key={s.key} title={titleOf(s)} cards={cards} variant={square ? 'square' : 'poster'} contained />
  }
  const [allSongs, setAllSongs] = useState(false)
  const songs = result.songs.length > 0 && (
    <section className={styles.songs} aria-labelledby="search-songs">
      <div className={styles.sectionHead}>
        <h2 id="search-songs" className={styles.sectionTitle}>
          {t('common.songs')}
        </h2>
        {result.songs.length > SONGS_BESIDE && (
          <Button variant="plain" size="small" aria-expanded={allSongs} onClick={() => setAllSongs((v) => !v)}>
            {allSongs ? t('common.collapse') : t('search.showAll')}
          </Button>
        )}
      </div>
      <TrackList
        tracks={allSongs ? result.songs : result.songs.slice(0, SONGS_BESIDE)}
        albumArtist=""
        variant="songs"
        subtitle="both"
        label={t('common.songs')}
        onPlay={(i) => audio.play(songItems(result.songs), i, { shuffle: false })}
      />
    </section>
  )
  // no songs: the next matches beside the best one, so the top row is never half empty
  const others = SECTIONS.flatMap((s) => result[s.key]).filter((c) => c !== best)
  const side = songs || (best && others.length > 0 && <MoreResults cards={others.slice(0, MORE_RESULTS)} />)
  // a kind with a few items is a small section; neighbouring ones share a row
  // instead of each taking a full-width rail
  const rows: ReactNode[] = []
  let few: ReactNode[] = []
  const flush = () => {
    if (few.length) rows.push(<div key={`few-${rows.length}`} className={styles.few}>{few}</div>)
    few = []
  }
  for (const s of SECTIONS) {
    // the best match is the card above: not shown again in its kind
    const cards = result[s.key].filter((c) => c !== best)
    if (cards.length === 0) continue
    if (cards.length <= FEW) few.push(<FewSection key={s.key} title={titleOf(s)} cards={cards} />)
    else {
      flush()
      rows.push(shelf(s, cards))
    }
  }
  flush()
  return (
    <div className={`${styles.results} ${styles.enter}`}>
      {/* the top row: the best match beside the songs or the next matches (Music app) */}
      {(best || side) && (
        <div className={[styles.top, best ? '' : styles.listOnly, allSongs && songs ? styles.expanded : ''].filter(Boolean).join(' ')}>
          {/* a new result is a new card: no busy state or failed artwork carried over */}
          {best && <TopResult key={`${best.kind}-${best.id}`} card={best} />}
          {side}
        </div>
      )}
      {/* then the kinds: a rail each, sized by its shape (posters, squares, circles) */}
      <div className={styles.rails}>{rows}</div>
    </div>
  )
}

/** A kind with at most this many results is a small section (sharing a row), not a rail. */
const FEW = 3
/** «Ещё совпадения» beside the best match: this many rows (the card is as tall as five). */
const MORE_RESULTS = 5
/** Songs beside the best match until «Показать все»: the card is as tall as these. */
const SONGS_BESIDE = 5

function FewSection({ title, cards }: { title: string; cards: Card[] }) {
  const id = useId()
  const square = cards[0] && isSquareKind(cards[0].kind)
  return (
    <section className={`${styles.fewSection} ${square ? styles.fewSquare : styles.fewPoster}`} aria-labelledby={id}>
      <h2 id={id} className={styles.sectionTitle}>
        {title}
      </h2>
      <ul className={styles.fewCards}>
        {cards.map((c) => (
          <li key={`${c.kind}-${c.id}`}>
            <MediaCard card={c} variant={square ? 'square' : 'poster'} />
          </li>
        ))}
      </ul>
    </section>
  )
}

/** The next matches as compact rows (a thumbnail, the title, the kind): the top row's other half when there are no songs. */
function MoreResults({ cards }: { cards: Card[] }) {
  const t = useT()
  return (
    <section className={styles.songs} aria-labelledby="search-more">
      <h2 id="search-more" className={styles.sectionTitle}>
        {t('search.moreResults')}
      </h2>
      <ul className={styles.moreList}>
        {cards.map((c) => {
          const src = imageUrl(c.poster || c.backdrop, 160)
          const sub = [kindLabel(c.kind), c.subtitle || (c.year ? String(c.year) : '')].filter(Boolean).join(' · ')
          return (
            <li key={`${c.kind}-${c.id}`}>
              <Link to={cardHref(c)} className={styles.moreRow}>
                <span className={`${styles.moreThumb} ${isSquareKind(c.kind) ? styles.square : ''}`}>
                  {src ? <img src={src} alt="" loading="lazy" /> : <Icon name={KIND_ICON[c.kind] ?? 'film'} size={16} />}
                </span>
                <span className={styles.moreText}>
                  <span className={styles.moreTitle}>{c.title}</span>
                  {sub && <span className={styles.moreSub}>{sub}</span>}
                </span>
              </Link>
            </li>
          )
        })}
      </ul>
    </section>
  )
}

/** What the top result's one action does: play music, watch a film or series, else open the item. */
type TopAction = { kind: 'listen' } | { kind: 'watch'; to: string } | { kind: 'open'; to: string }
function topAction(card: Card): TopAction {
  if (card.kind === 'album' || card.kind === 'artist') return { kind: 'listen' }
  // only what the server has: a title still downloading (or not there) opens its page
  if ((card.kind === 'movie' || card.kind === 'series') && card.id > 0 && card.onServer && !card.pending) return { kind: 'watch', to: `/watch/${card.id}` }
  return { kind: 'open', to: cardHref(card) }
}

/** The songs «Слушать» plays: an album's, or an artist's top songs. */
async function songsOf(card: Card, signal: AbortSignal): Promise<Song[]> {
  if (card.kind === 'album') return (await getAlbum(card.id, signal)).tracks
  if (card.kind === 'artist') return (await getArtist(card.id, signal)).topTracks
  return []
}

/** The artwork's shape: an artist is round, music square, the rest keeps its poster (books their spine). */
const artShape = (kind: string) => (kind === 'artist' ? 'round' : isSquareKind(kind) ? 'square' : isBookKind(kind) ? 'book' : 'poster')

/**
 * «Лучшее совпадение»: a compact card — the artwork, the name and its kind,
 * one primary action — as tall as the songs beside it (Music app). The name
 * is the card's link, so the whole card opens the item.
 */
function TopResult({ card }: { card: Card }) {
  const t = useT()
  const navigate = useNavigate()
  const src = imageUrl(card.poster || card.backdrop, 480)
  const [failed, setFailed] = useState(false)
  const [busy, setBusy] = useState(false)
  const href = cardHref(card)
  const shape = artShape(card.kind)
  const sub = [kindLabel(card.kind), card.subtitle || (card.year ? String(card.year) : '')].filter(Boolean).join(' · ')
  const action = topAction(card)
  // nothing to play (or the server failed): the item's page has the rest
  // a card that is gone (another query) neither plays nor navigates
  const pending = useRef<AbortController | null>(null)
  useEffect(() => () => pending.current?.abort(), [])
  const listen = () => {
    pending.current?.abort()
    const ac = new AbortController()
    pending.current = ac
    setBusy(true)
    songsOf(card, ac.signal)
      .then(
        (songs) => {
          if (ac.signal.aborted) return
          if (songs.length > 0) audio.play(songItems(songs), 0, { shuffle: false })
          else navigate(href)
        },
        () => !ac.signal.aborted && navigate(href),
      )
      .finally(() => !ac.signal.aborted && setBusy(false))
  }
  return (
    <section className={styles.topSection} aria-labelledby="best-match">
      <h2 id="best-match" className={styles.sectionTitle}>
        {t('search.bestMatch')}
      </h2>
      <Surface padding="lg" className={styles.topCard}>
        <span className={styles.topArt} data-shape={shape}>
          {src && !failed ? (
            <img src={src} alt="" onError={() => setFailed(true)} />
          ) : (
            <Icon name={KIND_ICON[card.kind] ?? (shape === 'square' || shape === 'round' ? 'music' : 'film')} size={36} />
          )}
        </span>
        <div className={styles.topFoot}>
          <div className={styles.topText}>
            <Link to={href} className={styles.topName}>
              {card.title}
            </Link>
            {sub && <p className={styles.topKind}>{sub}</p>}
          </div>
          <div className={styles.topAction}>
            {action.kind === 'listen' ? (
              <Button size="small" icon="play" loading={busy} onClick={listen}>
                {t('common.listen')}
              </Button>
            ) : (
              <ButtonLink size="small" icon={action.kind === 'watch' ? 'play' : undefined} to={action.to}>
                {action.kind === 'watch' ? t('common.watch') : t('search.open')}
              </ButtonLink>
            )}
          </div>
        </div>
      </Surface>
    </section>
  )
}

/** The top row's shape while the library answers (first search only; later ones keep the last answer). */
function TopSkeleton() {
  const t = useT()
  return (
    <div className={`${styles.results} ${styles.late}`} role="status" aria-busy="true">
      <span className="visually-hidden">{t('search.loading')}</span>
      <div className={`${styles.top} ${styles.topSkeleton}`}>
        <div className={styles.topSection}>
          <div className={`${styles.skeletonLine} ${styles.skeletonTitle}`} />
          <Surface padding="lg" className={styles.topCard}>
            <span className={`${styles.topArt} ${styles.skeleton}`} data-shape="square" />
            <div className={styles.topFoot}>
              <div className={`${styles.topText} ${styles.skeletonText}`}>
                <div className={styles.skeletonLine} style={{ width: '60%', height: 18 }} />
                <div className={styles.skeletonLine} style={{ width: '35%' }} />
              </div>
            </div>
          </Surface>
        </div>
        <div className={styles.songs}>
          <div className={`${styles.skeletonLine} ${styles.skeletonTitle}`} />
          <ul className={styles.skeletonRows}>
            {[0, 1, 2, 3, 4].map((i) => (
              <li key={i}>
                <span className={`${styles.skeletonThumb} ${styles.skeleton}`} />
                <span className={styles.skeletonText}>
                  <div className={styles.skeletonLine} style={{ width: `${55 - i * 6}%` }} />
                  <div className={styles.skeletonLine} style={{ width: '30%' }} />
                </span>
              </li>
            ))}
          </ul>
        </div>
      </div>
    </div>
  )
}
