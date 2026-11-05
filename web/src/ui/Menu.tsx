// A glass menu anchored to a trigger button — the one menu of the app
// (Select and MultiSelect are menus too). It renders in a portal with
// position: fixed (floating.ts), so it never stretches or gets clipped by
// the card it was opened from, and it always stays inside the window: on
// the side where it fits, else on the roomier side, shortened to scroll,
// with the chosen item scrolled into view. It handles the open state,
// outside clicks, Escape (focus returns to the trigger), arrow keys between
// items and moving focus into the menu. On phones it is a full-width sheet:
// next to its trigger when there is room, else standing on the tab bar.
import { useCallback, useEffect, useId, useLayoutEffect, useRef, useState, type KeyboardEvent, type MouseEvent, type ReactNode, type Ref } from 'react'
import { createPortal } from 'react-dom'
import { AnimatePresence, motion } from 'motion/react'
import { floatingBand, FLOAT_GAP, FLOAT_MARGIN, portalHost, positionFloating, revealChosen, safeInsets, type Placement } from './floating'
import styles from './Menu.module.css'

export interface TriggerProps {
  ref: Ref<HTMLButtonElement>
  'aria-haspopup': 'menu'
  'aria-expanded': boolean
  'aria-controls': string | undefined
  onClick: (e: MouseEvent) => void
}

export interface MenuProps {
  /** Accessible name of the menu. */
  label: string
  trigger: (p: TriggerProps) => ReactNode
  children: (close: (refocus?: boolean) => void) => ReactNode
  /** The side and edge it prefers; it flips when that side has no room. */
  placement?: Placement
  className?: string
  onOpenChange?: (open: boolean) => void
  /** Focus the checked (else the first) item on opening; false: the content focuses itself (a field). */
  focusItem?: boolean
  /** The tallest it gets with room (px); default FLOAT_CAP. */
  maxHeight?: number
  /** A pick list (default): it opens on its checked item. false: on its first item, scrolled to the top (an app menu with a few radio groups). */
  pickList?: boolean
}

const ITEMS = '[role="menuitem"],[role="menuitemradio"],[role="menuitemcheckbox"]'

/** Phones: the menu is a sheet across the window's bottom (Menu.module.css). */
const PHONE = '(max-width: 599px)'
const isPhone = () => typeof window.matchMedia === 'function' && window.matchMedia(PHONE).matches

// The menu element → its trigger: a Sheet opened from a menu item gives the
// focus back to the trigger, which no longer is the menu's DOM neighbour.
const triggers = new WeakMap<Element, HTMLElement>()
/** The trigger of the open menu `menu` (a [role="menu"] element). */
export const menuTrigger = (menu: Element | null): HTMLElement | null => (menu && triggers.get(menu)) ?? null

/** Phones: next to the trigger when it fits above or below, else on the tab bar, shortened. */
function placeSheet(t: HTMLElement, m: HTMLElement) {
  const { floor } = floatingBand(t)
  const r = t.getBoundingClientRect()
  const h = m.offsetHeight
  const below = floor - r.bottom - FLOAT_GAP - FLOAT_MARGIN
  const above = r.top - FLOAT_GAP - FLOAT_MARGIN
  const s = m.style
  s.top = s.bottom = s.left = s.maxHeight = ''
  // no tab bar (only the home indicator's band): CSS stands it on the window's bottom
  if (floor >= window.innerHeight - safeInsets().bottom) return
  if (h <= below) {
    s.top = `${r.bottom + FLOAT_GAP}px`
    s.bottom = 'auto'
  } else if (h <= above) s.bottom = `${window.innerHeight - r.top + FLOAT_GAP}px`
  else {
    s.bottom = `${window.innerHeight - floor + FLOAT_MARGIN}px`
    s.maxHeight = `${Math.min(window.innerHeight * 0.7, floor - 2 * FLOAT_MARGIN)}px`
  }
}

export function Menu({ label, trigger, children, placement = 'bottom-start', className, onOpenChange, focusItem = true, maxHeight, pickList = true }: MenuProps) {
  const [open, setOpenState] = useState(false)
  const id = useId()
  const triggerRef = useRef<HTMLButtonElement>(null)
  const menuRef = useRef<HTMLDivElement>(null)

  const setOpen = useCallback(
    (v: boolean) => {
      setOpenState(v)
      onOpenChange?.(v)
    },
    [onOpenChange],
  )

  // Opened with a pointer: give focus back without a keyboard focus ring.
  const byPointer = useRef(false)

  const close = useCallback(
    (refocus = true) => {
      setOpen(false)
      const t = triggerRef.current
      if (!refocus || !t) return
      if (byPointer.current) {
        t.setAttribute('data-quiet-focus', '')
        t.addEventListener('blur', () => t.removeAttribute('data-quiet-focus'), { once: true })
        t.focus({ focusVisible: false } as FocusOptions)
      } else t.focus()
    },
    [setOpen],
  )

  useEffect(() => {
    if (!open) return
    const onDown = (e: PointerEvent) => {
      const t = e.target as Node
      if (!menuRef.current?.contains(t) && !triggerRef.current?.contains(t)) close(false)
    }
    document.addEventListener('pointerdown', onDown)
    return () => document.removeEventListener('pointerdown', onDown)
  }, [open, close])

  // Placed before the first paint and kept next to the trigger while the
  // page scrolls or the window resizes.
  useLayoutEffect(() => {
    const t = triggerRef.current
    const m = menuRef.current
    if (!open || !t || !m) return
    triggers.set(m, t)
    const place = () => (isPhone() ? placeSheet(t, m) : positionFloating(t, m, placement, maxHeight))
    place()
    if (pickList) revealChosen(m, m.querySelector<HTMLElement>('[aria-checked="true"]'))
    let frame = 0
    const again = (e?: Event) => {
      // the menu's own scrolling moves nothing
      if (e?.target instanceof Node && m.contains(e.target)) return
      cancelAnimationFrame(frame)
      frame = requestAnimationFrame(place)
    }
    window.addEventListener('resize', again)
    window.addEventListener('scroll', again, true)
    // its own content changes size (a filtered list, a footer): placed again
    // at once, so a shorter menu above its trigger stays against it
    const ro = typeof ResizeObserver === 'function' ? new ResizeObserver(() => place()) : null
    ro?.observe(m)
    return () => {
      ro?.disconnect()
      cancelAnimationFrame(frame)
      window.removeEventListener('resize', again)
      window.removeEventListener('scroll', again, true)
    }
  }, [open, placement, maxHeight, pickList])

  useEffect(() => {
    if (!open || !focusItem) return
    const els = menuRef.current?.querySelectorAll<HTMLElement>(ITEMS)
    const checked = pickList ? menuRef.current?.querySelector<HTMLElement>('[aria-checked="true"]') : null
    ;(checked ?? els?.[0])?.focus({ preventScroll: true })
  }, [open, focusItem, pickList])

  const onKey = (e: KeyboardEvent<HTMLDivElement>) => {
    const els = Array.from(menuRef.current?.querySelectorAll<HTMLElement>(ITEMS) ?? []).filter(
      (el) => el.getAttribute('aria-disabled') !== 'true',
    )
    const i = els.indexOf(document.activeElement as HTMLElement)
    // a field in the menu (a studio's name) keeps its own editing keys
    const inField = e.target instanceof HTMLInputElement || e.target instanceof HTMLTextAreaElement
    const go = (n: number) => {
      e.preventDefault()
      e.stopPropagation()
      els[(n + els.length) % els.length]?.focus()
    }
    if (e.key === 'Escape') {
      e.preventDefault()
      e.stopPropagation()
      close()
    } else if (e.key === 'ArrowDown') go(i + 1)
    else if (e.key === 'ArrowUp') go(i - 1)
    else if (e.key === 'Home' && !inField) go(0)
    else if (e.key === 'End' && !inField) go(els.length - 1)
    else if (e.key === 'Tab') {
      // back on the trigger, as a macOS menu: the menu sits in a portal at the
      // end of the page, so the browser's next stop would be past the page
      e.preventDefault()
      e.stopPropagation()
      close()
    } else if (e.key === 'ArrowLeft' || e.key === 'ArrowRight' || e.key === ' ') e.stopPropagation()
  }

  return (
    <div className={styles.root}>
      {trigger({
        ref: triggerRef,
        'aria-haspopup': 'menu',
        'aria-expanded': open,
        'aria-controls': open ? id : undefined,
        onClick: (e) => {
          byPointer.current = e.detail > 0
          setOpen(!open)
        },
      })}
      {createPortal(
        <AnimatePresence>
          {open && (
            <motion.div
              ref={menuRef}
              id={id}
              role="menu"
              // opened with a pointer: the focused item is highlighted, without the keyboard ring
              data-pointer={byPointer.current || undefined}
              aria-label={label}
              className={[styles.menu, 'glass-strong', className].filter(Boolean).join(' ')}
              onKeyDown={onKey}
              initial={{ opacity: 0, scale: 0.96 }}
              animate={{ opacity: 1, scale: 1 }}
              exit={{ opacity: 0, scale: 0.97, transition: { duration: 0.12 } }}
              transition={{ duration: 0.18, ease: [0.22, 1, 0.36, 1] }}
            >
              {children(close)}
            </motion.div>
          )}
        </AnimatePresence>,
        portalHost(triggerRef.current),
      )}
    </div>
  )
}

export interface MenuItemProps {
  label: string
  detail?: string
  checked?: boolean
  disabled?: boolean
  icon?: ReactNode
  /** Renders as a link (downloads). */
  href?: string
  download?: boolean
  /** Red: the action deletes something. */
  destructive?: boolean
  /** The accessible name when the label alone is too short («Раньше» → «Субтитры раньше на 0,25 с»). */
  ariaLabel?: string
  /** A capsule after the label («Очищено»); `title` is its tooltip. Hidden from assistive tech: say it in `ariaLabel`. */
  badge?: { text: string; title?: string }
  /** With `checked`: one of several that can be on together (a checkbox item, not a radio). */
  multi?: boolean
  /** The label's language when it is not the page's (a language's own name). */
  lang?: string
  onSelect?: () => void
}

/** One row of a Menu. With `checked` defined it is a radio item (`multi`: a checkbox item) with a check mark. */
export function MenuItem({ label, detail, checked, disabled, icon, href, download, destructive, ariaLabel, badge, multi, lang, onSelect }: MenuItemProps) {
  const role = checked === undefined ? 'menuitem' : multi ? 'menuitemcheckbox' : 'menuitemradio'
  const content = (
    <>
      {checked !== undefined && (
        <span className={styles.check} aria-hidden>
          {checked ? (
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.6" strokeLinecap="round" strokeLinejoin="round">
              <path d="m5 12.5 4.5 4.5L19 7.5" />
            </svg>
          ) : null}
        </span>
      )}
      {icon && <span className={styles.icon}>{icon}</span>}
      <span className={styles.text}>
        {badge ? (
          <span className={styles.labelRow}>
            <span className={styles.label} lang={lang}>
                {label}
              </span>
            <span className={styles.badge} title={badge.title} aria-hidden="true">
              {badge.text}
            </span>
          </span>
        ) : (
          <span className={styles.label} lang={lang}>
          {label}
        </span>
        )}
        {detail && <span className={styles.detail}>{detail}</span>}
      </span>
    </>
  )
  if (href && !disabled) {
    return (
      <a role={role} tabIndex={-1} className={styles.item} href={href} download={download || undefined} onClick={onSelect}>
        {content}
      </a>
    )
  }
  return (
    <button
      type="button"
      role={role}
      tabIndex={-1}
      className={destructive ? `${styles.item} ${styles.destructive}` : styles.item}
      aria-checked={checked}
      aria-label={ariaLabel}
      aria-disabled={disabled || undefined}
      onClick={() => !disabled && onSelect?.()}
    >
      {content}
    </button>
  )
}

export function MenuHeading({ children }: { children: ReactNode }) {
  return <div className={styles.heading}>{children}</div>
}

export function MenuSeparator() {
  return <div className={styles.sep} role="separator" />
}
