// Right-click menu styled like macOS (compact glass, shortcut hints on the
// right). On a phone the same items come up as an iOS action sheet.
import { useEffect, useLayoutEffect, useRef, useState, type KeyboardEvent, type RefObject } from 'react'
import { createPortal } from 'react-dom'
import { motion } from 'motion/react'
import { Glyph, type GlyphName } from './glyphs'
import styles from './ContextMenu.module.css'
import { useT } from '../../i18n'

export interface MenuAction {
  id: string
  label: string
  icon?: GlyphName
  shortcut?: string
  disabled?: boolean
  destructive?: boolean
  run: () => void
}
export type MenuEntry = MenuAction | 'separator'

export interface ContextMenuProps {
  x: number
  y: number
  /** Accessible name and, on a phone, the sheet title. */
  title: string
  items: MenuEntry[]
  onClose: (refocus?: boolean) => void
  sheet?: boolean
}

function useItemKeys(root: RefObject<HTMLElement | null>, onClose: (refocus?: boolean) => void) {
  useEffect(() => {
    root.current?.querySelector<HTMLElement>('[role="menuitem"]:not([aria-disabled="true"])')?.focus()
  }, [root])
  return (e: KeyboardEvent) => {
    const els = Array.from(root.current?.querySelectorAll<HTMLElement>('[role="menuitem"]:not([aria-disabled="true"])') ?? [])
    const i = els.indexOf(document.activeElement as HTMLElement)
    const go = (n: number) => {
      e.preventDefault()
      els[(n + els.length) % els.length]?.focus()
    }
    e.stopPropagation()
    if (e.key === 'Escape') {
      e.preventDefault()
      onClose(true)
    } else if (e.key === 'ArrowDown') go(i + 1)
    else if (e.key === 'ArrowUp') go(i - 1)
    else if (e.key === 'Home') go(0)
    else if (e.key === 'End') go(els.length - 1)
    else if (e.key === 'Tab') {
      e.preventDefault()
      onClose(true)
    }
  }
}

export function ContextMenu({ x, y, title, items, onClose, sheet }: ContextMenuProps) {
  const t = useT()
  const ref = useRef<HTMLDivElement>(null)
  const [pos, setPos] = useState({ left: x, top: y })
  const onKey = useItemKeys(ref, onClose)

  // Keep the menu on screen (flip left/up near the edges, like macOS).
  useLayoutEffect(() => {
    if (sheet || !ref.current) return
    const r = ref.current.getBoundingClientRect()
    const vw = window.innerWidth
    const vh = window.innerHeight
    setPos({
      left: x + r.width > vw - 8 ? Math.max(8, x - r.width) : x,
      top: y + r.height > vh - 8 ? Math.max(8, vh - r.height - 8) : y,
    })
  }, [x, y, sheet])

  useEffect(() => {
    const down = (e: PointerEvent) => {
      if (!ref.current?.contains(e.target as Node)) onClose(false)
    }
    const blur = () => onClose(false)
    document.addEventListener('pointerdown', down, true)
    window.addEventListener('blur', blur)
    window.addEventListener('resize', blur)
    return () => {
      document.removeEventListener('pointerdown', down, true)
      window.removeEventListener('blur', blur)
      window.removeEventListener('resize', blur)
    }
  }, [onClose])

  const run = (a: MenuAction) => {
    if (a.disabled) return
    onClose(false)
    a.run()
  }

  const rows = items.map((it, i) =>
    it === 'separator' ? (
      <div key={`sep${i}`} role="separator" className={styles.sep} />
    ) : (
      <button
        key={it.id}
        type="button"
        role="menuitem"
        tabIndex={-1}
        aria-disabled={it.disabled || undefined}
        className={`${styles.item} ${it.destructive ? styles.destructive : ''}`}
        onClick={() => run(it)}
      >
        {sheet && it.icon && <Glyph name={it.icon} size={20} className={styles.icon} />}
        <span className={styles.label}>{it.label}</span>
        {!sheet && it.shortcut && <span className={styles.shortcut}>{it.shortcut}</span>}
        {!sheet && it.icon && <Glyph name={it.icon} size={15} className={styles.trailing} />}
      </button>
    ),
  )

  if (sheet) {
    return createPortal(
      <div className={styles.sheetBackdrop} onClick={(e) => e.target === e.currentTarget && onClose(false)}>
        <motion.div
          ref={ref}
          className={styles.sheet}
          initial={{ y: 40, opacity: 0 }}
          animate={{ y: 0, opacity: 1 }}
          transition={{ type: 'spring', stiffness: 520, damping: 42 }}
          onKeyDown={onKey}
        >
          <div role="menu" aria-label={title} className={`${styles.group} glass-strong`}>
            <div className={styles.sheetTitle}>{title}</div>
            {rows}
          </div>
          <button type="button" className={`${styles.cancel} glass-strong`} onClick={() => onClose(true)}>
            {t('common.cancelAction')}
          </button>
        </motion.div>
      </div>,
      document.body,
    )
  }

  return createPortal(
    <motion.div
      ref={ref}
      role="menu"
      aria-label={title}
      className={`${styles.menu} glass-strong`}
      style={{ left: pos.left, top: pos.top }}
      onKeyDown={onKey}
      onContextMenu={(e) => e.preventDefault()}
      initial={{ opacity: 0, scale: 0.98 }}
      animate={{ opacity: 1, scale: 1 }}
      transition={{ duration: 0.12 }}
    >
      {rows}
    </motion.div>,
    document.body,
  )
}
