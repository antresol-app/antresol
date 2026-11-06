// Finder toolbar: back / forward, the path as breadcrumbs, the view switcher
// (Значки / Список / Колонки), sort menu, «Новая папка», «Свойства» and a
// search field that searches inside the current folder — all of them the
// app's controls (ui/), the tools glass over the window as in macOS 26.
import type { ReactNode } from 'react'
import { Link } from 'react-router'
import type { FileSort, SortOrder } from '../../api/files'
import { IconButton } from '../../ui/IconButton'
import { MenuItem, MenuSeparator, Menu, type TriggerProps } from '../../ui/Menu'
import { SegmentedControl } from '../../ui/SegmentedControl'
import { TextField } from '../../ui/TextField'
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
  return (
    <SegmentedControl
      size="small"
      label={t('files.view')}
      value={value}
      onChange={onChange}
      segments={views.map((v) => ({ value: v.value, label: t(v.label), icon: <Glyph name={v.glyph} size={16} strokeWidth={1.8} /> }))}
    />
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
        ((tp) => <IconButton {...tp} size="sm" material="glass" icon={<Glyph name="sort" size={16} />} label={t('common.sort')} />)
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
    <TextField
      type="search"
      size="small"
      label={placeholder}
      hideLabel
      placeholder={placeholder}
      className={`${styles.search} ${className ?? ''}`}
      value={value}
      onChange={(e) => onChange(e.target.value)}
      onClear={() => onChange('')}
      clearLabel={t('files.search.clear')}
      spellCheck={false}
      autoComplete="off"
    />
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
      {/* back and forward share one glass capsule, as in macOS 26 */}
      <div className={`${styles.nav} glass`}>
        <IconButton size="sm" variant="plain" icon="chevronLeft" label={t('common.back')} disabled={!p.canBack} onClick={p.onBack} />
        <IconButton size="sm" variant="plain" icon="chevronRight" label={t('common.forward')} disabled={!p.canForward} onClick={p.onForward} />
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
        {p.onNewFolder && <IconButton size="sm" material="glass" icon={<Glyph name="folderPlus" size={17} />} label={t('files.newFolder')} onClick={p.onNewFolder} />}
        {p.onInfo && (
          <IconButton
            size="sm"
            material="glass"
            icon={<Glyph name="info" size={17} />}
            label={t('files.info')}
            tooltip={t('files.infoShortcut')}
            toggled={p.infoOpen}
            onClick={p.onInfo}
          />
        )}
        {p.onQuery && <SearchField value={p.query ?? ''} onChange={p.onQuery} placeholder={p.searchPlaceholder ?? t('common.search')} />}
      </div>
    </div>
  )
}
