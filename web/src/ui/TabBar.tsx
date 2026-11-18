// The phone's navigation (the iOS 26 tab bar): a floating glass rounded
// rectangle of at most five tabs (HIG) at the bottom of the screen, above the
// home indicator, and a square accessory beside it (search). The selected
// tab sits under a lighter «lens» that slides to the next one on a spring,
// stretching on its way like a drop of liquid. The bar and the search are
// Liquid Glass (design/glass.css .glass-liquid; ui/liquidGlass.ts bends the
// backdrop at their rims where the engine can).
// Scrolling down folds the bar into a square of the current tab's glyph (the
// player bar, if something plays, moves into the room it frees: PlayerBar's
// CSS reads `data-minimized`); scrolling up, or a tap on the square, opens it
// again. It is the bottom bar of the layout: `data-bottom-bar` keeps menus off
// it (floating.ts) and the --tabbar-* tokens tell pages, toasts and the
// player bar how much room it takes.
import { useEffect, useLayoutEffect, useRef, useState, type CSSProperties, type RefObject } from 'react'
import { Link, useLocation } from 'react-router'
import { Icon, type IconName } from '../components/Icon'
import { useLiquidGlass } from './liquidGlass'
import styles from './TabBar.module.css'

export interface TabBarItem {
  to: string
  label: string
  /** A solid glyph (as SF Symbols' .fill), the same at rest and selected. */
  icon: IconName
  /** The paths under which this tab is current ("/music" also covers "/music/album/3"); by default `to`. "/" matches only itself. */
  roots?: string[]
}

export interface TabBarProps {
  /** The navigation's accessible name («Разделы»). */
  label: string
  items: TabBarItem[]
  /** The square button beside the tabs (search); its label is the accessible name. */
  accessory?: TabBarItem
  /** The folded bar's button that opens it again («Показать вкладки»). */
  expandLabel: string
  className?: string
}

const cls = (...c: (string | false | undefined)[]) => c.filter(Boolean).join(' ')

function isUnder(pathname: string, root: string) {
  return root === '/' ? pathname === '/' : pathname === root || pathname.startsWith(root + '/')
}

const current = (pathname: string, it: TabBarItem) => (it.roots ?? [it.to.split('?')[0]!]).some((r) => isUnder(pathname, r))

// How far the page moves before the bar folds or opens: a finger's jitter
// and a momentum scroll's last pixels change nothing.
const FOLD_AFTER = 80
const STEP = 6

/** Folded while the page scrolls down, open while it scrolls up, at its top or on another page. */
function useFolded(pathname: string): [boolean, (v: boolean) => void] {
  const [folded, setFolded] = useState(false)
  useEffect(() => {
    setFolded(false)
    let last = window.scrollY
    const on = () => {
      const y = window.scrollY
      if (y < FOLD_AFTER) setFolded(false)
      else if (y - last > STEP) setFolded(true)
      else if (last - y > STEP) setFolded(false)
      else return
      last = y
    }
    window.addEventListener('scroll', on, { passive: true })
    return () => window.removeEventListener('scroll', on)
  }, [pathname])
  return [folded, setFolded]
}

/**
 * The lens's droplet (iOS 26): on its way to another tab it stretches along
 * the way and squashes across it, more the further it goes, and settles as
 * it arrives. The slide itself is CSS (left on the spring); this only adds the
 * shape. Reduce motion: the spring's duration is 0 and nothing stretches.
 */
function useDroplet(lens: RefObject<HTMLElement | null>, sel: number) {
  const prev = useRef(sel)
  useLayoutEffect(() => {
    const from = prev.current
    prev.current = sel
    const el = lens.current
    if (!el || from < 0 || sel < 0 || from === sel || typeof el.animate !== 'function') return
    const cs = getComputedStyle(el)
    // a duration token comes back as written or as the browser serialises it (".5s")
    const d = cs.getPropertyValue('--d-tabbar-spring').trim()
    const ms = (parseFloat(d) || 0) * (d.endsWith('ms') ? 1 : 1000)
    if (!ms || window.matchMedia?.('(prefers-reduced-motion: reduce)').matches) return
    const k = Math.min(parseFloat(cs.getPropertyValue('--liquid-stretch')) * Math.abs(sel - from), parseFloat(cs.getPropertyValue('--liquid-stretch-max'))) || 0
    el.animate(
      [
        { transform: 'scale(1, 1)' },
        { transform: `scale(${1 + k}, ${1 - k / 2})`, offset: 0.3 },
        { transform: `scale(${1 - k / 6}, ${1 + k / 8})`, offset: 0.7 },
        { transform: 'scale(1, 1)' },
      ],
      { duration: ms, easing: 'ease-out' },
    )
  }, [lens, sel])
}

export function TabBar({ label, items, accessory, expandLabel, className }: TabBarProps) {
  const { pathname } = useLocation()
  const [folded, setFolded] = useFolded(pathname)
  const sel = items.findIndex((it) => current(pathname, it))
  // A page that is no tab's own (a title, Настройки) keeps the last tab's glyph on the folded square.
  const lastSel = useRef(0)
  if (sel >= 0) lastSel.current = sel
  const shown = items[sel >= 0 ? sel : lastSel.current]
  const tabsRef = useRef<HTMLUListElement>(null)
  const capsuleRef = useRef<HTMLDivElement>(null)
  const searchRef = useRef<HTMLAnchorElement>(null)
  const lensRef = useRef<HTMLSpanElement>(null)
  useLiquidGlass(capsuleRef)
  useLiquidGlass(searchRef)
  useDroplet(lensRef, sel)

  const expand = () => {
    setFolded(false)
    // the keyboard goes on where the square was: the current tab
    requestAnimationFrame(() => tabsRef.current?.querySelector<HTMLElement>('a[aria-current="page"], a')?.focus())
  }

  return (
    <nav
      aria-label={label}
      className={cls(styles.bar, className)}
      data-testid="tabbar"
      data-bottom-bar
      data-minimized={folded || undefined}
    >
      <div ref={capsuleRef} className={`${styles.capsule} glass-float glass-liquid`} style={{ '--n': items.length } as CSSProperties}>
        {sel >= 0 && <span ref={lensRef} className={`${styles.lens} liquid-lens`} style={{ '--i': sel } as CSSProperties} aria-hidden />}
        <ul ref={tabsRef} className={styles.tabs} inert={folded}>
          {items.map((it, i) => (
            <li key={it.label}>
              <Link to={it.to} className={styles.tab} aria-current={i === sel ? 'page' : undefined}>
                <Icon name={it.icon} size={24} className={styles.glyph} />
                <span className={styles.label}>{it.label}</span>
              </Link>
            </li>
          ))}
        </ul>
        <button type="button" className={styles.unfold} onClick={expand} aria-label={expandLabel} tabIndex={folded ? 0 : -1} aria-hidden={!folded || undefined}>
          {shown && <Icon name={shown.icon} size={24} className={cls(styles.glyph, sel >= 0 && styles.selected)} />}
        </button>
      </div>
      {accessory && (
        <Link
          ref={searchRef}
          to={accessory.to}
          aria-label={accessory.label}
          aria-current={current(pathname, accessory) ? 'page' : undefined}
          className={cls(styles.accessory, 'glass-float glass-liquid')}
        >
          {/* 26: the system's search tab draws its magnifier about 22 pt across */}
          <Icon name={accessory.icon} size={26} />
        </Link>
      )}
    </nav>
  )
}
