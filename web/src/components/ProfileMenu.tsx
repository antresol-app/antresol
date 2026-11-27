// Avatar button in the top-right corner. Opens the app's Menu, laid out as
// the iOS app's native one (an account menu on iOS 26): the profile as its
// first row (avatar, name, role; it opens «Настройки»), its places
// («Календарь», «Файлы», «Мои запросы», the addons' entries) and «Настройки»
// (the profile's own: pages/ProfileSettings.tsx; an admin's open
// «Управление», the profile's and the server's in two parts); then the
// profile's library («Общая медиатека / Моя медиатека», as Photos has it in
// its View menu besides the toolbar picker); «Сменить профиль» last, a group
// of its own. No styles of its own: Menu draws all of it.
import { useLocation, useNavigate } from 'react-router'
import type { User } from '../api/types'
import { useLibraryScope } from '../state/libraryScope'
import { usePendingRequests } from '../state/kidRequests'
import { useLang, useT } from '../i18n'
import { addonString } from '../addons/registry'
import { useProfileMenu } from '../addons/slots'
import { Avatar } from './Avatar'
import { Icon, type IconName } from './Icon'
import { LIBRARY_OPTIONS } from './LibraryScopeMenu'
import styles from './ProfileMenu.module.css'
import { Menu, MenuHeading, MenuItem, MenuSeparator } from '../ui/Menu'

interface Item {
  id: string
  label: string
  icon: IconName
  run: () => void
  /** A count beside the label (requests waiting for an admin). */
  badge?: number
}

export function ProfileMenu({ user, onSwitchProfile }: { user: User; onSwitchProfile?: () => void }) {
  const t = useT()
  const lang = useLang()
  // the addons' entries (none for a kid; visible(user) may hide one)
  const addonEntries = useProfileMenu()
  const library = useLibraryScope()
  const navigate = useNavigate()
  const location = useLocation()
  // requests waiting for an admin's answer (none where requests are closed)
  const pending = usePendingRequests(user.id, Boolean(user.requests))

  // «Сменить профиль»: the last group, as an account menu ends with its sign-out
  const switchProfile = () => {
    // the picker opens over the current session: it ends only when another
    // profile signs in, and «Отмена» (Back, Escape) returns here still signed in
    if (onSwitchProfile) onSwitchProfile()
    else navigate('/profiles', { state: { from: location.pathname + location.search, switching: true } })
  }

  const settings: Item = user.isAdmin
    ? {
        id: 'settings',
        label: t('comp.profile.settings'),
        icon: 'sliders' as const,
        run: () => navigate(pending > 0 ? '/manage/requests' : '/manage'),
        badge: pending,
      }
    : {
        id: 'settings',
        label: t('comp.profile.settings'),
        icon: 'sliders' as const,
        run: () => navigate('/settings'),
      }

  const items: Item[] = [
    // what comes out of the titles this profile marked
    {
      id: 'calendar',
      label: t('tracking.calendar.title'),
      icon: 'calendar' as const,
      run: () => navigate('/calendar'),
    },
    // The file browser of the media disk (not for kid profiles).
    ...(!user.isKid
      ? [
          {
            id: 'files',
            label: t('common.files'),
            icon: 'folder' as const,
            run: () => navigate('/files'),
          },
        ]
      : []),
    // what this kid asked for («Попросить») and the answers
    ...(user.isKid && user.canRequest
      ? [
          {
            id: 'requests',
            label: t('requests.mine'),
            icon: 'send' as const,
            run: () => navigate('/requests'),
          },
        ]
      : []),
    // the addons' places of the profile
    ...addonEntries.map((e, i) => ({
      id: `addon:${e.addonId}:${i}`,
      label: addonString(e.addonId, lang, e.label),
      icon: e.icon,
      run: () => navigate(e.to),
    })),
    // «Настройки»: the profile's own (language, sound, mail, app password,
    // devices); an admin's open «Управление», where they are the first part
    // and the server's the second (with the requests' count)
    settings,
  ]

  return (
    <div className={styles.root}>
      <Menu
        label={t('comp.profile.role')}
        placement="bottom-end"
        pickList={false}
        // the profile, its places and the library: taller than a usual menu (still cut to the window)
        maxHeight={720}
        trigger={(tp) => (
          <button
            {...tp}
            type="button"
            className={styles.trigger}
            onKeyDown={(e) => {
              // ↓ opens it, as on a macOS menu button
              if (e.key === 'ArrowDown' && !tp['aria-expanded']) {
                e.preventDefault()
                e.currentTarget.click()
              }
            }}
            aria-label={t('comp.profile.aria', { name: user.name })}
          >
            <Avatar name={user.name} avatar={user.avatar} photo={user.photo} size={36} />
            {pending > 0 && (
              <>
                <span className={styles.dot} aria-hidden />
                <span className="visually-hidden">{t('requests.badge', { count: pending })}</span>
              </>
            )}
          </button>
        )}
      >
        {(close) => (
          <>
            {/* the profile: its avatar where a row's icon stands, the role (and
                this device's own history) under the name; it opens «Настройки».
                Reached with the arrows, never focused first. */}
            <MenuItem
              aside
              label={user.name}
              detail={[
                t(user.isAdmin ? 'common.role.admin' : user.isKid ? 'common.role.kid' : 'comp.profile.role'),
                ...(user.deviceHistory ? [t('comp.profile.deviceHistory', { name: user.deviceHistory.name })] : []),
              ]}
              icon={<Avatar name={user.name} avatar={user.avatar} photo={user.photo} size={30} />}
              onSelect={() => {
                close(false)
                settings.run()
              }}
            />
            <MenuSeparator />
            {items.map((it) => (
              <MenuItem
                key={it.id}
                label={it.label}
                icon={<Icon name={it.icon} />}
                badge={it.badge ? { text: it.badge > 99 ? '99+' : String(it.badge) } : undefined}
                ariaLabel={it.badge ? `${it.label}, ${t('requests.badge', { count: it.badge })}` : undefined}
                onSelect={() => {
                  close(false)
                  it.run()
                }}
              />
            ))}
            {library.available && (
              <>
                <MenuSeparator />
                <div role="group" aria-label={t('comp.library.label')}>
                  <MenuHeading>{t('comp.library.label')}</MenuHeading>
                  {LIBRARY_OPTIONS.map((o) => (
                    <MenuItem key={o.value} label={t(o.label)} checked={library.scope === o.value} onSelect={() => library.setScope(o.value)} />
                  ))}
                </div>
              </>
            )}
            <MenuSeparator />
            <MenuItem
              label={t('comp.profile.switch')}
              icon={<Icon name="switchUser" />}
              onSelect={() => {
                close(false)
                switchProfile()
              }}
            />
          </>
        )}
      </Menu>
    </div>
  )
}
