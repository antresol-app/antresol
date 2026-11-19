// Avatar button in the top-right corner. Opens the app's Menu (a sheet on a
// phone) with the current profile, its places («Файлы», «Мои запросы», the
// addons' entries), «Настройки» (the profile's own: pages/ProfileSettings.tsx;
// an admin's open «Управление», the profile's and the server's in two parts) and «Сменить профиль», then the profile's
// library («Общая медиатека / Моя медиатека», as Photos has it in its View
// menu besides the toolbar picker).
import { useNavigate } from 'react-router'
import type { User } from '../api/types'
import { useLibraryScope } from '../state/libraryScope'
import { usePendingRequests } from '../state/kidRequests'
import { session } from '../state/session'
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
  // requests waiting for an admin's answer (none where requests are closed)
  const pending = usePendingRequests(user.id, Boolean(user.requests))

  const items: Item[] = [
    // what comes out of the titles this profile marked
    {
      id: 'calendar',
      label: t('tracking.calendar.title'),
      icon: 'clock' as const,
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
    user.isAdmin
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
        },
    {
      id: 'switch',
      label: t('comp.profile.switch'),
      icon: 'switchUser',
      run: () => {
        if (onSwitchProfile) onSwitchProfile()
        // The session is cleared locally even if the server can't be reached.
        else
          void session
            .logout()
            .catch(() => {})
            .finally(() => navigate('/profiles'))
      },
    },
  ]

  return (
    <div className={styles.root}>
      <Menu
        label={t('comp.profile.role')}
        placement="bottom-end"
        pickList={false}
        // the profile, its places and the library: taller than a usual menu (still cut to the window)
        maxHeight={720}
        className={styles.menu}
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
            <div className={styles.head}>
              <Avatar name={user.name} avatar={user.avatar} photo={user.photo} size={44} />
              <div>
                <div className={styles.name}>{user.name}</div>
                <div className={styles.role}>{t(user.isAdmin ? 'common.role.admin' : user.isKid ? 'common.role.kid' : 'comp.profile.role')}</div>
                {/* «Отдельная история на этом устройстве»: where this device's history goes */}
                {user.deviceHistory && <div className={styles.role}>{t('comp.profile.deviceHistory', { name: user.deviceHistory.name })}</div>}
              </div>
            </div>
            <MenuSeparator />
            {items.map((it) => (
              <MenuItem
                key={it.id}
                label={it.label}
                icon={<Icon name={it.icon} size={19} />}
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
          </>
        )}
      </Menu>
    </div>
  )
}
