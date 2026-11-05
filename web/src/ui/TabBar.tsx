// The phone's navigation (the iOS 26 tab bar): a floating matte-glass
// capsule of tabs at the bottom of the screen, above the home indicator,
// and one round accessory beside it (search). It is the bottom bar of the
// layout: `data-bottom-bar` keeps menus off it (floating.ts) and the
// --tabbar-* tokens tell pages, toasts and the player bar how much room it
// takes. The router marks the current tab (aria-current="page").
import { NavLink } from 'react-router'
import { Icon, type IconName } from '../components/Icon'
import styles from './TabBar.module.css'

export interface TabBarItem {
  to: string
  label: string
  icon: IconName
  /** Current only on the exact path (the home tab). */
  end?: boolean
}

export interface TabBarProps {
  /** The navigation's accessible name («Разделы»). */
  label: string
  items: TabBarItem[]
  /** The round button beside the capsule (search); its label is the accessible name. */
  accessory?: TabBarItem
  className?: string
}

const cls = (...c: (string | false | undefined)[]) => c.filter(Boolean).join(' ')

export function TabBar({ label, items, accessory, className }: TabBarProps) {
  return (
    <nav aria-label={label} className={cls(styles.bar, className)} data-testid="tabbar" data-bottom-bar>
      <ul className={`${styles.tabs} glass`}>
        {items.map((s) => (
          <li key={s.to}>
            <NavLink to={s.to} end={s.end} className={({ isActive }) => cls(styles.tab, isActive && styles.active)}>
              <Icon name={s.icon} size={24} strokeWidth={1.8} />
              <span className={styles.label}>{s.label}</span>
            </NavLink>
          </li>
        ))}
      </ul>
      {accessory && (
        <NavLink
          to={accessory.to}
          end={accessory.end}
          aria-label={accessory.label}
          className={({ isActive }) => cls(styles.accessory, 'glass', isActive && styles.active)}
        >
          <Icon name={accessory.icon} size={22} strokeWidth={2.1} />
        </NavLink>
      )}
    </nav>
  )
}
