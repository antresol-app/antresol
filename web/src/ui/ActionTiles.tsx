// The secondary actions of a title page (watchlist, follow, «Ещё», delete):
// round IconButtons in the page's action row on a wide screen, a row of
// equal icon-over-label tiles on a phone — the TV app's row under «Play».
// Give each IconButton `tile` (or a shorter tile label); anything that
// wraps one (a Menu, a hint under «Следить») takes one tile's width.
import type { ReactNode } from 'react'
import styles from './ActionTiles.module.css'

export function ActionTiles({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <div className={[styles.tiles, className].filter(Boolean).join(' ')} data-action-tiles>
      {children}
    </div>
  )
}
