// «Колонки»: Miller columns. One column per folder on the way from the root
// to the current folder; a selected folder opens as the next column, a
// selected file shows its preview on the right.
import { useEffect, useRef, type MouseEvent } from 'react'
import type { FileSort, SortOrder } from '../../api/files'
import type { FileEntry } from '../../api/types'
import { joinPath, sortEntries, type Selection } from './model'
import { Glyph } from './glyphs'
import { InfoContent } from './InfoPanel'
import { Thumb } from './Thumb'
import { useDir } from './useDir'
import { itemId, type ContainerProps } from './views'
import styles from './ColumnView.module.css'
import { useT } from '../../i18n'

export interface ColumnViewProps {
  root: string
  /** The folder of the active column. */
  path: string
  entries: FileEntry[]
  selection: Selection
  sort: FileSort
  order: SortOrder
  container: ContainerProps
  active: boolean
  /** A click in the column of `dir` (may be another column than the active one). */
  onColumnSelect: (dir: string, e: MouseEvent, entry: FileEntry) => void
  onOpen: (entry: FileEntry) => void
  onMenu: (e: MouseEvent, entry: FileEntry | null) => void
  onClear: () => void
  whereOf: (e: FileEntry) => string
  refresh?: number
}

function Column({
  dir,
  entries,
  isActive,
  highlight,
  selection,
  focused,
  p,
}: {
  dir: string
  entries: FileEntry[] | undefined
  isActive: boolean
  /** The child on the way to the active column. */
  highlight?: string
  selection?: Selection
  focused: boolean
  p: ColumnViewProps
}) {
  const t = useT()
  return (
    <div
      className={styles.column}
      role="listbox"
      aria-multiselectable={isActive || undefined}
      aria-label={dir ? dir.slice(dir.lastIndexOf('/') + 1) : t('files.col.root')}
      onContextMenu={(e) => {
        if (e.target === e.currentTarget) p.onMenu(e, null)
      }}
    >
      {entries === undefined && <div className={styles.loading} aria-label={t('common.loading')} />}
      {entries?.length === 0 && <div className={styles.empty}>{t('files.col.empty')}</div>}
      {entries?.map((e) => {
        const sel = isActive ? Boolean(selection?.selected.has(e.path)) : e.path === highlight
        const blue = sel && isActive && focused
        return (
          <div
            key={e.path}
            id={isActive ? itemId(e.path) : undefined}
            role="option"
            aria-selected={sel}
            aria-label={e.name}
            data-name={e.name}
            className={`${styles.cell} ${sel ? (blue ? styles.selected : styles.path) : ''}`}
            onClick={(ev) => p.onColumnSelect(dir, ev, e)}
            onDoubleClick={() => p.onOpen(e)}
            onContextMenu={(ev) => {
              p.onColumnSelect(dir, ev, e)
              p.onMenu(ev, e)
            }}
          >
            <Thumb entry={e} size={18} />
            <span className={styles.name}>{e.name}</span>
            {e.isDir && <Glyph name="chevronRight" size={11} strokeWidth={2.6} className={styles.chevron} />}
          </div>
        )
      })}
    </div>
  )
}

function DirColumn({ dir, highlight, p }: { dir: string; highlight?: string; p: ColumnViewProps }) {
  const { data } = useDir(p.root, dir)
  const entries = data ? sortEntries(data.entries, p.sort, p.order) : undefined
  return <Column dir={dir} entries={entries} isActive={false} highlight={highlight} focused={p.active} p={p} />
}

export function ColumnView(p: ColumnViewProps) {
  const { path, entries, selection, container, active } = p
  const scroller = useRef<HTMLDivElement | null>(null)
  const parts = path ? path.split('/') : []
  const ancestors = parts.map((_, i) => parts.slice(0, i).join('/'))
  const one = selection.selected.size === 1 ? entries.find((e) => selection.selected.has(e.path)) : undefined

  // Keep the newest column in view.
  useEffect(() => {
    const el = scroller.current
    if (el) el.scrollTo({ left: el.scrollWidth, behavior: 'smooth' })
  }, [path, one?.path])

  const { ref: containerRef, ...rest } = container
  return (
    <div
      {...rest}
      ref={(el) => {
        scroller.current = el
        if (typeof containerRef === 'function') containerRef(el)
        else if (containerRef) (containerRef as { current: HTMLDivElement | null }).current = el
      }}
      role="group"
      className={styles.columns}
      onClick={(e) => {
        if (e.target === e.currentTarget) p.onClear()
      }}
      onContextMenu={(e) => {
        if (e.target === e.currentTarget) p.onMenu(e, null)
      }}
    >
      {ancestors.map((dir, i) => (
        <DirColumn key={dir || '/'} dir={dir} highlight={joinPath(dir, parts[i]!)} p={p} />
      ))}
      <Column dir={path} entries={entries} isActive highlight={undefined} selection={selection} focused={active} p={p} />
      {one?.isDir && <DirColumn key={`next:${one.path}`} dir={one.path} p={p} />}
      {one && !one.isDir && (
        <div className={styles.preview}>
          <InfoContent root={p.root} entry={one} where={p.whereOf(one)} compact refresh={p.refresh} />
        </div>
      )}
    </div>
  )
}
