// System-style sheets. `alert` (default): a macOS alert — icon, bold title,
// short text, buttons side by side. `wide`: the same with room for a form.
// `large`: a panel with a left-aligned title, scrollable content and the
// buttons at the bottom (release table, add-source form). A dialog with no
// Cancel button of its own (a picker, a read-only panel) gets the round ✕ in
// its top-right corner instead of a «Закрыть» button. Esc cancels (a
// read-only picker with dismissOnOutside also on a click outside it; a form
// never, so a stray click can't throw away what was typed), Enter confirms.
//
// On a phone every sheet is a bottom sheet: it slides up from the bottom
// edge across the width, clear of the home indicator, with its buttons
// stacked full width at the thumb; the on-screen keyboard lifts it
// (keyboard.ts), so a field and its buttons are never covered.
import { useEffect, useRef, useState, type KeyboardEvent, type PointerEvent as ReactPointerEvent, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { MaterialContext } from './Button'
import { useT } from '../i18n'
import { useIsCompact, useReducedMotion } from '../state/useMediaQuery'
import { IconButton } from './IconButton'
import { useKeyboardInset } from './keyboard'
import { menuTrigger, recentMenuTrigger } from './Menu'
import { useScrollLock } from './scrollLock'
import styles from './Sheet.module.css'

/** How long a bottom sheet takes to slide away. */
const CLOSE_MS = 240
/** A drag this far down (pt), or a flick this fast (pt per ms), closes a picker. */
const DISMISS_DRAG = 110
const DISMISS_FLICK = 0.6

export interface SheetAction {
  label: string
  kind?: 'primary' | 'destructive' | 'cancel'
  disabled?: boolean
  run: () => void
}

export interface SheetProps {
  title: string
  message?: ReactNode
  icon?: ReactNode
  /** The footer buttons; none (or only a ✕) for a picker or a read-only panel. */
  actions?: SheetAction[]
  onCancel: () => void
  children?: ReactNode
  /** alertdialog for confirmations, dialog for forms. */
  role?: 'alertdialog' | 'dialog'
  busy?: boolean
  /** Same as size="wide". */
  wide?: boolean
  size?: 'alert' | 'wide' | 'large'
  /** Hides the button row (the content has its own controls). */
  noButtons?: boolean
  /** Where to mount it (default document.body); the video player passes its own root so the sheet shows in full screen. */
  container?: Element | null
  /** A click on the dimmed area closes it: only for read-only pickers (release table, contents, devices), never for forms or confirmations. */
  dismissOnOutside?: boolean
  /** The round ✕ in the corner: by default on a dialog whose footer has no Cancel button. */
  closeButton?: boolean
}

export function Sheet({ title, message, icon, actions = [], onCancel, children, role = 'alertdialog', busy, wide, size, noButtons, container, dismissOnOutside, closeButton }: SheetProps) {
  const t = useT()
  const kind = size ?? (wide ? 'wide' : 'alert')
  const ref = useRef<HTMLDivElement>(null)
  const titleId = useRef(`sheet-${Math.random().toString(36).slice(2)}`).current
  const latest = useRef({ actions, onCancel, busy })
  latest.current = { actions, onCancel, busy }
  const downOutside = useRef(false)
  const close = closeButton ?? (role === 'dialog' && !actions.some((a) => a.kind === 'cancel'))
  const buttons = !noButtons && actions.length > 0
  const bottom = useIsCompact()
  const still = useReducedMotion()
  useKeyboardInset()
  useScrollLock()
  // a bottom sheet slides up from below the edge, a centred one settles in
  // place (Sheet.module.css: the enter animations, the slide away, the drag)
  // A bottom sheet leaves the way it came: it slides down, then the page's
  // onCancel unmounts it. A read-only picker can also be dragged down by
  // its grabber (a form never: what was typed is not thrown away).
  const [closing, setClosing] = useState(false)
  const [dragY, setDragY] = useState(0)
  const [dragging, setDragging] = useState(false)
  const drag = useRef<{ y: number; t: number; vy: number; dy: number } | null>(null)
  const closeTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  useEffect(() => () => clearTimeout(closeTimer.current), [])
  const animated = bottom && !still
  const grabber = bottom && !!dismissOnOutside
  /** Closes it: after the slide down on a phone, at once elsewhere. */
  const dismiss = (then: () => void = () => latest.current.onCancel()) => {
    if (!animated) return then()
    if (closing) return
    setClosing(true)
    closeTimer.current = setTimeout(then, CLOSE_MS)
  }
  const onGrabDown = (e: ReactPointerEvent<HTMLElement>) => {
    if (latest.current.busy) return
    drag.current = { y: e.clientY, t: e.timeStamp, vy: 0, dy: 0 }
    setDragging(true)
    e.currentTarget.setPointerCapture?.(e.pointerId)
  }
  const onGrabMove = (e: ReactPointerEvent<HTMLElement>) => {
    const d = drag.current
    if (!d) return
    const dy = Math.max(0, e.clientY - d.y)
    const dt = e.timeStamp - d.t
    if (dt > 0) d.vy = (dy - d.dy) / dt
    d.dy = dy
    d.t = e.timeStamp
    setDragY(dy)
  }
  const onGrabEnd = () => {
    const d = drag.current
    drag.current = null
    setDragging(false)
    if (!d) return
    if (d.dy > DISMISS_DRAG || d.vy > DISMISS_FLICK) dismiss()
    else setDragY(0)
  }

  // Everything beside the sheet is inert while it is open (Tab, clicks and
  // screen readers stay inside even when focus was lost to <body>).
  // Declared first: its cleanup runs before focus goes back.
  useEffect(() => {
    const backdrop = ref.current?.parentElement
    const parent = backdrop?.parentElement
    if (!backdrop || !parent) return
    const made = Array.from(parent.children).filter((el) => el !== backdrop && !el.hasAttribute('inert'))
    for (const el of made) el.setAttribute('inert', '')
    return () => {
      for (const el of made) el.removeAttribute('inert')
    }
  }, [])

  useEffect(() => {
    const restore = document.activeElement as HTMLElement | null
    // opened from an item of a menu (that menu is closing; its keys would
    // swallow the page's Esc): focus goes back to the menu's trigger instead
    const menu = restore?.closest('[role="menu"]')
    const lost = !restore || restore === document.body
    const back = menu ? (menuTrigger(menu) ?? menu.parentElement?.querySelector<HTMLElement>('[aria-haspopup="menu"]')) : lost ? (recentMenuTrigger() ?? restore) : restore
    const el = ref.current
    const first =
      el?.querySelector<HTMLElement>('[data-autofocus]') ??
      el?.querySelector<HTMLElement>('input') ??
      el?.querySelector<HTMLElement>('[data-primary="true"]') ??
      el?.querySelector('button')
    first?.focus()
    // only when focus would be lost: a control that went away meanwhile, or a
    // page that already moved focus elsewhere, is left alone
    return () => {
      const now = document.activeElement
      const lost = !now || now === document.body || !!el?.contains(now)
      if (lost && back?.isConnected) back.focus?.()
    }
  }, [])

  // Esc while focus is somewhere else (e.g. <body> after a click outside) still closes the sheet.
  useEffect(() => {
    const onWindowKey = (e: globalThis.KeyboardEvent) => {
      if (e.key !== 'Escape' || e.defaultPrevented || ref.current?.contains(document.activeElement)) return
      e.preventDefault()
      dismissRef.current()
    }
    window.addEventListener('keydown', onWindowKey)
    return () => window.removeEventListener('keydown', onWindowKey)
  }, [])

  const dismissRef = useRef(dismiss)
  dismissRef.current = dismiss

  const onKey = (e: KeyboardEvent) => {
    e.stopPropagation()
    const l = latest.current
    if (e.key === 'Escape') {
      e.preventDefault()
      dismiss()
    } else if (e.key === 'Enter' && !(e.target instanceof HTMLButtonElement) && !(e.target instanceof HTMLTextAreaElement)) {
      const p = l.actions.find((a) => a.kind === 'primary' || a.kind === 'destructive')
      if (p && !p.disabled && !l.busy) {
        e.preventDefault()
        p.run()
      }
    } else if (e.key === 'Tab') {
      const els = Array.from(
        ref.current?.querySelectorAll<HTMLElement>('input, textarea, select, a[href], button:not(:disabled), [tabindex="0"]') ?? [],
      ).filter((x) => x.offsetParent !== null || x === document.activeElement)
      if (!els.length) return
      const i = els.indexOf(document.activeElement as HTMLElement)
      if (e.shiftKey && i <= 0) {
        e.preventDefault()
        els[els.length - 1]!.focus()
      } else if (!e.shiftKey && i === els.length - 1) {
        e.preventDefault()
        els[0]!.focus()
      }
    }
  }

  return createPortal(
    // a sheet is glass: what it holds is never glass again
    <MaterialContext.Provider value="fill">
    <div
      className={styles.backdrop}
      data-closing={closing || undefined}
      // a click on the dimmed area must not take focus out of the sheet
      // (Esc and Tab are handled on the sheet; in the player, Esc on <body> would close the player)
      onMouseDown={(e) => {
        downOutside.current = e.target === e.currentTarget
        if (downOutside.current) e.preventDefault()
      }}
      // a click on the dimmed area closes a read-only picker (a press that
      // started in the sheet, e.g. selecting text, does not); a form or a
      // confirmation needs an answer
      onClick={(e) => {
        // a sheet mounted in the player: the click must not reach the player under it
        e.stopPropagation()
        const outside = downOutside.current && e.target === e.currentTarget
        downOutside.current = false
        if (outside && dismissOnOutside && !latest.current.busy) dismiss()
      }}
    >
      <div
        ref={ref}
        role={role}
        aria-modal="true"
        aria-labelledby={titleId}
        className={`${styles.sheet} ${styles[kind]} ${close ? styles.closable : ''} glass-strong`}
        data-placement={bottom ? 'bottom' : 'center'}
        data-closing={closing || undefined}
        data-dragging={dragging || undefined}
        style={dragY && !closing ? { transform: `translateY(${dragY}px)` } : undefined}
        onKeyDown={onKey}
      >
        {grabber && (
          <div
            className={styles.grabber}
            data-testid="sheet-grabber"
            aria-hidden
            onPointerDown={onGrabDown}
            onPointerMove={onGrabMove}
            onPointerUp={onGrabEnd}
            onPointerCancel={onGrabEnd}
          />
        )}
        {icon && <div className={styles.icon}>{icon}</div>}
        <h2 id={titleId} className={styles.title}>
          {title}
        </h2>
        {kind === 'large' ? (
          <>
            {message && <div className={styles.message}>{message}</div>}
            <div className={styles.body} data-sheet-body>
              {children}
            </div>
          </>
        ) : (
          // what scrolls on a phone: the title and the buttons stay pinned
          (message || children) && (
            <div className={styles.content} data-sheet-body>
              {message && <div className={styles.message}>{message}</div>}
              {children}
            </div>
          )
        )}
        {buttons && <div className={styles.buttons}>
          {actions.map((a) => (
            <button
              key={a.label}
              type="button"
              data-primary={a.kind === 'primary' || a.kind === 'destructive' ? 'true' : undefined}
              className={[styles.button, a.kind && styles[a.kind]].filter(Boolean).join(' ')}
              disabled={a.disabled || (busy && a.kind !== 'cancel')}
              // Cancel slides the sheet away first, as the ✕ does
              onClick={a.kind === 'cancel' ? () => dismiss(a.run) : a.run}
            >
              {busy && (a.kind === 'primary' || a.kind === 'destructive') ? (
                <span className={styles.spinner} aria-label={t('comp.sheet.wait')} />
              ) : (
                <span className={styles.label}>{a.label}</span>
              )}
            </button>
          ))}
        </div>}
        {/* last in the DOM: focus on open still goes to the content first */}
        {close && (
          <IconButton size="xs" icon="close" className={styles.close} label={t('common.close')} onClick={() => dismiss()} />
        )}
      </div>
    </div>
    </MaterialContext.Provider>,
    container ?? document.body,
  )
}

