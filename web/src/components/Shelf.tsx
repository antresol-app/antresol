// A titled horizontal row of cards: a Rail (scroll-snap, chevrons on hover).
import { useId } from 'react'
import { Link } from 'react-router'
import type { Card as CardData } from '../api/types'
import { Card, cardKey, type CardVariant } from './Card'
import { Icon } from './Icon'
import { Rail } from './Rail'
import styles from './Shelf.module.css'

export interface ShelfProps {
  title: string
  cards: CardData[]
  variant?: CardVariant
  /** «Все» link to the full list. */
  seeAllTo?: string
  /** Eager-load the first visible cards (top shelf). */
  priority?: boolean
  /**
   * The shelf sits in a page's centred column (--column-max), not across
   * the window: a wider window does not add columns, so its posters keep
   * the size they have where the column stops growing.
   */
  contained?: boolean
}

export function Shelf({ title, cards, variant = 'poster', seeAllTo, priority = false, contained = false }: ShelfProps) {
  const headingId = useId()
  if (cards.length === 0) return null

  return (
    <section className={[styles.shelf, styles[variant], contained ? styles.contained : ''].filter(Boolean).join(' ')} aria-labelledby={headingId}>
      <div className={styles.header}>
        <h2 id={headingId} className={styles.title}>
          {seeAllTo ? (
            <Link to={seeAllTo} className={styles.titleLink}>
              {title}
              <Icon name="chevronRight" size={18} strokeWidth={2.4} />
            </Link>
          ) : (
            title
          )}
        </h2>
      </div>
      <Rail>
        {cards.map((c, i) => (
          <li key={cardKey(c)} className={styles.cell}>
            <Card card={c} variant={variant} priority={priority && i < 8} />
          </li>
        ))}
      </Rail>
    </section>
  )
}
