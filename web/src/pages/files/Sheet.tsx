// File browser sheets: the shared Sheet (components/Sheet), a name prompt
// (rename, new folder) and the «Переместить…» folder picker.
import { useEffect, useRef, useState } from 'react'
import { NOT_MOVE_TARGETS } from '../../api/files'
import type { FileRoot } from '../../api/types'
import { joinPath, sortEntries } from './model'
import { FileIcon } from './FileIcon'
import { Glyph, rootGlyph } from './glyphs'
import { useDir } from './useDir'
import styles from './Sheet.module.css'
import { t, useT } from '../../i18n'

export { Sheet, type SheetAction, type SheetProps } from '../../ui/Sheet'

/** Name field: selects the name without its extension, like Finder. */
export function NameField({ value, onChange, label, error }: { value: string; onChange: (v: string) => void; label: string; error?: string }) {
  const ref = useRef<HTMLInputElement>(null)
  useEffect(() => {
    const el = ref.current
    if (!el) return
    const dot = el.value.lastIndexOf('.')
    el.setSelectionRange(0, dot > 0 ? dot : el.value.length)
  }, [])
  return (
    <div className={styles.field}>
      <input
        ref={ref}
        className={styles.input}
        aria-label={label}
        aria-invalid={error ? true : undefined}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        spellCheck={false}
        autoComplete="off"
      />
      {error && (
        <p className={styles.error} role="alert">
          {error}
        </p>
      )}
    </div>
  )
}

/** Client-side name check (the server checks again). */
export function nameProblem(name: string): string | undefined {
  if (!name.trim()) return t('files.name.empty')
  if (name !== name.trim()) return t('files.name.spaces')
  if (/[/\\]/.test(name)) return t('files.name.slash')
  if (name.startsWith('.')) return t('files.name.dot')
  if (new TextEncoder().encode(name).length > 255) return t('files.name.long')
  return undefined
}

export interface MoveTarget {
  root: string
  path: string
}

/** «Переместить…»: pick a folder. The stream cache is never a destination. */
export function MovePicker({
  roots,
  start,
  disabledPaths,
  value,
  onChange,
}: {
  roots: FileRoot[]
  start: MoveTarget
  /** Folders being moved (cannot move into themselves). */
  disabledPaths: Set<string>
  value: MoveTarget
  onChange: (t: MoveTarget) => void
}) {
  const tr = useT()
  const [at, setAt] = useState<MoveTarget | null>(start)
  const { data, error } = useDir(at?.root ?? null, at?.path ?? '')
  const targets = roots.filter((r) => !NOT_MOVE_TARGETS.has(r.id) && r.id !== 'library')
  const folders = data ? sortEntries(data.entries.filter((e) => e.isDir), 'name', 'asc') : []
  const rootTitle = (id: string) => roots.find((r) => r.id === id)?.title ?? id
  const crumbs = at ? [rootTitle(at.root), ...(at.path ? at.path.split('/') : [])] : []
  const go = (target: MoveTarget | null) => {
    setAt(target)
    if (target) onChange(target)
  }

  return (
    <div className={styles.picker}>
      <div className={styles.pickerBar}>
        <button
          type="button"
          className={styles.pickerBack}
          aria-label={tr('common.back')}
          disabled={!at}
          onClick={() => go(at && at.path ? { root: at.root, path: at.path.includes('/') ? at.path.slice(0, at.path.lastIndexOf('/')) : '' } : null)}
        >
          <Glyph name="chevronLeft" size={14} strokeWidth={2.4} />
        </button>
        <span className={styles.pickerWhere}>{at ? crumbs.join(' › ') : tr('files.places')}</span>
      </div>
      <div className={styles.pickerList} role="listbox" aria-label={tr('files.move.destination')}>
        {!at &&
          targets.map((r) => (
            <button key={r.id} type="button" role="option" aria-selected={false} className={styles.pickerRow} onClick={() => go({ root: r.id, path: '' })}>
              <Glyph name={rootGlyph(r.icon)} size={17} className={styles.pickerGlyph} />
              <span>{r.title}</span>
              <Glyph name="chevronRight" size={12} strokeWidth={2.4} className={styles.pickerChevron} />
            </button>
          ))}
        {at && error && <p className={styles.pickerEmpty}>{error}</p>}
        {at && data && folders.length === 0 && <p className={styles.pickerEmpty}>{tr('files.move.noSubfolders')}</p>}
        {at &&
          folders.map((f) => {
            const off = at.root === start.root && disabledPaths.has(f.path)
            return (
              <button
                key={f.path}
                type="button"
                role="option"
                aria-selected={false}
                aria-disabled={off || undefined}
                className={styles.pickerRow}
                onClick={() => !off && go({ root: at.root, path: joinPath(at.path, f.name) })}
              >
                <FileIcon entry={f} size={20} />
                <span>{f.name}</span>
                <Glyph name="chevronRight" size={12} strokeWidth={2.4} className={styles.pickerChevron} />
              </button>
            )
          })}
      </div>
      <p className={styles.pickerTarget}>
        {value && at ? (
          <>
            {tr('files.move.here')} <strong>{crumbs[crumbs.length - 1]}</strong>
          </>
        ) : (
          tr('files.move.pick')
        )}
      </p>
    </div>
  )
}
