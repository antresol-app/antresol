// A surface: the rounded solid panel (content is never glass) that holds a block of a page (a
// chart, a download, an empty state). One radius, one fill, one hairline;
// three paddings. A list of rows is a GroupedSection, not a Card; a poster
// tile of the library is components/Card (MediaCard), not this.
import type { HTMLAttributes, ReactNode } from 'react'
import styles from './Card.module.css'

export type CardPadding = 'none' | 'sm' | 'md' | 'lg'

export interface CardProps extends HTMLAttributes<HTMLElement> {
  as?: 'div' | 'section' | 'li' | 'article'
  padding?: CardPadding
  children?: ReactNode
}

export function Card({ as: Tag = 'div', padding = 'md', className, children, ...rest }: CardProps) {
  return (
    <Tag className={[styles.card, styles[padding], className].filter(Boolean).join(' ')} {...rest}>
      {children}
    </Tag>
  )
}
