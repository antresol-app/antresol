// An ordered list, the way iOS and macOS Settings edit one: each row has its
// name, a remove button and a drag handle (≡). The handle drags with a mouse
// or a finger; on the keyboard ↑/↓ (Home/End) on the handle move the row and
// the new place is read out. «+ Добавить…» under the list opens a menu of
// what can still be added — and, with `field`, a text field for any name
// (studios). The first row is the main one; the caller's hint says so.
// «Мои языки», «Озвучка», «Любимые озвучки», «Звук и субтитры» use it.
import { useEffect, useId, useRef, useState, type KeyboardEvent, type PointerEvent } from 'react'
import { useT } from '../i18n'
import { Button } from '../ui/Button'
import { IconButton } from '../ui/IconButton'
import { MenuItem, MenuSeparator, Menu } from '../ui/Menu'
import { TextField } from '../ui/TextField'
import styles from './RankedList.module.css'

export interface RankedChoice {
  value: string
  label: string
}

export interface RankedListProps {
  /** The list's accessible name (or `labelledBy`). */
  label?: string
  labelledBy?: string
  items: string[]
  /** How an item reads (a language code → «Русский»); the item itself by default. */
  itemLabel?: (v: string) => string
  /** These many rows always stay (their remove button is off). */
  min?: number
  /** No «Добавить» once the list has this many. */
  max?: number
  /** A reorder or a removal. */
  onChange: (items: string[]) => void
  /** What the add menu offers (not in the list yet), for what is typed in `field`. */
  choices: (query: string) => RankedChoice[]
  /** A choice of the menu, or the field's text, was added. */
  onAdd: (value: string) => void
  /** «Добавить язык»: the button and the menu's name. */
  addLabel: string
  /** A text field at the top of the add menu: any name, added on Enter. */
  field?: { label: string; maxLength?: number }
  /** Shown in place of an empty list. */
  empty?: string
}

/** `list` with the item at `from` moved to `to`. */
export function moveItem<T>(list: T[], from: number, to: number): T[] {
  if (from === to || from < 0 || from >= list.length || to < 0 || to >= list.length) return list
  const out = [...list]
  const [x] = out.splice(from, 1)
  out.splice(to, 0, x!)
  return out
}

interface Drag {
  value: string
  from: number
  pointer: number
  startY: number
  /** How far the pointer went since the drag began. */
  dy: number
  /** Where the row lands if dropped now. */
  to: number
  /** The rows' tops and heights when the drag began. */
  tops: number[]
  heights: number[]
}

/** Where a dragged row lands: before the first other row whose middle is below the dragged row's middle. */
function landing(d: Pick<Drag, 'from' | 'tops' | 'heights'>, dy: number): number {
  const middle = d.tops[d.from]! + d.heights[d.from]! / 2 + dy
  return d.tops.filter((top, i) => i !== d.from && top + d.heights[i]! / 2 < middle).length
}

/**
 * How far row `i` slides while row `d.from` is dragged to `d.to`: the dragged
 * one follows the pointer, the ones it passes make room by one row.
 */
function slide(d: Drag, i: number): number {
  if (i === d.from) return d.dy
  const n = d.tops.length
  const step = d.from + 1 < n ? d.tops[d.from + 1]! - d.tops[d.from]! : d.from > 0 ? d.tops[d.from]! - d.tops[d.from - 1]! : d.heights[d.from]!
  if (i > d.from && i <= d.to) return -step
  if (i < d.from && i >= d.to) return step
  return 0
}

export function RankedList({ label, labelledBy, items, itemLabel = (v) => v, min = 0, max, onChange, choices, onAdd, addLabel, field, empty }: RankedListProps) {
  const t = useT()
  const id = useId()
  const listRef = useRef<HTMLOListElement>(null)
  const addRef = useRef<HTMLButtonElement>(null)
  const [drag, setDrag] = useState<Drag | null>(null)
  const [said, setSaid] = useState('')
  const [query, setQuery] = useState('')
  // after a move or a removal: the control to focus once React has redrawn the rows
  const refocus = useRef<{ value: string | null; what: 'move' | 'remove' } | null>(null)

  useEffect(() => {
    const f = refocus.current
    if (!f) return
    refocus.current = null
    const row = f.value === null ? null : listRef.current?.querySelector<HTMLElement>(`li[data-value="${CSS.escape(f.value)}"]`)
    const el = row?.querySelector<HTMLButtonElement>(`[data-role="${f.what}"]`)
    // a disabled target (the last row that must stay) cannot take the focus:
    // that row's handle, else «Добавить» — never the page
    const handle = row?.querySelector<HTMLButtonElement>('[data-role="move"]')
    ;(el && !el.disabled ? el : (handle ?? addRef.current))?.focus()
  }, [items])

  // the DOM keeps its order during a drag: rows slide with transforms (moving
  // the node under the pointer would drop the handle's pointer capture)
  const shown = items
  const name = (v: string) => itemLabel(v)

  const move = (i: number, to: number) => {
    const next = moveItem(items, i, Math.max(0, Math.min(items.length - 1, to)))
    if (next === items) return
    const v = items[i]!
    refocus.current = { value: v, what: 'move' }
    setSaid(t('comp.ranked.moved', { name: name(v), n: next.indexOf(v) + 1, count: next.length }))
    onChange(next)
  }

  const remove = (i: number) => {
    const next = items.filter((_, j) => j !== i)
    refocus.current = { value: next[i] ?? next[i - 1] ?? null, what: 'remove' }
    onChange(next)
  }

  const onHandleKey = (e: KeyboardEvent, i: number) => {
    const to = e.key === 'ArrowUp' ? i - 1 : e.key === 'ArrowDown' ? i + 1 : e.key === 'Home' ? 0 : e.key === 'End' ? items.length - 1 : null
    if (to === null) return
    e.preventDefault()
    move(i, to)
  }

  const onPointerDown = (e: PointerEvent<HTMLButtonElement>, value: string) => {
    if (e.button !== 0) return
    e.preventDefault()
    e.currentTarget.setPointerCapture?.(e.pointerId)
    const rects = [...(listRef.current?.querySelectorAll<HTMLLIElement>(':scope > li') ?? [])].map((li) => li.getBoundingClientRect())
    const from = items.indexOf(value)
    setDrag({ value, from, to: from, pointer: e.pointerId, startY: e.clientY, dy: 0, tops: rects.map((r) => r.top), heights: rects.map((r) => r.height) })
  }
  const onPointerMove = (e: PointerEvent) => {
    if (!drag || e.pointerId !== drag.pointer) return
    const dy = e.clientY - drag.startY
    setDrag({ ...drag, dy, to: landing(drag, dy) })
  }
  const onPointerUp = (e: PointerEvent) => {
    if (!drag || e.pointerId !== drag.pointer) return
    setDrag(null)
    const next = moveItem(items, drag.from, drag.to)
    if (next !== items) {
      setSaid(t('comp.ranked.moved', { name: name(drag.value), n: drag.to + 1, count: next.length }))
      onChange(next)
    }
  }
  // Escape puts the row back
  const dragging = drag !== null
  useEffect(() => {
    if (!dragging) return
    const onKey = (e: globalThis.KeyboardEvent) => e.key === 'Escape' && setDrag(null)
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [dragging])

  const offered = choices(query)
  const full = max !== undefined && items.length >= max
  const canAdd = !full && (field !== undefined || choices('').length > 0)

  return (
    <div className={styles.box}>
      {shown.length > 0 ? (
        <ol className={styles.list} aria-label={label} aria-labelledby={labelledBy} ref={listRef}>
          {shown.map((v, i) => {
            const n = name(v)
            return (
              <li
                key={v}
                className={styles.row}
                data-value={v}
                data-dragging={drag?.value === v || undefined}
                data-sliding={(drag && drag.value !== v) || undefined}
                style={drag ? { transform: slide(drag, i) ? `translateY(${slide(drag, i)}px)` : undefined } : undefined}
              >
                <span className={styles.name}>{n}</span>
                <span id={`${id}-${i}`} className="visually-hidden">
                  {t('comp.ranked.position', { n: i + 1, count: shown.length })}
                </span>
                <IconButton
                  variant="plain"
                  size="xs"
                  icon="minusCircle"
                  className={styles.remove}
                  data-role="remove"
                  label={t('comp.ranked.remove', { name: n })}
                  disabled={items.length <= min}
                  onClick={() => remove(i)}
                />
                <IconButton
                  variant="plain"
                  size="xs"
                  icon="grip"
                  className={styles.handle}
                  data-role="move"
                  label={t('comp.ranked.move', { name: n })}
                  aria-describedby={`${id}-${i}`}
                  tooltip={t('comp.ranked.moveHint')}
                  onKeyDown={(e) => onHandleKey(e, i)}
                  onPointerDown={(e) => onPointerDown(e, v)}
                  onPointerMove={onPointerMove}
                  onPointerUp={onPointerUp}
                  onPointerCancel={() => setDrag(null)}
                  // the capture lost some other way (the node went away, the
                  // browser took the gesture): no drop, the row goes back
                  onLostPointerCapture={() => setDrag(null)}
                />
              </li>
            )
          })}
        </ol>
      ) : (
        empty && <p className={styles.empty}>{empty}</p>
      )}
      <span role="status" className="visually-hidden">
        {said}
      </span>
      {canAdd && (
        <Menu
          label={addLabel}
          placement="bottom-start"
          focusItem={!field}
          onOpenChange={(open) => !open && setQuery('')}
          trigger={(p) => (
            <Button
              {...p}
              ref={(el) => {
                addRef.current = el
                if (typeof p.ref === 'function') p.ref(el)
                else if (p.ref) (p.ref as { current: HTMLButtonElement | null }).current = el
              }}
              variant="plain"
              size="small"
              icon="plusCircle"
            >
              {addLabel}
            </Button>
          )}
        >
          {(close) => (
            <>
              {field && (
                <div className={styles.field}>
                  <TextField
                    label={field.label}
                    size="small"
                    hideLabel
                    placeholder={field.label}
                    value={query}
                    maxLength={field.maxLength}
                    autoComplete="off"
                    autoFocus
                    onChange={(e) => setQuery(e.target.value)}
                    onKeyDown={(e) => {
                      if (e.key !== 'Enter' || e.nativeEvent.isComposing) return
                      e.preventDefault()
                      if (!query.trim()) return
                      onAdd(query)
                      close()
                    }}
                  />
                </div>
              )}
              {field && offered.length > 0 && <MenuSeparator />}
              {offered.map((c) => (
                <MenuItem
                  key={c.value}
                  label={c.label}
                  onSelect={() => {
                    onAdd(c.value)
                    close()
                  }}
                />
              ))}
            </>
          )}
        </Menu>
      )}
    </div>
  )
}
