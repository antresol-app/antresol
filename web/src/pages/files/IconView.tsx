// «Значки»: a grid of thumbnails with two-line captions truncated in the
// middle (as Finder does), so «Interstellar.2014…FGT.mkv» keeps its ending.
import { memo, useMemo } from 'react'
import type { FileEntry } from '../../api/types'
import { fitMiddle, objects } from './model'
import { Thumb } from './Thumb'
import { itemId, type ViewProps } from './views'
import styles from './IconView.module.css'

let ctx: CanvasRenderingContext2D | null | undefined
function measure(s: string): number {
  if (ctx === undefined) {
    try {
      ctx = document.createElement('canvas').getContext('2d')
      if (ctx) ctx.font = "500 12px -apple-system, BlinkMacSystemFont, 'SF Pro Text', Inter, sans-serif"
    } catch {
      ctx = null
    }
  }
  return ctx ? ctx.measureText(s).width : s.length * 6.6
}

/** Caption width per line (px) and the budget for two lines. */
const LINE = 104
const BUDGET = LINE * 1.8

// Measuring is the costly part of a 5000-item grid: remember each name's caption.
const captions = new Map<string, string>()
function caption(name: string): string {
  let c = captions.get(name)
  if (c === undefined) {
    c = fitMiddle(name, BUDGET, measure)
    if (captions.size > 20000) captions.clear()
    captions.set(name, c)
  }
  return c
}

const Tile = memo(function Tile({
  entry,
  selected,
  lead,
  active,
  size,
  h,
}: {
  entry: FileEntry
  selected: boolean
  lead: boolean
  active: boolean
  size: number
  h: Pick<ViewProps, 'onSelect' | 'onOpen' | 'onMenu'>
}) {
  // A selected caption shows the whole name, as in Finder.
  const text = selected ? entry.name : caption(entry.name)
  return (
    <div
      id={itemId(entry.path)}
      role="option"
      aria-selected={selected}
      aria-label={entry.name}
      data-name={entry.name}
      data-path={entry.path}
      data-lead={lead || undefined}
      className={`${styles.tile} ${selected ? (active ? styles.selected : styles.selectedInactive) : ''}`}
      onClick={(e) => h.onSelect(e, entry)}
      onDoubleClick={() => h.onOpen(entry)}
      onContextMenu={(e) => h.onMenu(e, entry)}
      title={text !== entry.name ? entry.name : undefined}
    >
      <span className={styles.art}>
        <Thumb entry={entry} size={size} />
      </span>
      <span className={styles.caption}>
        <span className={styles.name}>{text}</span>
      </span>
      {entry.isDir && entry.childCount !== undefined && <span className={styles.meta}>{objects(entry.childCount)}</span>}
    </div>
  )
})

export function IconView({ entries, selection, container, active, empty, onSelect, onOpen, onMenu, onClear, compact }: ViewProps & { compact?: boolean }) {
  const h = useMemo(() => ({ onSelect, onOpen, onMenu }), [onSelect, onOpen, onMenu])
  const size = compact ? 72 : 84
  return (
    <div
      {...container}
      role="listbox"
      aria-multiselectable="true"
      className={`${styles.grid} ${compact ? styles.compact : ''}`}
      onClick={(e) => {
        if (e.target === e.currentTarget) onClear()
      }}
      onContextMenu={(e) => {
        if (e.target === e.currentTarget) onMenu(e, null)
      }}
    >
      {entries.length === 0 && empty}
      {entries.map((e) => (
        <Tile
          key={e.path}
          entry={e}
          selected={selection.selected.has(e.path)}
          lead={selection.lead === e.path}
          active={active}
          size={size}
          h={h}
        />
      ))}
    </div>
  )
}
