// A responsive grid of poster cards for the Фильмы / Сериалы sections.
import type { Card as CardData } from '../api/types'
import { Card, cardKey, type CardVariant } from './Card'
import styles from './PosterGrid.module.css'

export interface PosterGridProps {
  cards: CardData[]
  label: string
  /** `square` for albums and playlists. */
  variant?: CardVariant
}

export function PosterGrid({ cards, label, variant = 'poster' }: PosterGridProps) {
  return (
    <ul className={`${styles.grid} ${variant === 'square' ? styles.squareGrid : ''}`} aria-label={label}>
      {cards.map((c, i) => (
        <li key={cardKey(c)} className={styles.cell}>
          <Card card={c} variant={variant} priority={i < 14} />
        </li>
      ))}
    </ul>
  )
}

export function PosterGridSkeleton({ count = 21, variant = 'poster' }: { count?: number; variant?: CardVariant }) {
  return (
    <ul className={`${styles.grid} ${variant === 'square' ? styles.squareGrid : ''}`} aria-hidden>
      {Array.from({ length: count }, (_, i) => (
        <li key={i} className={styles.cell}>
          <div className={styles.skeleton} />
        </li>
      ))}
    </ul>
  )
}
