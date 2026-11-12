// An ordered list the way iOS and macOS edit one: each row is the caller's
// content, then (optionally) «Выше» / «Ниже» buttons, a remove button and
// the drag handle (≡) at the trailing edge.
//   - A mouse, a pen or a finger drags a row by its handle: the row lifts and
//     follows the pointer, the others slide out of its way, the list (or the
//     sheet it is in) scrolls when the pointer nears its edge, the drop
//     commits, Escape or a lost pointer puts the row back.
//   - The keyboard moves the row with ↑/↓ (Home/End) on its handle, or with
//     the «Выше» / «Ниже» buttons; the new place is read out and the focus
//     stays on the control that moved it.
//   - On a phone (`moveButtons`) «Выше», «Ниже» and «Убрать» go into the
//     row's «⋯», so the content keeps the width.
// RankedList (languages, studios) and the channel editor's «Состав канала»
// are built on it.
import { useEffect, useId, useRef, useState, type KeyboardEvent, type PointerEvent, type ReactNode } from 'react'
import { Icon } from '../components/Icon'
import { useT } from '../i18n'
import { useIsCompact } from '../state/useMediaQuery'
import { IconButton } from './IconButton'
import { Menu, MenuItem, MenuSeparator } from './Menu'
import styles from './SortableList.module.css'

export interface SortableListProps<T> {
  items: T[]
  /** A stable, unique key of an item (kept on the row as data-value). */
  getKey: (item: T) => string
  /** How an item reads in the controls' names and the announcements. */
  itemLabel: (item: T) => string
  /** The row's content (the text, a leading glyph); the controls are the list's. */
  renderItem: (item: T, index: number) => ReactNode
  /** A reorder or a removal: the new list. */
  onChange: (items: T[]) => void
  /** The list's accessible name (or `labelledBy`). */
  label?: string
  labelledBy?: string
  /** A remove button in each row. */
  removable?: boolean
  /** These many rows always stay (their remove button is off). */
  min?: number
  /** «Выше» / «Ниже» buttons in each row (in its «⋯» on a phone). */
  moveButtons?: boolean
  /** Shown in place of an empty list. */
  empty?: ReactNode
  /** Where the focus goes when the last row is removed. */
  focusWhenEmpty?: () => HTMLElement | null
  className?: string
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
  key: string
  from: number
  pointer: number
  startY: number
  /** The scroller's scrollTop when the drag began. */
  startScroll: number
  /** How far the row went since the drag began (pointer and scrolling). */
  dy: number
  /** Where the row lands if dropped now. */
  to: number
  /** The rows' tops and heights when the drag began. */
  tops: number[]
  heights: number[]
}

/** Where a dragged row lands: before the first other row whose middle is below the dragged row's middle. */
export function landing(d: Pick<Drag, 'from' | 'tops' | 'heights'>, dy: number): number {
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

/** The nearest ancestor that scrolls vertically (a sheet's body), else the page. */
function scrollerOf(el: HTMLElement | null): HTMLElement {
  for (let p = el?.parentElement; p; p = p.parentElement) {
    const o = getComputedStyle(p).overflowY
    if ((o === 'auto' || o === 'scroll') && p.scrollHeight > p.clientHeight) return p
  }
  return (document.scrollingElement as HTMLElement | null) ?? document.documentElement
}

/** How near the edge of the scroller the pointer starts scrolling it (px), and how fast at the edge (px a frame). */
const EDGE = 48
const SPEED = 14

export function SortableList<T>({
  items,
  getKey,
  itemLabel,
  renderItem,
  onChange,
  label,
  labelledBy,
  removable,
  min = 0,
  moveButtons,
  empty,
  focusWhenEmpty,
  className,
}: SortableListProps<T>) {
  const t = useT()
  const id = useId()
  const compact = useIsCompact()
  const listRef = useRef<HTMLOListElement>(null)
  const [drag, setDragState] = useState<Drag | null>(null)
  const dragRef = useRef<Drag | null>(null)
  const setDrag = (d: Drag | null) => {
    dragRef.current = d
    setDragState(d)
  }
  const [said, setSaid] = useState('')
  // after a move or a removal: the control to focus once React has redrawn the rows
  const refocus = useRef<{ key: string | null; role: string } | null>(null)
  const keys = items.map(getKey)

  useEffect(() => {
    const f = refocus.current
    if (!f) return
    refocus.current = null
    const row = f.key === null ? null : listRef.current?.querySelector<HTMLElement>(`li[data-value="${CSS.escape(f.key)}"]`)
    const el = row?.querySelector<HTMLButtonElement>(`[data-role="${f.role}"]`)
    // a disabled target (the top row's «Выше», the last row that must stay)
    // cannot take the focus: that row's handle, else the caller's — never the page
    const handle = row?.querySelector<HTMLButtonElement>('[data-role="move"]')
    ;(el && !el.disabled ? el : (handle ?? focusWhenEmpty?.()))?.focus()
  }, [items, focusWhenEmpty])

  const name = (i: number) => itemLabel(items[i]!)

  const move = (i: number, to: number, role: string) => {
    const next = moveItem(items, i, Math.max(0, Math.min(items.length - 1, to)))
    if (next === items) return
    const k = keys[i]!
    refocus.current = { key: k, role }
    setSaid(t('comp.sortable.moved', { name: name(i), n: next.map(getKey).indexOf(k) + 1, count: next.length }))
    onChange(next)
  }

  const remove = (i: number) => {
    const next = items.filter((_, j) => j !== i)
    const nk = next.map(getKey)
    refocus.current = { key: nk[i] ?? nk[i - 1] ?? null, role: 'remove' }
    setSaid(t('comp.sortable.removed', { name: name(i) }))
    onChange(next)
  }

  const onHandleKey = (e: KeyboardEvent, i: number) => {
    const to = e.key === 'ArrowUp' ? i - 1 : e.key === 'ArrowDown' ? i + 1 : e.key === 'Home' ? 0 : e.key === 'End' ? items.length - 1 : null
    if (to === null) return
    e.preventDefault()
    move(i, to, 'move')
  }

  // ---------- the pointer ----------
  const scroller = useRef<HTMLElement | null>(null)
  const lastY = useRef(0)
  const frame = useRef(0)

  const follow = (clientY: number) => {
    const d = dragRef.current
    if (!d) return
    const s = scroller.current
    const dy = clientY - d.startY + (s ? s.scrollTop - d.startScroll : 0)
    setDrag({ ...d, dy, to: landing(d, dy) })
  }

  // near the scroller's top or bottom edge the list scrolls under the row
  const autoscroll = () => {
    frame.current = 0
    const d = dragRef.current
    const s = scroller.current
    if (!d || !s) return
    const page = s === document.scrollingElement || s === document.documentElement
    const r = page ? { top: 0, bottom: window.innerHeight } : s.getBoundingClientRect()
    const y = lastY.current
    const v = y < r.top + EDGE ? -SPEED * Math.min(1, (r.top + EDGE - y) / EDGE) : y > r.bottom - EDGE ? SPEED * Math.min(1, (y - (r.bottom - EDGE)) / EDGE) : 0
    if (!v) return
    const before = s.scrollTop
    s.scrollTop = before + v
    if (s.scrollTop !== before) follow(y)
    frame.current = requestAnimationFrame(autoscroll)
  }

  const onPointerDown = (e: PointerEvent<HTMLButtonElement>, i: number) => {
    if (e.button !== 0) return
    e.preventDefault()
    e.currentTarget.setPointerCapture?.(e.pointerId)
    const rects = [...(listRef.current?.querySelectorAll<HTMLLIElement>(':scope > li') ?? [])].map((li) => li.getBoundingClientRect())
    const s = scrollerOf(listRef.current)
    scroller.current = s
    lastY.current = e.clientY
    setDrag({
      key: keys[i]!,
      from: i,
      to: i,
      pointer: e.pointerId,
      startY: e.clientY,
      startScroll: s.scrollTop,
      dy: 0,
      tops: rects.map((r) => r.top),
      heights: rects.map((r) => r.height),
    })
  }
  const onPointerMove = (e: PointerEvent) => {
    const d = dragRef.current
    if (!d || e.pointerId !== d.pointer) return
    lastY.current = e.clientY
    follow(e.clientY)
    if (!frame.current) frame.current = requestAnimationFrame(autoscroll)
  }
  const cancel = () => {
    if (frame.current) cancelAnimationFrame(frame.current)
    frame.current = 0
    if (dragRef.current) setDrag(null)
  }
  const onPointerUp = (e: PointerEvent) => {
    const d = dragRef.current
    if (!d || e.pointerId !== d.pointer) return
    cancel()
    const next = moveItem(items, d.from, d.to)
    if (next !== items) {
      setSaid(t('comp.sortable.moved', { name: name(d.from), n: d.to + 1, count: next.length }))
      onChange(next)
    }
  }
  // Escape puts the row back
  const dragging = drag !== null
  useEffect(() => {
    if (!dragging) return
    const onKey = (e: globalThis.KeyboardEvent) => {
      if (e.key !== 'Escape') return
      e.preventDefault()
      e.stopPropagation()
      cancel()
    }
    window.addEventListener('keydown', onKey, true)
    return () => window.removeEventListener('keydown', onKey, true)
  }, [dragging])
  useEffect(() => () => cancelAnimationFrame(frame.current), [])

  const inMenu = moveButtons && compact

  return (
    <div className={[styles.box, className].filter(Boolean).join(' ')}>
      {items.length > 0 ? (
        <ol className={styles.list} aria-label={label} aria-labelledby={labelledBy} ref={listRef} data-dragging={dragging || undefined}>
          {items.map((item, i) => {
            const k = keys[i]!
            const n = itemLabel(item)
            const dy = drag ? slide(drag, i) : 0
            const canRemove = items.length > min
            return (
              <li
                key={k}
                className={styles.row}
                data-value={k}
                data-dragging={drag?.key === k || undefined}
                data-sliding={(drag && drag.key !== k) || undefined}
                style={dy ? { transform: `translateY(${dy}px)` } : undefined}
              >
                <div className={styles.content}>{renderItem(item, i)}</div>
                <span id={`${id}-${i}`} className="visually-hidden">
                  {t('comp.sortable.position', { n: i + 1, count: items.length })}
                </span>
                {moveButtons && !inMenu && (
                  <>
                    <IconButton
                      variant="plain"
                      size="xs"
                      icon="chevronUp"
                      data-role="up"
                      label={t('comp.sortable.up', { name: n })}
                      tooltip={t('comp.sortable.upShort')}
                      disabled={i === 0}
                      onClick={() => move(i, i - 1, 'up')}
                    />
                    <IconButton
                      variant="plain"
                      size="xs"
                      icon="chevronDown"
                      data-role="down"
                      label={t('comp.sortable.down', { name: n })}
                      tooltip={t('comp.sortable.downShort')}
                      disabled={i === items.length - 1}
                      onClick={() => move(i, i + 1, 'down')}
                    />
                  </>
                )}
                {removable && !inMenu && (
                  <IconButton
                    variant="plain"
                    size="xs"
                    icon="minusCircle"
                    className={styles.remove}
                    data-role="remove"
                    label={t('comp.sortable.remove', { name: n })}
                    disabled={!canRemove}
                    onClick={() => remove(i)}
                  />
                )}
                {inMenu && (
                  <Menu
                    label={n}
                    placement="bottom-end"
                    trigger={(p) => <IconButton {...p} variant="plain" size="xs" icon="ellipsis" data-role="remove" label={t('comp.sortable.actions', { name: n })} />}
                  >
                    {(close) => (
                      <>
                        <MenuItem icon={<Icon name="arrowUp" size={16} />} label={t('comp.sortable.upShort')} disabled={i === 0} onSelect={() => (close(false), move(i, i - 1, 'remove'))} />
                        <MenuItem
                          icon={<Icon name="arrowDown" size={16} />}
                          label={t('comp.sortable.downShort')}
                          disabled={i === items.length - 1}
                          onSelect={() => (close(false), move(i, i + 1, 'remove'))}
                        />
                        {removable && (
                          <>
                            <MenuSeparator />
                            <MenuItem destructive icon={<Icon name="trash" size={16} />} label={t('comp.sortable.removeShort')} disabled={!canRemove} onSelect={() => (close(false), remove(i))} />
                          </>
                        )}
                      </>
                    )}
                  </Menu>
                )}
                <IconButton
                  variant="plain"
                  size="xs"
                  icon="grip"
                  className={styles.handle}
                  data-role="move"
                  label={t('comp.sortable.move', { name: n })}
                  aria-describedby={`${id}-${i}`}
                  tooltip={t('comp.sortable.moveHint')}
                  onKeyDown={(e) => onHandleKey(e, i)}
                  onPointerDown={(e) => onPointerDown(e, i)}
                  onPointerMove={onPointerMove}
                  onPointerUp={onPointerUp}
                  onPointerCancel={cancel}
                  // the capture lost some other way (the node went away, the
                  // browser took the gesture): no drop, the row goes back
                  onLostPointerCapture={cancel}
                />
              </li>
            )
          })}
        </ol>
      ) : (
        empty && <div className={styles.empty}>{empty}</div>
      )}
      <span role="status" className="visually-hidden">
        {said}
      </span>
    </div>
  )
}

/**
 * The usual content of a row: a glyph on a small tile (what kind of thing it
 * is), the name and a detail line under it — both cut with an ellipsis.
 */
export function SortableRowText({ leading, title, detail }: { leading?: ReactNode; title: ReactNode; detail?: ReactNode }) {
  return (
    <span className={styles.text}>
      {leading && (
        <span className={styles.glyph} aria-hidden>
          {leading}
        </span>
      )}
      <span className={styles.lines}>
        <span className={styles.title}>{title}</span>
        {detail && <span className={styles.detail}>{detail}</span>}
      </span>
    </span>
  )
}
