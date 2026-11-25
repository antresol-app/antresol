// «Управление» (admin; the profile menu's «Настройки» opens it): a sidebar
// like macOS System Settings in two titled parts, as the owner asked for a
// clear split — «Мои настройки» (the admin's own profile: pages/
// ProfileSettings.tsx, drawn in this frame) and «Сервер», what applies to the
// whole server in groups as iOS Settings has them: Семья (Профили, Запросы),
// Медиатека (its settings, Файлы, Импорт, Переезд), Воспроизведение (the
// playback settings, the channels), Загрузки и источники (the addons' tiles
// that name that group), Расширения (an addon's tile with no group), Система
// (Состояние, Задачи, Обработка, Статистика, Расширения, ИИ, Kindle, Аккаунты и синхронизация) — with the section on the
// right (the channels' pages, pages/tv, are drawn in it too). The screens of the server's settings (/manage/settings/<group>) are
// rows of their groups, a phone's row with its current choice («1080p»). On
// phones the list is its own screen and each section has a back button.
// Every row's tile is ui/SettingsIcon: one glyph set, one palette colour
// each; an addon's tile names its glyph, colour and group (`manageTiles`).
import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import { NavLink, Navigate, Outlet, useLocation } from 'react-router'
import { Avatar } from '../../components/Avatar'
import { Icon } from '../../components/Icon'
import { useIsCompact } from '../../state/useMediaQuery'
import { useT, type Key } from '../../i18n'
import { useSession } from '../../state/session'
import { usePendingRequests } from '../../state/kidRequests'
import { AddonBoundary, resolveAddonPath, useAddonLabel, useAddonSlot } from '../../addons/slots'
import type { Contribution, ManageGroup } from '../../addons/registry'
import { SettingsIcon, settingsGlyph, settingsTint, type SettingsGlyph, type SettingsTint } from '../../ui/SettingsIcon'
import { getSettings } from '../../api/client'
import type { Settings as ServerSettings } from '../../api/types'
import { settingsSummary } from './settingsSummary'
import styles from './Manage.module.css'

/** A group of the list: the server's, and `addons` for an addon's tile that names none. */
type Group = ManageGroup | 'addons'

/** The groups, in their order on the list. */
const GROUPS: { id: Group; title: Key }[] = [
  { id: 'family', title: 'manage.group.family' },
  { id: 'library', title: 'manage.group.library' },
  { id: 'playback', title: 'manage.group.playback' },
  { id: 'downloads', title: 'manage.group.downloads' },
  { id: 'addons', title: 'addons.title' },
  { id: 'server', title: 'manage.group.server' },
]
const ADDON_GROUPS: readonly string[] = ['family', 'library', 'playback', 'downloads', 'server']

// to: under /manage/ (every section is drawn in this frame: the settings
// are apart from the app's own pages), or an app path starting with `/`.
// ifRequests: shown when kids' requests are open on this server (user.requests).
// ifAddons: shown when the server runs addons (user.addons).
// Медиатека is what is in the library and where it lies; Воспроизведение
// how it is watched (quality, languages, voice-overs, the channels);
// Система the server itself: «Состояние» (is everything working, versions
// and updates), «Задачи» (what runs in the background, in one place) and
// «Обработка» (what the server makes of the files: transcoding, preview
// frames, cleaned voice-overs).
export const MANAGE_SECTIONS: { to: string; label: Key; glyph: SettingsGlyph; tint: SettingsTint; group: ManageGroup; ifRequests?: boolean; ifAddons?: boolean }[] = [
  { to: 'profiles', label: 'manage.section.profiles', glyph: 'people', tint: 'blue', group: 'family' },
  { to: 'requests', label: 'manage.section.requests', glyph: 'bubble', tint: 'orange', group: 'family', ifRequests: true },
  { to: 'settings/library', label: 'settings.group.library', glyph: 'stack', tint: 'indigo', group: 'library' },
  { to: 'files', label: 'common.files', glyph: 'folder', tint: 'teal', group: 'library' },
  { to: 'import', label: 'manage.section.import', glyph: 'trayDown', tint: 'green', group: 'library' },
  { to: 'migrate', label: 'manage.section.migrate', glyph: 'box', tint: 'orange', group: 'library' },
  { to: 'settings/watching', label: 'settings.watching', glyph: 'playRect', tint: 'red', group: 'playback' },
  { to: 'settings/music', label: 'common.music', glyph: 'note', tint: 'pink', group: 'playback' },
  { to: 'settings/subtitles', label: 'common.subtitles', glyph: 'captions', tint: 'teal', group: 'playback' },
  { to: 'channels', label: 'manage.section.channels', glyph: 'tv', tint: 'blue', group: 'playback' },
  { to: 'health', label: 'manage.section.health', glyph: 'pulse', tint: 'green', group: 'server' },
  { to: 'tasks', label: 'manage.section.tasks', glyph: 'updates', tint: 'orange', group: 'server' },
  { to: 'settings/processing', label: 'settings.group.processing', glyph: 'cpu', tint: 'gray', group: 'server' },
  { to: 'stats', label: 'manage.section.stats', glyph: 'chart', tint: 'indigo', group: 'server' },
  { to: 'addons', label: 'addons.title', glyph: 'puzzle', tint: 'green', group: 'server', ifAddons: true },
  { to: 'settings/ai', label: 'settings.group.ai', glyph: 'sparkles', tint: 'purple', group: 'server' },
  { to: 'settings/kindle', label: 'settings.mailGroup', glyph: 'envelope', tint: 'blue', group: 'server' },
  { to: 'settings/accounts', label: 'acct.section', glyph: 'link', tint: 'cyan', group: 'server' },
  { to: 'settings/address', label: 'settings.group.address', glyph: 'globe', tint: 'blue', group: 'server' },
  { to: 'settings/external', label: 'ext.title', glyph: 'antenna', tint: 'indigo', group: 'server' },
]

/** Where a section's row leads. */
const sectionHref = (to: string) => (to.startsWith('/') ? to : `/manage/${to}`)

/** The sections this server has. */
function useSections() {
  const { user } = useSession()
  return MANAGE_SECTIONS.filter((s) => !(s.ifRequests && !user?.requests) && !(s.ifAddons && !user?.addons))
}

const navClass = ({ isActive }: { isActive: boolean }) => [styles.navItem, isActive ? styles.navActive : ''].join(' ')

/** An addon's tile: its label is a key of the addon's strings. */
function AddonTile({ tile }: { tile: Contribution<'manageTiles'> }) {
  const label = useAddonLabel(tile.addonId)
  return (
    <NavLink to={resolveAddonPath(tile.addonId, tile.to)} className={navClass}>
      <SettingsIcon glyph={settingsGlyph(tile.icon)} tint={settingsTint(tile.tint)} size="sm" className={styles.navIcon} />
      <span className={styles.navLabel}>{label(tile.label)}</span>
      <Icon name="chevronRight" size={14} strokeWidth={2.4} className={styles.navChevron} />
    </NavLink>
  )
}

/** The admin's own profile, first on the list: its «Настройки». */
function ProfileRow() {
  const t = useT()
  const { user } = useSession()
  const compact = useIsCompact()
  if (!user) return null
  return (
    <ul className={`${styles.navList} ${styles.navProfile}`}>
      <li>
        <NavLink to="/settings" className={({ isActive }) => `${navClass({ isActive })} ${styles.profileItem}`}>
          <Avatar name={user.name} avatar={user.avatar} photo={user.photo} size={compact ? 52 : 26} className={styles.profileAvatar} />
          <span className={styles.navLabel}>
            <span className={styles.profileName}>{user.name}</span>
            {compact && <span className={styles.profileHint}>{t('manage.profileHint')}</span>}
          </span>
          <Icon name="chevronRight" size={14} strokeWidth={2.4} className={styles.navChevron} />
        </NavLink>
      </li>
    </ul>
  )
}

/**
 * The server's settings for the rows' current choices («Просмотр 1080p»), on
 * a phone's list only (as iOS; the wide sidebar is navigation). Read each
 * time the list shows, so a change made on a screen is there on return.
 */
function useSummaries(): ServerSettings | null {
  const compact = useIsCompact()
  const [values, setValues] = useState<ServerSettings | null>(null)
  useEffect(() => {
    if (!compact) return
    const ac = new AbortController()
    getSettings(ac.signal).then(setValues, () => {})
    return () => ac.abort()
  }, [compact])
  return compact ? values : null
}

function SectionList() {
  const t = useT()
  const sections = useSections()
  const { user } = useSession()
  // «Запросы»: the count of requests waiting for an answer
  const pending = usePendingRequests(user?.id ?? 0, Boolean(user?.requests))
  const addonTiles = useAddonSlot('manageTiles')
  const summaries = useSummaries()
  const groupOf = (tile: Contribution<'manageTiles'>): Group => (tile.group && ADDON_GROUPS.includes(tile.group) ? tile.group : 'addons')
  return (
    <nav aria-label={t('common.settings')} className={styles.nav}>
      {/* two parts, each under its own title: what is this profile's, and
          what applies to the whole server (the owner: a clear split) */}
      <section className={styles.navPart} aria-labelledby="manage-part-mine">
        <h2 id="manage-part-mine" className={styles.navPartTitle}>
          {t('manage.part.mine')}
        </h2>
        <ProfileRow />
      </section>
      <section className={styles.navPart} aria-labelledby="manage-part-server">
        <h2 id="manage-part-server" className={styles.navPartTitle}>
          {t('manage.part.server')}
        </h2>
      {GROUPS.map((g) => {
        const core = sections.filter((s) => s.group === g.id)
        const tiles = addonTiles.filter((tile) => groupOf(tile) === g.id)
        if (core.length + tiles.length === 0) return null
        const id = `manage-group-${g.id}`
        return (
          <section key={g.id} className={styles.navGroup} aria-labelledby={id}>
            <h3 id={id} className={styles.navGroupTitle}>
              {t(g.title)}
            </h3>
            <ul className={styles.navList}>
              {core.map((s) => (
                <li key={s.to}>
                  <NavLink to={sectionHref(s.to)} className={navClass}>
                    <SettingsIcon glyph={s.glyph} tint={s.tint} size="sm" className={styles.navIcon} />
                    <span className={styles.navLabel}>{t(s.label)}</span>
                    {summaries && s.to.startsWith('settings/') && <span className={styles.navValue}>{settingsSummary(s.to.slice('settings/'.length), summaries)}</span>}
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
              {tiles.map((tile) => (
                <AddonBoundary key={`${tile.addonId}:${tile.to}`} addonId={tile.addonId} as="li">
                  <AddonTile tile={tile} />
                </AddonBoundary>
              ))}
            </ul>
          </section>
        )
      })}
      </section>
    </nav>
  )
}

export function Manage() {
  const t = useT()
  const compact = useIsCompact()
  const sections = useSections()
  const { pathname } = useLocation()
  const atRoot = /^\/manage\/?$/.test(pathname)
  // Deeper screens (e.g. «Добавить источник») bring their own back link; a
  // screen of the server's settings (settings/library) is a section itself.
  const here = pathname.replace(/\/+$/, '')
  const nested = /^\/manage\/[^/]+\/./.test(here) && !sections.some((s) => sectionHref(s.to) === here)

  if (compact && atRoot) {
    return (
      <div className={styles.page}>
        <h1 className={styles.largeTitle}>{t('common.settings')}</h1>
        <SectionList />
      </div>
    )
  }
  // a wide window opens on the first row: the profile's own settings
  if (atRoot) return <Navigate to="/settings" replace />
  return (
    <ManageFrame nested={nested}>
      <Outlet />
    </ManageFrame>
  )
}

/**
 * The sidebar's list, scrolling inside the panel when the window is shorter
 * than it: an edge with more rows past it fades (so a cut row reads as «more
 * below», not as the list's end), and the section on the screen is kept in
 * view (it may be far down: «Почта и уведомления» on a laptop).
 */
function SidebarScroll({ children }: { children: ReactNode }) {
  const ref = useRef<HTMLDivElement>(null)
  const { pathname } = useLocation()
  const [fade, setFade] = useState({ start: false, end: false })
  const measure = () => {
    const el = ref.current
    if (!el) return
    const over = el.scrollHeight - el.clientHeight
    // a pixel of rounding is not «more»
    const next = { start: over > 1 && el.scrollTop > 1, end: over > 1 && el.scrollTop < over - 1 }
    setFade((f) => (f.start === next.start && f.end === next.end ? f : next))
  }
  useLayoutEffect(() => {
    const el = ref.current
    if (!el) return
    measure()
    if (typeof ResizeObserver === 'undefined') return
    const ro = new ResizeObserver(measure)
    ro.observe(el)
    if (el.firstElementChild) ro.observe(el.firstElementChild)
    return () => ro.disconnect()
  }, [])
  // the current section stays clear of the faded edges (only this panel scrolls, never the page)
  useLayoutEffect(() => {
    const el = ref.current
    const here = el?.querySelector('[aria-current="page"]')
    if (!el || !here) return
    const pad = Math.min(48, el.clientHeight / 4)
    const box = el.getBoundingClientRect()
    const r = here.getBoundingClientRect()
    if (r.top < box.top + pad) el.scrollTop -= box.top + pad - r.top
    else if (r.bottom > box.bottom - pad) el.scrollTop += r.bottom - box.bottom + pad
    measure()
  }, [pathname])
  return (
    <div ref={ref} className={styles.sidebarScroll} onScroll={measure} data-fade-start={fade.start || undefined} data-fade-end={fade.end || undefined}>
      {children}
    </div>
  )
}

/**
 * «Управление» around one section: the section list beside it (on phones a
 * back link to the list instead, unless the screen is `nested` and brings
 * its own). The core's sections and the addons' pages its tiles open
 * (pages/AddonPage.tsx) are drawn in it alike.
 */
export function ManageFrame({ nested, children }: { nested: boolean; children: ReactNode }) {
  const t = useT()
  const compact = useIsCompact()
  if (compact) {
    return (
      <div className={styles.page}>
        {!nested && (
          <NavLink to="/manage" className={styles.back}>
            <Icon name="chevronLeft" size={18} strokeWidth={2.4} />
            {t('common.settings')}
          </NavLink>
        )}
        {children}
      </div>
    )
  }
  return (
    <div className={styles.page}>
      <div className={styles.layout}>
        <aside className={`${styles.sidebar} glass`}>
          <h1 className={styles.sidebarTitle}>{t('common.settings')}</h1>
          <SidebarScroll>
            <SectionList />
          </SidebarScroll>
        </aside>
        <div className={styles.content}>{children}</div>
      </div>
    </div>
  )
}

/** The plain page frame: room for the floating navigation, the gutters and the page's end (an addon's page that is not «Управление»'s). */
export function PageFrame({ children }: { children: ReactNode }) {
  return <div className={styles.page}>{children}</div>
}
