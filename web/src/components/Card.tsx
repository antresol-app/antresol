// A library tile. `poster` (2:3) for browsing, `wide` (16:9) for «Продолжить»
// with a progress bar, `square` (1:1) for albums, playlists and artists
// (round). Lifts slightly on hover and keyboard focus.
import { useState } from 'react'
import { Link } from 'react-router'
import { imageSrcSet, imageUrl } from '../api/client'
import type { Card as CardData } from '../api/types'
import { useT } from '../i18n'
import { Badges } from '../ui/Badge'
import { Icon } from './Icon'
import { PendingOverlay, pendingLabel } from './PendingOverlay'
import { useSession } from '../state/session'
import styles from './Card.module.css'

export type CardVariant = 'poster' | 'wide' | 'square'

export interface CardProps {
  card: CardData
  variant?: CardVariant
  /** Eager-load images for the first visible cards. */
  priority?: boolean
}

export function cardHref(c: CardData) {
  // «Продолжить просмотр» of an online title: id is 0, the TMDB id says what it is
  if (!__LIBRARY_EDITION__ && c.online && c.tmdbId && (c.tmdbKind === 'movie' || c.tmdbKind === 'series')) {
    return onlineCardHref(c.tmdbKind, c.tmdbId, c.season ?? 0, c.episode ?? 0, c.resumeSec ?? 0)
  }
  if (!__LIBRARY_EDITION__ && isTitleCard(c)) return `/title/${c.tmdbKind}/${c.tmdbId}`
  return itemHref(c.kind, c.id)
}

/**
 * A TMDB title that is not in the library («Похожие», a missing part of a
 * collection; sources build): id 0, its title page by tmdbKind and tmdbId.
 */
export function isTitleCard(c: CardData): boolean {
  return !(c.id > 0) && !c.online && !c.pending && !c.onServer && Boolean(c.tmdbId) && (c.tmdbKind === 'movie' || c.tmdbKind === 'series')
}

/**
 * Where a card of a title still downloading leads: a movie or series to its
 * title page (the download button's ring is there too), anything else to
 * «Загрузки» for an admin; null: nowhere (not a link).
 */
export function pendingHref(c: CardData, isAdmin: boolean): string | null {
  if (c.tmdbId && (c.tmdbKind === 'movie' || c.tmdbKind === 'series')) return `/title/${c.tmdbKind}/${c.tmdbId}`
  return isAdmin ? '/manage/downloads' : null
}

/** A stable key of a card in a row (online and pending cards all have id 0). */
export const cardKey = (c: CardData) =>
  !__LIBRARY_EDITION__ && c.online
    ? `online-${c.tmdbKind}-${c.tmdbId}`
    : !__LIBRARY_EDITION__ && c.pending && !(c.id > 0)
      ? `pending-${c.pending.key}`
      : !__LIBRARY_EDITION__ && isTitleCard(c)
        ? `title-${c.tmdbKind}-${c.tmdbId}`
        : `${c.kind}-${c.id}`

/** The online player's resume URL; kept here so the shelf does not load the player. */
function onlineCardHref(kind: 'movie' | 'series', tmdbId: number, season: number, episode: number, resumeSec: number): string {
  const q = new URLSearchParams()
  if (kind === 'series') {
    q.set('s', String(season))
    q.set('e', String(episode))
  }
  if (resumeSec > 0) q.set('t', String(Math.floor(resumeSec)))
  return `/online/${kind}/${tmdbId}${q.size ? `?${q}` : ''}`
}

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

// how wide a card shows (Shelf.module.css: 2.25 or 1.25 a phone's width,
// down to an eighth of a wide window): the srcset step the browser takes
const CARD_SIZES: Record<CardVariant, string> = {
  poster: '(max-width: 699px) 45vw, (max-width: 1099px) 22vw, 15vw',
  square: '(max-width: 699px) 45vw, (max-width: 1099px) 22vw, 15vw',
  wide: '(max-width: 699px) 80vw, (max-width: 1099px) 34vw, 26vw',
}

export function Card({ card, variant = 'poster', priority = false }: CardProps) {
  const t = useT()
  const { user } = useSession()
  const art = variant === 'wide' ? card.backdrop || card.poster : card.poster || card.backdrop
  const src = imageUrl(art, 480)
  const [failed, setFailed] = useState(false)
  const showImage = src && !failed
  const pct = Math.round(Math.min(Math.max(card.progress || 0, 0), 1) * 100)
  // a title on its way into the library: its own tinted card, or the progress on the library's card
  const pending = !__LIBRARY_EDITION__ ? card.pending : undefined
  const own = !!pending && !(card.id > 0)
  const sub = own ? pendingLabel(t, pending) : secondaryLine(card)
  const href = own ? pendingHref(card, !!user?.isAdmin) : cardHref(card)

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
            loading={priority ? 'eager' : 'lazy'}
            decoding="async"
            draggable={false}
            onError={() => setFailed(true)}
          />
        ) : (
          <span className={styles.fallbackTitle} aria-hidden>
            {card.title}
          </span>
        )}
        {pending ? (
          <PendingOverlay pending={pending} corner={!own} />
        ) : !__LIBRARY_EDITION__ && card.online ? (
          <span className={`${styles.chip} glass`}>{t('comp.card.online')}</span>
        ) : (
          (!card.onServer || card.watched) && (
            // the corner's badges side by side
            <span className={styles.corner} data-testid="card-corner">
              {!card.onServer && (
                <span className={`${styles.badge} glass`} title={t('comp.card.notOnServer')}>
                  <Icon name="cloud" size={14} strokeWidth={2} />
                </span>
              )}
              {card.watched && (
                // part of the link's name («Матрица Просмотрено 1999»)
                <span className={`${styles.badge} glass`} role="img" aria-label={t('comp.card.watched')} title={t('comp.card.watched')}>
                  <Icon name="check" size={14} strokeWidth={2.6} />
                </span>
              )}
            </span>
          )
        )}
        {pct > 0 && !own && (
          <div
            className={styles.progress}
            role="progressbar"
            aria-label={isBookKind(card.kind) ? t('comp.card.read') : card.kind === 'audiobook' ? t('comp.card.listened') : t('comp.card.watched')}
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
    <Link to={href} state={!__LIBRARY_EDITION__ && card.online ? { title: card.title, poster: card.poster } : undefined} className={className}>
      {body}
    </Link>
  )
}
