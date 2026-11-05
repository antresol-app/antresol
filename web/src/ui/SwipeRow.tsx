// A list row whose actions a finger reaches with a swipe to the left — the
// Mail and Music rows of iOS. The row slides and the actions grow out of its
// trailing edge (the destructive one last, at the edge); half the way and it
// stays open, less and it springs back. A tap on the open row, or a touch
// anywhere else, closes it; an action runs and closes it.
//
// A shortcut, never the only way: the same actions must be in the row's «⋯»
// menu (a mouse never swipes, and closed actions are hidden from assistive
// tech and the keyboard). Vertical drags stay the page's scrolling.
import { useEffect, useRef, useState, type ReactNode } from 'react'
import { Icon, type IconName } from '../components/Icon'
import styles from './SwipeRow.module.css'

export interface SwipeAction {
  key: string
  label: string
  icon: IconName
  /** destructive: red (delete), accent: blue, neutral: grey (default). */
  tone?: 'destructive' | 'accent' | 'neutral'
  disabled?: boolean
  onSelect: () => void
}

export interface SwipeRowProps {
  actions: SwipeAction[]
  children: ReactNode
  /** On the <li> (separators between rows). */
  className?: string
  /** On the sliding content (the row's own layout). */
  contentClassName?: string
  'data-testid'?: string
  'data-state'?: string
}

/** One action's width (the --swipe-action-w token). */
const ACTION_W = 76
/** A drag this long before it is a swipe or a scroll. */
const SLOP = 8
/** A flick this fast (pt per ms) opens or closes the row whatever the distance. */
const FLICK = 0.3

export function SwipeRow({ actions, children, className, contentClassName, ...data }: SwipeRowProps) {
  const [open, setOpen] = useState(false)
  const [drag, setDrag] = useState<number | null>(null)
  const rowRef = useRef<HTMLLIElement>(null)
  // the gesture lives in a ref: a quick flick can end before React has
  // rendered its last move, so the release reads the live offset, not state
  const g = useRef<{ x: number; y: number; base: number; axis: 'x' | 'y' | null; moved: boolean; offset: number; lastX: number; lastT: number; vx: number } | null>(null)
  const swallowClick = useRef(false)
  const full = actions.length * ACTION_W
  const offset = drag ?? (open ? -full : 0)

  // a touch anywhere else closes the open row
  useEffect(() => {
    if (!open) return
    const onDown = (e: PointerEvent) => {
      if (!rowRef.current?.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('pointerdown', onDown)
    return () => document.removeEventListener('pointerdown', onDown)
  }, [open])

  const onPointerDown = (e: React.PointerEvent<HTMLDivElement>) => {
    swallowClick.current = false
    if (e.pointerType === 'mouse' || actions.length === 0) return
    const base = open ? -full : 0
    g.current = { x: e.clientX, y: e.clientY, base, axis: null, moved: false, offset: base, lastX: e.clientX, lastT: e.timeStamp, vx: 0 }
  }
  const onPointerMove = (e: React.PointerEvent<HTMLDivElement>) => {
    const s = g.current
    if (!s) return
    const dx = e.clientX - s.x
    const dy = e.clientY - s.y
    if (!s.axis) {
      if (Math.abs(dx) < SLOP && Math.abs(dy) < SLOP) return
      s.axis = Math.abs(dx) > Math.abs(dy) ? 'x' : 'y'
      if (s.axis === 'x') e.currentTarget.setPointerCapture?.(e.pointerId)
      // the list scrolls: an open row closes, as in Mail
      else if (open) setOpen(false)
    }
    if (s.axis !== 'x') return
    s.moved = true
    const now = e.timeStamp
    if (now > s.lastT) s.vx = (e.clientX - s.lastX) / (now - s.lastT)
    s.lastX = e.clientX
    s.lastT = now
    // past the actions' width the row resists (a rubber band)
    let next = Math.min(0, s.base + dx)
    if (next < -full) next = -full - (-full - next) * 0.25
    s.offset = next
    setDrag(next)
  }
  const onPointerEnd = () => {
    const s = g.current
    g.current = null
    if (!s?.moved) return
    swallowClick.current = true
    // half the way, or a flick faster than FLICK either way
    const opened = s.vx < -FLICK ? true : s.vx > FLICK ? false : s.offset < -full / 2
    setOpen(opened)
    setDrag(null)
  }

  return (
    <li ref={rowRef} className={[styles.row, className].filter(Boolean).join(' ')} data-open={open} data-swiping={drag !== null || undefined} {...data}>
      {(open || drag !== null) && (
        <div className={styles.actions} style={{ width: -offset }}>
          {actions.map((a) => (
            <button
              key={a.key}
              type="button"
              className={styles.action}
              data-tone={a.tone ?? 'neutral'}
              disabled={a.disabled}
              tabIndex={open ? 0 : -1}
              aria-hidden={open ? undefined : true}
              onClick={() => {
                setOpen(false)
                a.onSelect()
              }}
            >
              <Icon name={a.icon} size={20} />
              <span className={styles.actionLabel}>{a.label}</span>
            </button>
          ))}
        </div>
      )}
      <div
        className={[styles.content, contentClassName].filter(Boolean).join(' ')}
        style={offset ? { transform: `translateX(${offset}px)` } : undefined}
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMove}
        onPointerUp={onPointerEnd}
        onPointerCancel={onPointerEnd}
        onClickCapture={(e) => {
          // a click that ends a swipe, or a tap on the open row: no action
          if (swallowClick.current || open) {
            e.preventDefault()
            e.stopPropagation()
            if (!swallowClick.current) setOpen(false)
          }
        }}
      >
        {children}
      </div>
    </li>
  )
}
