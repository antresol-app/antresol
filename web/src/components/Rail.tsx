// A horizontal rail of cards with scroll-snap — Home's shelves, a series'
// episodes. On devices with a mouse, round chevrons at the rail's edges page
// through it on hover, each hidden at its own end; touch and trackpads just
// scroll. The caller lays out the cells and says where the chevrons sit
// vertically (--rail-pager-top: the middle of the artwork, not of the text).
import { useCallback, useLayoutEffect, useRef, useState, type ReactNode, type RefObject } from 'react'
import { IconButton } from '../ui/IconButton'
import { useT } from '../i18n'
import styles from './Rail.module.css'

export interface RailProps {
  /** The list's accessible name (or `labelledBy`). */
  label?: string
  labelledBy?: string
  /** The `<li>` cells. */
  children: ReactNode
  /** Extra class of the list (its column width, --card-w). */
  listClassName?: string
  className?: string
  /** The list element, for a caller that scrolls it itself. */
  listRef?: RefObject<HTMLUListElement | null>
}

/** How far one chevron click goes: most of a screen, so the next card stays in context. */
export const PAGE = 0.85

export function Rail({ label, labelledBy, children, listClassName, className, listRef }: RailProps) {
  const t = useT()
  const own = useRef<HTMLUListElement>(null)
  const ref = listRef ?? own
  const [edges, setEdges] = useState({ start: true, end: true })

  const measure = useCallback(() => {
    const el = ref.current
    if (!el) return
    const max = el.scrollWidth - el.clientWidth
    const next = { start: el.scrollLeft <= 4, end: el.scrollLeft >= max - 4 }
    setEdges((e) => (e.start === next.start && e.end === next.end ? e : next))
  }, [ref])

  // after every render: the cells may have changed (another season)
  useLayoutEffect(measure)
  useLayoutEffect(() => {
    const el = ref.current
    if (!el || typeof ResizeObserver === 'undefined') return
    const ro = new ResizeObserver(measure)
    ro.observe(el)
    return () => ro.disconnect()
  }, [measure, ref])

  const page = (dir: 1 | -1) => {
    const el = ref.current
    if (!el) return
    const reduce = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches
    el.scrollBy({ left: dir * el.clientWidth * PAGE, behavior: reduce ? 'auto' : 'smooth' })
  }

  return (
    <div className={[styles.viewport, className].filter(Boolean).join(' ')}>
      <ul ref={ref} className={[styles.list, listClassName].filter(Boolean).join(' ')} aria-label={label} aria-labelledby={labelledBy} onScroll={measure}>
        {children}
      </ul>
      {/* pointer only: the keyboard and screen readers go through the cells,
          which scroll themselves into view — the chevrons stay out of their way */}
      <IconButton
        icon="chevronLeft"
        label={t('common.back')}
        material="glass"
        className={`${styles.pager} ${styles.prev}`}
        data-dir="prev"
        aria-hidden="true"
        tabIndex={-1}
        hidden={edges.start}
        onClick={() => page(-1)}
      />
      <IconButton
        icon="chevronRight"
        label={t('common.forward')}
        material="glass"
        className={`${styles.pager} ${styles.next}`}
        data-dir="next"
        aria-hidden="true"
        tabIndex={-1}
        hidden={edges.end}
        onClick={() => page(1)}
      />
    </div>
  )
}
