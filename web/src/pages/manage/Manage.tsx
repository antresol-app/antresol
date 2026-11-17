// «Управление» (admin): a sidebar like macOS System Settings —
// (Просьбы,) Переезд, Импорт, Файлы, Статистика, Профили, Расширения, Настройки
// and the tiles the addons add — with the section on the right. On phones the list is its own screen and each section has a
// back button.
import { NavLink, Navigate, Outlet, useLocation } from 'react-router'
import { Icon, type IconName } from '../../components/Icon'
import { useIsCompact } from '../../state/useMediaQuery'
import { useT, type Key } from '../../i18n'
import { useSession } from '../../state/session'
import { usePendingRequests } from '../../state/kidRequests'
import { AddonBoundary, resolveAddonPath, useAddonLabel, useAddonSlot } from '../../addons/slots'
import type { Contribution } from '../../addons/registry'
import styles from './Manage.module.css'

// ifRequests:
// shown when kids' requests are open on this server (user.requests).
// ifAddons: shown when the server runs addons (user.addons).
// tint: the icon tile's colour, a palette token (DESIGN.md: palette).
export const MANAGE_SECTIONS: { to: string; label: Key; icon: IconName; tint: string; ifRequests?: boolean; ifAddons?: boolean }[] = [
  { to: 'requests', label: 'manage.section.requests', icon: 'send', tint: 'var(--orange)', ifRequests: true },
  { to: 'migrate', label: 'manage.section.migrate', icon: 'arrowRight', tint: 'var(--indigo)' },
  { to: 'import', label: 'manage.section.import', icon: 'music', tint: 'var(--pink)' },
  { to: 'files', label: 'common.files', icon: 'folder', tint: 'var(--teal)' },
  { to: 'stats', label: 'manage.section.stats', icon: 'chart', tint: 'var(--orange)' },
  { to: 'profiles', label: 'manage.section.profiles', icon: 'people', tint: 'var(--purple)' },
  { to: 'addons', label: 'addons.title', icon: 'puzzle', tint: 'var(--cyan)', ifAddons: true },
  { to: 'settings', label: 'settings.serverTitle', icon: 'gear', tint: 'var(--gray)' },
]

/** An addon's own tile tint. */
const ADDON_TINT = 'var(--blue)'

/** The sections this server has. */
function useSections() {
  const { user } = useSession()
  return MANAGE_SECTIONS.filter((s) => !(s.ifRequests && !user?.requests) && !(s.ifAddons && !user?.addons))
}

/** An addon's tile: its label is a key of the addon's strings. */
function AddonTile({ tile }: { tile: Contribution<'manageTiles'> }) {
  const label = useAddonLabel(tile.addonId)
  return (
    <li>
      <NavLink to={resolveAddonPath(tile.addonId, tile.to)} className={({ isActive }) => [styles.navItem, isActive ? styles.navActive : ''].join(' ')}>
        <span className={styles.navIcon} style={{ background: ADDON_TINT }}>
          <Icon name={tile.icon} size={16} strokeWidth={2.2} />
        </span>
        <span className={styles.navLabel}>{label(tile.label)}</span>
        <Icon name="chevronRight" size={14} strokeWidth={2.4} className={styles.navChevron} />
      </NavLink>
    </li>
  )
}

function SectionList() {
  const t = useT()
  const sections = useSections()
  const { user } = useSession()
  // «Просьбы»: the count of requests waiting for an answer
  const pending = usePendingRequests(user?.id ?? 0, Boolean(user?.requests))
  const addonTiles = useAddonSlot('manageTiles')
  return (
    <nav aria-label={t('common.manage')} className={styles.nav}>
      <ul className={styles.navList}>
        {sections.map((s) => (
          <li key={s.to}>
            <NavLink to={`/manage/${s.to}`} className={({ isActive }) => [styles.navItem, isActive ? styles.navActive : ''].join(' ')}>
              <span className={styles.navIcon} style={{ background: s.tint }}>
                <Icon name={s.icon} size={16} strokeWidth={2.2} />
              </span>
              <span className={styles.navLabel}>{t(s.label)}</span>
              {s.to === 'requests' && pending > 0 && (
                <>
                  <span className={styles.navBadge} aria-hidden>
                    {pending > 99 ? '99+' : pending}
                  </span>
                  <span className="visually-hidden">{t('requests.badge', { count: pending })}</span>
                </>
              )}
              <Icon name="chevronRight" size={14} strokeWidth={2.4} className={styles.navChevron} />
            </NavLink>
          </li>
        ))}
        {addonTiles.map((tile, i) => (
          <AddonBoundary key={`${tile.addonId}:${i}`} addonId={tile.addonId} as="li">
            <AddonTile tile={tile} />
          </AddonBoundary>
        ))}
      </ul>
    </nav>
  )
}

export function Manage() {
  const t = useT()
  const compact = useIsCompact()
  const sections = useSections()
  const { pathname } = useLocation()
  const atRoot = /^\/manage\/?$/.test(pathname)
  // Deeper screens (e.g. «Добавить источник») bring their own back link.
  const nested = /^\/manage\/[^/]+\/./.test(pathname)

  if (compact) {
    return (
      <div className={styles.page}>
        {atRoot ? (
          <>
            <h1 className={styles.largeTitle}>{t('common.manage')}</h1>
            <SectionList />
          </>
        ) : (
          <>
            {!nested && (
              <NavLink to="/manage" className={styles.back}>
                <Icon name="chevronLeft" size={18} strokeWidth={2.4} />
                {t('common.manage')}
              </NavLink>
            )}
            <Outlet />
          </>
        )}
      </div>
    )
  }
  // «Переезд» and «Импорт» are one-off tools, not where «Управление» opens
  if (atRoot) return <Navigate to={`/manage/${sections.find((s) => s.to !== 'migrate' && s.to !== 'import')?.to ?? 'files'}`} replace />
  return (
    <div className={styles.page}>
      <div className={styles.layout}>
        <aside className={`${styles.sidebar} glass`}>
          <h1 className={styles.sidebarTitle}>{t('common.manage')}</h1>
          <SectionList />
        </aside>
        <div className={styles.content}>
          <Outlet />
        </div>
      </div>
    </div>
  )
}
