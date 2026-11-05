// «Список»: Finder's list view. Columns Имя / Изменён / Размер / Тип; a click
// on a header sorts (again: reverses), disclosure triangles open folders in
// place. On a phone it becomes the iOS «Файлы» list: thumbnail, name, date
// and size underneath, a «⋯» button for actions.
import { memo, useMemo, type MouseEvent } from 'react'
import type { FileSort, SortOrder } from '../../api/files'
import type { FileEntry } from '../../api/types'
import { formatDate, formatShortDate, formatSize, kindLabel, objects } from './model'
import { Glyph } from './glyphs'
import { Thumb } from './Thumb'
import { itemId, type ViewProps } from './views'
import styles from './ListView.module.css'
import { useT, type Key } from '../../i18n'

export interface Row {
  entry: FileEntry
  level: number
  /** Children are being loaded (expanded folder). */
  loading?: boolean
}

export interface ListViewProps extends Omit<ViewProps, 'entries'> {
  rows: Row[]
  expanded: ReadonlySet<string>
  onToggle: (entry: FileEntry) => void
  sort: FileSort
  order: SortOrder
  onSort: (key: FileSort) => void
  /** Phone layout (iOS «Файлы»). */
  compact?: boolean
  /** Phone: the «⋯» button of a row. */
  onMore?: (e: MouseEvent, entry: FileEntry) => void
  /** Search results: show where each item is. */
  showWhere?: boolean
}

const COLUMNS: { key: FileSort; label: Key; className: string }[] = [
  { key: 'name', label: 'files.col.name', className: styles.colName! },
  { key: 'mtime', label: 'files.col.modified', className: styles.colDate! },
  { key: 'size', label: 'files.col.size', className: styles.colSize! },
  { key: 'kind', label: 'files.col.kind', className: styles.colKind! },
]

const where = (p: string) => {
  const i = p.lastIndexOf('/')
  return i < 0 ? '' : p.slice(0, i).split('/').join(' › ')
}

const DesktopRow = memo(function DesktopRow({
  row,
  index,
  selected,
  lead,
  active,
  expanded,
  showWhere,
  h,
}: {
  row: Row
  index: number
  selected: boolean
  lead: boolean
  active: boolean
  expanded: boolean
  showWhere?: boolean
  h: Pick<ListViewProps, 'onSelect' | 'onOpen' | 'onMenu' | 'onToggle'>
}) {
  const t = useT()
  const e = row.entry
  const cls = [styles.row, index % 2 ? styles.odd : '', selected ? (active ? styles.selected : styles.selectedInactive) : ''].join(' ')
  return (
    <div
      id={itemId(e.path)}
      role="row"
      aria-selected={selected}
      aria-level={row.level + 1}
      aria-expanded={e.isDir ? expanded : undefined}
      aria-label={e.name}
      data-name={e.name}
      data-path={e.path}
      data-lead={lead || undefined}
      className={cls}
      onClick={(ev) => h.onSelect(ev, e)}
      onDoubleClick={() => h.onOpen(e)}
      onContextMenu={(ev) => h.onMenu(ev, e)}
    >
      <div role="gridcell" className={`${styles.cell} ${styles.colName}`} style={{ paddingLeft: 8 + row.level * 18 }}>
        <span className={styles.disclosureBox}>
          {e.isDir && (
            <span
              className={`${styles.disclosure} ${expanded ? styles.open : ''}`}
              aria-hidden
              onClick={(ev) => {
                ev.stopPropagation()
                h.onToggle(e)
              }}
              onDoubleClick={(ev) => ev.stopPropagation()}
            >
              <Glyph name="disclosure" size={13} />
            </span>
          )}
        </span>
        <Thumb entry={e} size={20} />
        <span className={styles.name}>{e.name}</span>
        {showWhere && where(e.path) && <span className={styles.where}>{where(e.path)}</span>}
        {row.loading && <span className={styles.spinner} aria-label={t('common.loading')} />}
      </div>
      <div role="gridcell" className={`${styles.cell} ${styles.colDate} ${styles.dim}`}>
        {formatDate(e.mtime)}
      </div>
      <div role="gridcell" className={`${styles.cell} ${styles.colSize} ${styles.dim}`}>
        {e.isDir ? '—' : formatSize(e.size)}
      </div>
      <div role="gridcell" className={`${styles.cell} ${styles.colKind} ${styles.dim}`}>
        {kindLabel(e)}
      </div>
    </div>
  )
})

const PhoneRow = memo(function PhoneRow({
  entry: e,
  selected,
  h,
  onMore,
}: {
  entry: FileEntry
  selected: boolean
  h: Pick<ListViewProps, 'onSelect' | 'onOpen' | 'onMenu'>
  onMore?: ListViewProps['onMore']
}) {
  const t = useT()
  const sub = [formatShortDate(e.mtime), e.isDir ? (e.childCount !== undefined ? objects(e.childCount) : '') : formatSize(e.size)]
    .filter(Boolean)
    .join(' · ')
  return (
    <div
      id={itemId(e.path)}
      role="row"
      aria-selected={selected}
      aria-label={e.name}
      data-name={e.name}
      data-path={e.path}
      className={`${styles.prow} ${selected ? styles.pselected : ''}`}
      onClick={() => h.onOpen(e)}
      onContextMenu={(ev) => h.onMenu(ev, e)}
    >
      <div role="gridcell" className={styles.pthumb}>
        <Thumb entry={e} size={44} />
      </div>
      <div role="gridcell" className={styles.ptext}>
        <span className={styles.pname}>{e.name}</span>
        <span className={styles.psub}>{sub}</span>
      </div>
      <div role="gridcell" className={styles.pmore}>
        <button
          type="button"
          className={styles.moreButton}
          aria-label={t('files.row.actions', { name: e.name })}
          onClick={(ev) => {
            ev.stopPropagation()
            onMore?.(ev, e)
          }}
        >
          <Glyph name="ellipsisCircle" size={22} strokeWidth={1.6} />
        </button>
      </div>
    </div>
  )
})

export function ListView(p: ListViewProps) {
  const t = useT()
  const { rows, selection, container, active, empty, expanded, sort, order, onSort, compact } = p
  const h = useMemo(() => ({ onSelect: p.onSelect, onOpen: p.onOpen, onMenu: p.onMenu, onToggle: p.onToggle }), [p.onSelect, p.onOpen, p.onMenu, p.onToggle])

  if (compact) {
    return (
      <div {...container} role="grid" aria-multiselectable="true" className={styles.phone}>
        {rows.length === 0 && empty}
        {rows.map((r) => (
          <PhoneRow key={r.entry.path} entry={r.entry} selected={selection.selected.has(r.entry.path)} h={h} onMore={p.onMore} />
        ))}
      </div>
    )
  }

  return (
    <div
      {...container}
      role="treegrid"
      aria-multiselectable="true"
      className={styles.table}
      onClick={(e) => {
        if (e.target === e.currentTarget || (e.target as HTMLElement).dataset.filler) p.onClear()
      }}
      onContextMenu={(e) => {
        if (e.target === e.currentTarget || (e.target as HTMLElement).dataset.filler) p.onMenu(e, null)
      }}
    >
      <div role="row" className={styles.header}>
        {COLUMNS.map((c) => {
          const on = c.key === sort
          return (
            <div
              key={c.key}
              role="columnheader"
              aria-sort={on ? (order === 'asc' ? 'ascending' : 'descending') : 'none'}
              className={`${styles.hcell} ${c.className}`}
            >
              <button type="button" tabIndex={-1} className={`${styles.hbutton} ${on ? styles.hon : ''}`} onClick={() => onSort(c.key)}>
                <span>{t(c.label)}</span>
                <span className={styles.arrow} aria-hidden>
                  {on && <Glyph name="chevronDown" size={11} strokeWidth={2.4} style={{ transform: order === 'asc' ? 'rotate(180deg)' : undefined }} />}
                </span>
              </button>
            </div>
          )
        })}
      </div>
      {rows.length === 0 && empty}
      {rows.map((r, i) => (
        <DesktopRow
          key={r.entry.path}
          row={r}
          index={i}
          selected={selection.selected.has(r.entry.path)}
          lead={selection.lead === r.entry.path}
          active={active}
          expanded={expanded.has(r.entry.path)}
          showWhere={p.showWhere}
          h={h}
        />
      ))}
      <div className={styles.filler} data-filler="1" aria-hidden />
    </div>
  )
}
