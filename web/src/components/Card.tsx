// A library tile. `poster` (2:3) for browsing, `wide` (16:9) for «Продолжить»
// with a progress bar, `square` (1:1) for albums, playlists and artists
// (round). Lifts slightly on hover and keyboard focus. A finished title
// (`watched`) has its picture dimmed and the eye in the middle, and no
// progress bar (web/DESIGN.md, «A watched title»).
import { memo, useState } from 'react'
import { Link } from 'react-router'
import { imageSrcSet, imageUrl } from '../api/client'
import type { Card as CardData, FollowWhy } from '../api/types'
import { useT, type Key, type TFunction } from '../i18n'
import { Badges } from '../ui/Badge'
import { Icon } from './Icon'
import { PendingOverlay, pendingLabel } from './PendingOverlay'
import { WatchedMark, dimClass, doneLabel } from './WatchedMark'
import styles from './Card.module.css'

export type CardVariant = 'poster' | 'wide' | 'square'

export interface CardProps {
  card: CardData
  variant?: CardVariant
  /** Eager-load images for the first visible cards. */
  priority?: boolean
}

export function cardHref(c: CardData) {
  // «Продолжить» from an addon: the page it names (the core checked it is the app's)
  if (c.addonId && c.href) return c.href
  if (isTitleCard(c)) return titleCardHref(c)
  return itemHref(c.kind, c.id)
}

/** A TMDB title's page; a new episode («Вышло новое») is chosen there for «Смотреть». */
function titleCardHref(c: CardData): string {
  const base = `/title/${c.tmdbKind}/${c.tmdbId}`
  return c.tmdbKind === 'series' && c.season && c.episode ? `${base}?season=${c.season}&episode=${c.episode}` : base
}

/**
 * A TMDB title that is not in the library («Похожие», a missing part of a
 * collection, «Вышло новое»): id 0, its core title page by tmdbKind and tmdbId.
 */
export function isTitleCard(c: CardData): boolean {
  return !(c.id > 0) && !c.pending && !c.onServer && Boolean(c.tmdbId) && (c.tmdbKind === 'movie' || c.tmdbKind === 'series')
}

/** Where a card of a title on its way leads: the page its addon names (D3); null: nowhere (not a link). */
export function pendingHref(c: CardData): string | null {
  return c.pending?.href ?? null
}

/** A stable key of a card in a row (the cards of titles outside the library all have id 0). */
export const cardKey = (c: CardData) =>
  c.addonId && c.key
    ? c.key
    : c.pending && !(c.id > 0)
      ? `pending-${c.pending.key}`
      : isTitleCard(c)
        ? `title-${c.tmdbKind}-${c.tmdbId}`
        : `${c.kind}-${c.id}`

/** The page of a library item of any kind (a film's detail, an album, a book…). */
export function itemHref(kind: string, id: number) {
  const c = { kind, id }
  switch (c.kind) {
    case 'album':
      return `/music/album/${c.id}`
    case 'artist':
      return `/music/artist/${c.id}`
    case 'playlist':
      return `/music/playlist/${c.id}`
    case 'book':
      return `/books/${c.id}`
    case 'audiobook':
      return `/books/audiobook/${c.id}`
    case 'comic':
      return `/books/comic/${c.id}`
    case 'comic_volume':
      return `/read/${c.id}`
    default:
      return `/item/${c.id}`
  }
}

/** Books, comics and their volumes look like printed books (spine, shadow). */
export const isBookKind = (kind: string) => kind === 'book' || kind === 'comic' || kind === 'comic_volume'

/** Music kinds are shown as square tiles. */
export const isSquareKind = (kind: string) => kind === 'album' || kind === 'artist' || kind === 'playlist'

/** Deterministic, calm two-tone fallback when there is no artwork. */
function fallbackStyle(seed: string) {
  let h = 0
  for (let i = 0; i < seed.length; i++) h = (h * 31 + seed.charCodeAt(i)) >>> 0
  const hue = h % 360
  return { background: `linear-gradient(160deg, hsl(${hue} 28% 26%), hsl(${(hue + 40) % 360} 32% 12%))` }
}

export function secondaryLine(c: CardData): string {
  if (c.subtitle) return c.subtitle
  return c.year ? String(c.year) : ''
}

// the lists a mark may come from, by their own names
const LIST_NAMES: Record<string, string> = { shikimori: 'Shikimori', myshows: 'MyShows' }

/** Why a «Вышло новое» card is there: the mark and the list that set it, or the watchlist. */
export function whyLine(t: TFunction, why: FollowWhy | undefined): string {
  if (!why) return ''
  if (why.state === 'watchlist') return t('comp.card.why.watchlist')
  const state = t(`tracking.mark.${why.state}` as Key)
  if (!why.origin || why.origin === 'antresol') return t('comp.card.why.here', { state })
  return t('comp.card.why.list', { list: LIST_NAMES[why.origin] ?? why.origin, state })
}

// how wide a card shows (Shelf.module.css: 2.25 or 1.25 a phone's width,
// down to an eighth of a wide window): the srcset step the browser takes
const CARD_SIZES: Record<CardVariant, string> = {
  poster: '(max-width: 699px) 45vw, (max-width: 1099px) 22vw, 15vw',
  square: '(max-width: 699px) 45vw, (max-width: 1099px) 22vw, 15vw',
  wide: '(max-width: 699px) 80vw, (max-width: 1099px) 34vw, 26vw',
}

// memo: a grid that gets its next page (or a pending title) keeps the objects
// of the cards it already had, so only the new ones render
export const Card = memo(function Card({ card, variant = 'poster', priority = false }: CardProps) {
  const t = useT()
  const art = variant === 'wide' ? card.backdrop || card.poster : card.poster || card.backdrop
  const src = imageUrl(art, 480)
  const [failed, setFailed] = useState(false)
  const showImage = src && !failed
  const pct = Math.round(Math.min(Math.max(card.progress || 0, 0), 1) * 100)
  // a title on its way into the library: its own tinted card, or the progress on the library's card
  const pending = card.pending
  const own = !!pending && !(card.id > 0)
  // finished: dimmed picture and the mark; a download (a new season on its way) takes the place of both
  const done = !!card.watched && !pending
  const dim = done ? dimClass : undefined
  const doneWord = doneLabel(t, card.kind)
  const sub = own ? pendingLabel(t, pending) : secondaryLine(card)
  const href = own ? pendingHref(card) : cardHref(card)
  const why = own ? '' : whyLine(t, card.why)

  const className = [
    styles.card,
    styles[variant],
    variant === 'square' && card.kind === 'artist' ? styles.round : '',
    variant === 'poster' && isBookKind(card.kind) ? styles.book : '',
    own ? styles.pending : '',
  ]
    .filter(Boolean)
    .join(' ')

  const body = (
    <>
      <div className={styles.media} style={showImage ? undefined : fallbackStyle(card.title)}>
        {showImage ? (
          <img
            src={src}
            srcSet={imageSrcSet(art, 160, 1280)}
            sizes={CARD_SIZES[variant]}
            alt=""
            className={dim}
            loading={priority ? 'eager' : 'lazy'}
            decoding="async"
            draggable={false}
            onError={() => setFailed(true)}
          />
        ) : (
          <span className={[styles.fallbackTitle, dim].filter(Boolean).join(' ')} aria-hidden>
            {card.title}
          </span>
        )}
        {pending ? (
          <PendingOverlay pending={pending} corner={!own} />
        ) : (
          // an addon's card is no library title: no «Ещё не на сервере»
          !card.onServer &&
          !card.addonId && (
            <span className={styles.corner} data-testid="card-corner">
              <span className={styles.badge} title={t('comp.card.notOnServer')}>
                <Icon name="cloud" size={14} strokeWidth={2} />
              </span>
            </span>
          )
        )}
        {/* in the middle of the dimmed picture; part of the link's name («Матрица Просмотрено 1999») */}
        {done && <WatchedMark label={doneWord} />}
        {pct > 0 && pct < 100 && !own && !done && (
          <div
            className={styles.progress}
            role="progressbar"
            aria-label={doneWord}
            aria-valuemin={0}
            aria-valuemax={100}
            aria-valuenow={pct}
          >
            <div className={styles.progressFill} data-testid="progress-fill" style={{ width: `${pct}%` }} />
          </div>
        )}
      </div>
      <div className={styles.meta}>
        <span className={styles.title}>{card.title}</span>
        {sub && <span className={styles.subtitle}>{sub}</span>}
        {why && (
          <span className={styles.why} data-testid="card-why">
            {why}
          </span>
        )}
        {!own && <Badges items={card.badges?.slice(0, 2)} className={styles.badges} />}
      </div>
    </>
  )

  if (href === null) {
    return (
      <div className={className} role="group" aria-label={`${card.title}, ${sub}`}>
        {body}
      </div>
    )
  }
  return (
    <Link to={href} className={className}>
      {body}
    </Link>
  )
})
