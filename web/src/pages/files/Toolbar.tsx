// Finder toolbar: back / forward, the path as breadcrumbs, the view switcher
// (Значки / Список / Колонки), sort menu, «Новая папка», «Свойства» and a
// search field that searches inside the current folder.
import { useRef, type ReactNode } from 'react'
import { Link } from 'react-router'
import type { FileSort, SortOrder } from '../../api/files'
import { MenuItem, MenuSeparator, Menu, type TriggerProps } from '../../ui/Menu'
import { Glyph, type GlyphName } from './glyphs'
import type { ViewMode } from './views'
import styles from './Toolbar.module.css'
import { useT, type Key } from '../../i18n'

export interface Crumb {
  label: string
  to: string
}

export const SORTS: { key: FileSort; label: Key }[] = [
  { key: 'name', label: 'files.col.name' },
  { key: 'kind', label: 'files.col.kind' },
  { key: 'mtime', label: 'files.sort.modified' },
  { key: 'size', label: 'files.col.size' },
]

const VIEWS: { value: ViewMode; label: Key; glyph: GlyphName }[] = [
  { value: 'icons', label: 'files.view.icons', glyph: 'gridView' },
  { value: 'list', label: 'files.view.list', glyph: 'listView' },
  { value: 'columns', label: 'files.view.columns', glyph: 'columnView' },
]

export function ViewSwitch({ value, onChange, views = VIEWS }: { value: ViewMode; onChange: (v: ViewMode) => void; views?: typeof VIEWS }) {
  const t = useT()
  const refs = useRef<(HTMLButtonElement | null)[]>([])
  const i = Math.max(0, views.findIndex((v) => v.value === value))
  return (
    <div
      className={styles.segmented}
      role="radiogroup"
      aria-label={t('files.view')}
      onKeyDown={(e) => {
        const d = e.key === 'ArrowRight' ? 1 : e.key === 'ArrowLeft' ? -1 : 0
        if (!d) return
        e.preventDefault()
        e.stopPropagation()
        const n = (i + d + views.length) % views.length
        onChange(views[n]!.value)
        refs.current[n]?.focus()
      }}
    >
      {views.map((v, k) => (
        <button
          key={v.value}
          ref={(el) => {
            refs.current[k] = el
          }}
          type="button"
          role="radio"
          aria-checked={v.value === value}
          aria-label={t(v.label)}
          title={t(v.label)}
          tabIndex={v.value === value ? 0 : -1}
          className={styles.segment}
          onClick={() => onChange(v.value)}
        >
          <Glyph name={v.glyph} size={16} strokeWidth={1.8} />
        </button>
      ))}
    </div>
  )
}

export function SortMenu({
  sort,
  order,
  onSort,
  onOrder,
  trigger,
}: {
  sort: FileSort
  order: SortOrder
  onSort: (k: FileSort) => void
  onOrder: (o: SortOrder) => void
  trigger?: (p: TriggerProps) => ReactNode
}) {
  const t = useT()
  return (
    <Menu
      label={t('common.sort')}
      placement="bottom-end"
      trigger={
        trigger ??
        ((tp) => (
          <button {...tp} type="button" className={styles.tool} aria-label={t('common.sort')} title={t('common.sort')}>
            <Glyph name="sort" size={16} />
            <Glyph name="chevronDown" size={10} strokeWidth={2.6} className={styles.caret} />
          </button>
        ))
      }
    >
      {(close) => (
        <>
          {SORTS.map((s) => (
            <MenuItem
              key={s.key}
              label={t(s.label)}
              checked={s.key === sort}
              onSelect={() => {
                onSort(s.key)
                close()
              }}
            />
          ))}
          <MenuSeparator />
          <MenuItem
            label={t('files.sort.asc')}
            checked={order === 'asc'}
            onSelect={() => {
              onOrder('asc')
              close()
            }}
          />
          <MenuItem
            label={t('files.sort.desc')}
            checked={order === 'desc'}
            onSelect={() => {
              onOrder('desc')
              close()
            }}
          />
        </>
      )}
    </Menu>
  )
}

export function SearchField({ value, onChange, placeholder, className }: { value: string; onChange: (v: string) => void; placeholder: string; className?: string }) {
  const t = useT()
  return (
    <label className={`${styles.search} ${className ?? ''}`}>
      <Glyph name="search" size={14} strokeWidth={2.1} className={styles.searchGlyph} />
      <input
        type="search"
        value={value}
        placeholder={placeholder}
        aria-label={placeholder}
        onChange={(e) => onChange(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Escape' && value) {
            e.preventDefault()
            e.stopPropagation()
            onChange('')
          }
        }}
        spellCheck={false}
        autoComplete="off"
      />
      {value && (
        <button type="button" className={styles.clear} aria-label={t('files.search.clear')} onClick={() => onChange('')}>
          <Glyph name="xmarkCircle" size={14} />
        </button>
      )}
    </label>
  )
}

export interface ToolbarProps {
  canBack: boolean
  canForward: boolean
  onBack: () => void
  onForward: () => void
  crumbs: Crumb[]
  view?: ViewMode
  onView?: (v: ViewMode) => void
  sort?: FileSort
  order?: SortOrder
  onSort?: (k: FileSort) => void
  onOrder?: (o: SortOrder) => void
  query?: string
  onQuery?: (q: string) => void
  searchPlaceholder?: string
  infoOpen?: boolean
  onInfo?: () => void
  onNewFolder?: () => void
  /** Extra buttons (Корзина: «Восстановить», «Очистить»). */
  children?: ReactNode
}

export function Toolbar(p: ToolbarProps) {
  const t = useT()
  const last = p.crumbs[p.crumbs.length - 1]
  // Long paths: first crumb, …, the last two (as the Finder path bar does).
  const shown = p.crumbs.length > 4 ? [p.crumbs[0]!, null, ...p.crumbs.slice(-2)] : p.crumbs
  return (
    <div className={styles.toolbar} role="toolbar" aria-label={t('files.toolbar')}>
      <div className={styles.nav}>
        <button type="button" className={styles.tool} aria-label={t('common.back')} title={t('common.back')} disabled={!p.canBack} onClick={p.onBack}>
          <Glyph name="chevronLeft" size={17} strokeWidth={2.2} />
        </button>
        <button type="button" className={styles.tool} aria-label={t('common.forward')} title={t('common.forward')} disabled={!p.canForward} onClick={p.onForward}>
          <Glyph name="chevronRight" size={17} strokeWidth={2.2} />
        </button>
      </div>
      <nav className={styles.crumbs} aria-label={t('files.path')}>
        <ol>
          {shown.map((c, i) =>
            c === null ? (
              <li key="gap" className={styles.gap} aria-hidden>
                …
              </li>
            ) : (
              <li key={c.to}>
                {c === last ? (
                  <h1 className={styles.current} aria-current="page" title={c.label}>
                    {c.label}
                  </h1>
                ) : (
                  <Link to={c.to} className={styles.crumb} title={c.label}>
                    {c.label}
                  </Link>
                )}
                {i < shown.length - 1 && <Glyph name="chevronRight" size={10} strokeWidth={2.6} className={styles.sep} />}
              </li>
            ),
          )}
        </ol>
      </nav>
      <div className={styles.tools}>
        {p.children}
        {p.view && p.onView && <ViewSwitch value={p.view} onChange={p.onView} />}
        {p.sort && p.order && p.onSort && p.onOrder && <SortMenu sort={p.sort} order={p.order} onSort={p.onSort} onOrder={p.onOrder} />}
        {p.onNewFolder && (
          <button type="button" className={styles.tool} aria-label={t('files.newFolder')} title={t('files.newFolder')} onClick={p.onNewFolder}>
            <Glyph name="folderPlus" size={17} />
          </button>
        )}
        {p.onInfo && (
          <button
            type="button"
            className={`${styles.tool} ${p.infoOpen ? styles.pressed : ''}`}
            aria-label={t('files.info')}
            aria-pressed={p.infoOpen}
            title={t('files.infoShortcut')}
            onClick={p.onInfo}
          >
            <Glyph name="info" size={17} />
          </button>
        )}
        {p.onQuery && <SearchField value={p.query ?? ''} onChange={p.onQuery} placeholder={p.searchPlaceholder ?? t('common.search')} />}
      </div>
    </div>
  )
}
