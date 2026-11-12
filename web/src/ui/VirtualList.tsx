// A long list that renders only the rows near the screen (the external
// source's channel table: thousands of rows). Rows have one fixed height;
// the list keeps its full height, so the page (or the scroller around it)
// scrolls as if every row were there and the scroll bar tells the truth.
// It follows whichever scroller moves (the window or an ancestor): every
// scroll is heard in the capture phase and the rows in the window's
// visible band, plus `overscan` above and below, are rendered.
//   role / itemRole — 'list'/'listitem' (default), or 'rowgroup'/'row' in a
//   table whose header row the caller draws above it.
import { useCallback, useLayoutEffect, useRef, useState, type CSSProperties, type ReactNode } from 'react'
import styles from './VirtualList.module.css'

export interface VirtualListProps {
  count: number
  /** Every row's height in CSS px (a phone may pass another). */
  itemHeight: number
  renderItem: (index: number) => ReactNode
  /** A stable key for row `index` (default: the index). */
  itemKey?: (index: number) => string | number
  /** Rows rendered past each edge of the visible band. */
  overscan?: number
  role?: 'list' | 'rowgroup'
  itemRole?: 'listitem' | 'row'
  label?: string
  className?: string
  itemClassName?: string
}

/** The rows [first, last) a list whose top is at `top` (viewport px) shows in a viewport `height` tall. */
export function visibleRange(top: number, height: number, itemHeight: number, count: number, overscan: number): [number, number] {
  if (count === 0 || itemHeight <= 0) return [0, 0]
  const first = Math.max(0, Math.floor(-top / itemHeight) - overscan)
  const last = Math.min(count, Math.ceil((height - top) / itemHeight) + overscan)
  return [Math.min(first, count), Math.max(Math.min(first, count), last)]
}

export function VirtualList({ count, itemHeight, renderItem, itemKey = (i) => i, overscan = 6, role = 'list', itemRole = 'listitem', label, className, itemClassName }: VirtualListProps) {
  const ref = useRef<HTMLDivElement>(null)
  const [range, setRange] = useState<[number, number]>(() => visibleRange(0, typeof window === 'undefined' ? 800 : window.innerHeight, itemHeight, count, overscan))
  const frame = useRef(0)

  const measure = useCallback(() => {
    frame.current = 0
    const el = ref.current
    if (!el) return
    const next = visibleRange(el.getBoundingClientRect().top, window.innerHeight, itemHeight, count, overscan)
    setRange((r) => (r[0] === next[0] && r[1] === next[1] ? r : next))
  }, [count, itemHeight, overscan])

  useLayoutEffect(() => {
    measure()
    const schedule = () => {
      if (!frame.current) frame.current = window.requestAnimationFrame(measure)
    }
    window.addEventListener('scroll', schedule, { capture: true, passive: true })
    window.addEventListener('resize', schedule)
    return () => {
      window.removeEventListener('scroll', schedule, { capture: true })
      window.removeEventListener('resize', schedule)
      if (frame.current) window.cancelAnimationFrame(frame.current)
      frame.current = 0
    }
  }, [measure])

  const items: ReactNode[] = []
  // the range is measured after a render: a list that just got shorter clamps it until then
  for (let i = Math.min(range[0], count); i < Math.min(range[1], count); i++) {
    const style: CSSProperties = { height: itemHeight, transform: `translateY(${i * itemHeight}px)` }
    items.push(
      <div
        key={itemKey(i)}
        role={itemRole}
        className={[styles.item, itemClassName].filter(Boolean).join(' ')}
        style={style}
        aria-rowindex={itemRole === 'row' ? i + 2 : undefined}
        aria-posinset={itemRole === 'listitem' ? i + 1 : undefined}
        aria-setsize={itemRole === 'listitem' ? count : undefined}
        data-index={i}
      >
        {renderItem(i)}
      </div>,
    )
  }
  return (
    <div ref={ref} role={role} aria-label={label} className={[styles.list, className].filter(Boolean).join(' ')} style={{ height: count * itemHeight }}>
      {items}
    </div>
  )
}
