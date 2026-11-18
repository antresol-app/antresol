// How a finished title looks on its tile (a card, an episode, a comic's
// volume): its picture dimmed (`dimClass` on the <img> and the fallback
// title) and this mark in the middle of it, never a full progress bar as well
// (web/DESIGN.md, «A watched title»). The apps do the same: half-dark
// art and the eye in the middle.
import type { TFunction } from '../i18n'
import { Icon } from './Icon'
import styles from './WatchedMark.module.css'

/** The class of a finished title's picture. */
export const dimClass = styles.dim

/** The word that says «done» for a kind: read, listened to, watched. */
export function doneLabel(t: TFunction, kind: string): string {
  if (kind === 'book' || kind === 'comic' || kind === 'comic_volume') return t('comp.card.read')
  return kind === 'audiobook' ? t('comp.card.listened') : t('comp.card.watched')
}

/**
 * The eye on its disc, centred over the picture (the host's picture box is positioned and a size
 * container). `label` is part of the tile's name («Матрица Просмотрено 1999»).
 */
export function WatchedMark({ label, className }: { label: string; className?: string }) {
  return (
    <span className={[styles.mark, className].filter(Boolean).join(' ')} role="img" aria-label={label} title={label}>
      <Icon name="eye" size={16} strokeWidth={2.2} />
    </span>
  )
}
