// An empty page or panel: nothing here yet (an empty library, a trash with
// nothing in it) or what was asked for failed to load. A glyph, a title, one
// line of what to do, the actions under it, centred a little above the
// middle of the window. A list of rows that is empty inside a group is not
// this: that is a ListRow with a hint.
//   icon     the glyph (an `Icon`; it is drawn in the tertiary label)
//   title    the heading (`as`: h2 by default; h1 when it is the page's only one)
//   hint     one line under it (cut to 42 characters, balanced)
//   actions  buttons: they stand as the column's last items
//   inset    the page's gutters at the sides (a page that has none of its own)
//   role     "alert" for a failure to load
import type { ReactNode } from 'react'
import styles from './EmptyState.module.css'

export interface EmptyStateProps {
  icon?: ReactNode
  title: ReactNode
  hint?: ReactNode
  actions?: ReactNode
  as?: 'h1' | 'h2' | 'h3'
  inset?: boolean
  role?: 'alert' | 'status'
  className?: string
}

export function EmptyState({ icon, title, hint, actions, as: Heading = 'h2', inset, role, className }: EmptyStateProps) {
  return (
    <div className={[styles.empty, inset ? styles.inset : '', className].filter(Boolean).join(' ')} role={role}>
      {icon && <span className={styles.icon}>{icon}</span>}
      <Heading className={styles.title}>{title}</Heading>
      {hint && <p className={styles.hint}>{hint}</p>}
      {actions}
    </div>
  )
}
