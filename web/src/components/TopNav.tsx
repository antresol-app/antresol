// Global navigation, variant B: a centred matte-glass pill (tvOS style) with the
// profile and downloads on the right. Below 700px the pill is replaced by the
// bottom tab bar (ui/TabBar) and the header keeps only the brand and the profile.
import { APP_NAME } from '../brand'
import { useEffect, useState } from 'react'
import { Link, NavLink, useLocation } from 'react-router'
import { motion } from 'motion/react'
import type { User } from '../api/types'
import { SECTIONS, searchHref } from '../app/sections'
import { useIsCompact } from '../state/useMediaQuery'
import { DownloadsBadge } from './DownloadsBadge'
import { useT } from '../i18n'
import { Icon } from './Icon'
import { LogoMark } from './Logo'
import { ProfileMenu } from './ProfileMenu'
import { TabBar } from '../ui/TabBar'
import styles from './TopNav.module.css'

export interface TopNavProps {
  user: User
  /** Active downloads; shown to admins only. */
  downloads?: number
  onSwitchProfile?: () => void
}

function useScrolled(threshold = 8) {
  const [scrolled, setScrolled] = useState(false)
  useEffect(() => {
    const on = () => setScrolled(window.scrollY > threshold)
    on()
    window.addEventListener('scroll', on, { passive: true })
    return () => window.removeEventListener('scroll', on)
  }, [threshold])
  return scrolled
}

export function TopNav({ user, downloads = 0, onSwitchProfile }: TopNavProps) {
  const t = useT()
  const compact = useIsCompact()
  const scrolled = useScrolled()
  const { pathname } = useLocation()
  const search = SECTIONS.find((s) => s.iconOnly)

  return (
    <>
      {/* the layout's top bar: menus open below it, never over it (ui/floating.ts) */}
      <header className={[styles.header, compact ? styles.compact : '', scrolled ? styles.scrolled : ''].join(' ')} data-top-bar>
        <Link to="/" className={styles.brand} aria-label={t('comp.topNav.home', { app: APP_NAME })}>
          <LogoMark size={compact ? 28 : 30} />
          <span className={styles.wordmark}>{APP_NAME}</span>
        </Link>

        {!compact && (
          <nav aria-label={t('comp.sections')} className={`${styles.pill} glass`} data-testid="topnav-pill">
            <ul className={styles.items}>
              {SECTIONS.map((s) => (
                <li key={s.to}>
                  <NavLink
                    to={s.iconOnly ? searchHref(pathname) : s.to}
                    end={s.to === '/'}
                    aria-label={s.iconOnly ? s.label : undefined}
                    className={({ isActive }) => [styles.item, isActive ? styles.active : '', s.iconOnly ? styles.iconOnly : ''].join(' ')}
                  >
                    {({ isActive }) => (
                      <>
                        {isActive && (
                          <motion.span
                            layoutId="topnav-active"
                            className={styles.capsule}
                            transition={{ type: 'spring', stiffness: 520, damping: 42, mass: 0.9 }}
                          />
                        )}
                        <span className={styles.itemLabel}>
                          {s.iconOnly ? <Icon name={s.icon} size={18} strokeWidth={2.2} /> : s.label}
                        </span>
                      </>
                    )}
                  </NavLink>
                </li>
              ))}
            </ul>
          </nav>
        )}

        <div className={styles.trailing}>
          {!__LIBRARY_EDITION__ && user.isAdmin && <DownloadsBadge count={downloads} />}
          <ProfileMenu user={user} onSwitchProfile={onSwitchProfile} />
        </div>
      </header>
      {compact && (
        <TabBar
          label={t('comp.sections')}
          items={SECTIONS.filter((s) => !s.iconOnly).map((s) => ({ to: s.to, label: s.label, icon: s.icon, end: s.to === '/' }))}
          accessory={search && { to: searchHref(pathname), label: search.label, icon: search.icon }}
        />
      )}
    </>
  )
}
