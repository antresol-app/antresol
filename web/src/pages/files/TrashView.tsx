// «Корзина» (admin): what was moved to the trash, where it came from and when.
// Restore puts it back; «Удалить навсегда» and «Очистить корзину» ask first.
import { useCallback, useEffect, useMemo, useState, type KeyboardEvent, type MouseEvent, type ReactNode } from 'react'
import { ApiError } from '../../api/client'
import { deleteTrash, getTrash, restoreTrash } from '../../api/files'
import type { FileRoot, TrashItem } from '../../api/types'
import { clickSelect, emptySelection, formatDate, formatSize, moveLead, objects, pruneSelection, selectAll, type Selection } from './model'
import { ContextMenu, type MenuEntry } from './ContextMenu'
import { FileIcon } from './FileIcon'
import { Glyph } from './glyphs'
import { Sheet } from './Sheet'
import { itemId } from './views'
import styles from './TrashView.module.css'
import { useT } from '../../i18n'

export interface TrashViewProps {
  roots: FileRoot[]
  compact?: boolean
  /** Something was restored or deleted (folders and storage refresh). */
  onChanged: () => void
  /** Renders the toolbar with the page's own buttons. */
  toolbar: (buttons: ReactNode) => ReactNode
}

const key = (t: TrashItem) => String(t.id)

export function TrashView({ roots, compact, onChanged, toolbar }: TrashViewProps) {
  const tr = useT()
  const [items, setItems] = useState<TrashItem[] | null>(null)
  const [error, setError] = useState<string>()
  const [sel, setSel] = useState<Selection>(emptySelection)
  const [menu, setMenu] = useState<{ x: number; y: number; items: TrashItem[] } | null>(null)
  const [confirm, setConfirm] = useState<{ kind: 'delete' | 'empty'; items: TrashItem[] } | null>(null)
  const [busy, setBusy] = useState(false)
  const [focused, setFocused] = useState(false)

  const load = useCallback(() => {
    getTrash().then(
      (list) => {
        const sorted = [...list].sort((a, b) => Date.parse(b.deletedAt) - Date.parse(a.deletedAt))
        setItems(sorted)
        setSel((s) => pruneSelection(s, sorted.map(key)))
        setError(undefined)
      },
      (e: unknown) => setError(e instanceof ApiError ? e.message : tr('files.trash.openFailed')),
    )
  }, [])
  useEffect(load, [load])

  const order = useMemo(() => (items ?? []).map(key), [items])
  const selected = (items ?? []).filter((t) => sel.selected.has(key(t)))
  const rootTitle = (id: string) => roots.find((r) => r.id === id)?.title ?? id
  const from = (t: TrashItem) => {
    const dir = t.path.includes('/') ? t.path.slice(0, t.path.lastIndexOf('/')) : ''
    return [rootTitle(t.root), ...(dir ? dir.split('/') : [])].join(' › ')
  }

  const run = async (list: TrashItem[], op: (t: TrashItem) => Promise<unknown>) => {
    setBusy(true)
    const failed: string[] = []
    for (const t of list) {
      try {
        await op(t)
      } catch (e) {
        failed.push(e instanceof ApiError ? tr('files.op.itemError', { name: t.name, error: e.message }) : tr('files.quoted', { name: t.name }))
      }
    }
    setBusy(false)
    setConfirm(null)
    setError(failed.length ? tr('files.trash.failed', { errors: failed.join('; ') }) : undefined)
    setSel(emptySelection)
    load()
    onChanged()
  }
  const restore = (list: TrashItem[]) => void run(list, (t) => restoreTrash(t.id))

  const onClick = (e: MouseEvent, t: TrashItem) => {
    setSel((s) => clickSelect(s, order, key(t), { toggle: e.metaKey || e.ctrlKey, range: e.shiftKey }))
  }
  const onMenu = (e: MouseEvent, t: TrashItem) => {
    e.preventDefault()
    const list = sel.selected.has(key(t)) ? selected : [t]
    if (!sel.selected.has(key(t))) setSel(clickSelect(sel, order, key(t), {}))
    setMenu({ x: e.clientX, y: e.clientY, items: list })
  }
  const onKey = (e: KeyboardEvent) => {
    const meta = e.metaKey || e.ctrlKey
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault()
      setSel((s) => moveLead(s, order, e.key === 'ArrowDown' ? 1 : -1, e.shiftKey))
    } else if (meta && e.key.toLowerCase() === 'a') {
      e.preventDefault()
      setSel(selectAll(order))
    } else if (meta && e.key === 'Backspace' && selected.length) {
      e.preventDefault()
      setConfirm({ kind: 'delete', items: selected })
    } else if (e.key === 'Escape') setSel(emptySelection)
  }

  const menuItems = (list: TrashItem[]): MenuEntry[] => [
    { id: 'restore', label: tr('files.trash.restore'), icon: 'restore', run: () => restore(list) },
    'separator',
    { id: 'delete', label: tr('files.trash.deleteForever'), icon: 'trash', destructive: true, run: () => setConfirm({ kind: 'delete', items: list }) },
  ]

  const buttons = (
    <>
      <button type="button" className={styles.action} disabled={!selected.length || busy} onClick={() => restore(selected)}>
        <Glyph name="restore" size={15} /> {tr('files.trash.restore')}
      </button>
      <button
        type="button"
        className={styles.action}
        disabled={!items?.length || busy}
        onClick={() => items && setConfirm({ kind: 'empty', items })}
      >
        {tr('files.trash.empty')}
      </button>
    </>
  )

  const count = items?.length ?? 0
  const label = confirm?.items.length === 1 ? tr('files.quoted', { name: confirm.items[0]!.name }) : objects(confirm?.items.length ?? 0)

  return (
    <>
      {toolbar(buttons)}
      <div className={styles.body}>
        {error && (
          <p className={styles.error} role="alert">
            {error}
          </p>
        )}
        {items && count === 0 ? (
          <div className={styles.empty}>
            <Glyph name="trash" size={56} strokeWidth={1.2} />
            <h2>{tr('files.trash.isEmpty')}</h2>
            <p>{tr('files.trash.emptyHint')}</p>
          </div>
        ) : (
          <div
            role="grid"
            aria-label={tr('files.trash')}
            aria-multiselectable="true"
            tabIndex={0}
            className={`${styles.table} ${compact ? styles.compact : ''}`}
            aria-activedescendant={sel.lead ? itemId(`trash:${sel.lead}`) : undefined}
            onKeyDown={onKey}
            onFocus={() => setFocused(true)}
            onBlur={() => setFocused(false)}
          >
            {!compact && (
              <div role="row" className={styles.header}>
                <div role="columnheader">{tr('files.col.name')}</div>
                <div role="columnheader">{tr('files.trash.from')}</div>
                <div role="columnheader">{tr('files.trash.deleted')}</div>
                <div role="columnheader" className={styles.right}>
                  {tr('files.col.size')}
                </div>
              </div>
            )}
            {(items ?? []).map((t, i) => {
              const on = sel.selected.has(key(t))
              return (
                <div
                  key={t.id}
                  id={itemId(`trash:${key(t)}`)}
                  role="row"
                  aria-selected={on}
                  aria-label={t.name}
                  data-name={t.name}
                  className={`${styles.row} ${i % 2 ? styles.odd : ''} ${on ? (focused || compact ? styles.selected : styles.selectedInactive) : ''}`}
                  onClick={(e) => (compact ? onMenu(e, t) : onClick(e, t))}
                  onContextMenu={(e) => onMenu(e, t)}
                >
                  <div role="gridcell" className={styles.name}>
                    <FileIcon entry={{ name: t.name, isDir: t.isDir, kind: t.isDir ? 'folder' : 'other' }} size={compact ? 36 : 18} />
                    <span className={styles.text}>
                      <span className={styles.title}>{t.name}</span>
                      {compact && (
                        <span className={styles.sub}>
                          {from(t)} · {formatDate(t.deletedAt)}
                        </span>
                      )}
                    </span>
                  </div>
                  {!compact && (
                    <>
                      <div role="gridcell" className={styles.dim}>
                        {from(t)}
                      </div>
                      <div role="gridcell" className={styles.dim}>
                        {formatDate(t.deletedAt)}
                      </div>
                      <div role="gridcell" className={`${styles.dim} ${styles.right}`}>
                        {t.isDir && !t.size ? '—' : formatSize(t.size)}
                      </div>
                    </>
                  )}
                </div>
              )
            })}
          </div>
        )}
      </div>
      {!compact && <div className={styles.status}>{items ? (sel.selected.size ? tr('files.selectedOf', { n: sel.selected.size, total: count }) : objects(count)) : ''}</div>}
      {menu && (
        <ContextMenu
          x={menu.x}
          y={menu.y}
          title={menu.items.length === 1 ? menu.items[0]!.name : objects(menu.items.length)}
          items={menuItems(menu.items)}
          onClose={() => setMenu(null)}
          sheet={compact}
        />
      )}
      {confirm && (
        <Sheet
          title={confirm.kind === 'empty' ? tr('files.trash.emptyTitle') : tr('files.trash.deleteTitle', { what: label })}
          message={
            confirm.kind === 'empty'
              ? tr('files.trash.emptyMessage', { objects: objects(confirm.items.length) })
              : tr('files.trash.deleteMessage')
          }
          icon={<Glyph name="warning" size={44} strokeWidth={1.5} style={{ color: 'var(--orange)' }} />}
          busy={busy}
          onCancel={() => setConfirm(null)}
          actions={[
            { label: tr('common.cancelAction'), kind: 'cancel', run: () => setConfirm(null) },
            {
              label: confirm.kind === 'empty' ? tr('files.trash.empty') : tr('common.delete'),
              kind: 'destructive',
              run: () => void run(confirm.items, (t) => deleteTrash(t.id)),
            },
          ]}
        />
      )}
    </>
  )
}
