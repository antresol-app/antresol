// Avatar button in the top-right corner. Opens the app's Menu (a sheet on a
// phone) with the current profile, «Управление» (admins only), «На этом
// устройстве» (saved albums and books), «Звук и субтитры…» (adults),
// «Пароль для приложений…», «Устройства…», «О сервере…» and «Сменить профиль»,
// then the profile's library («Общая медиатека / Моя медиатека», as Photos
// has it in its View menu besides the toolbar picker) and the language.
import { useRef, useState } from 'react'
import { useNavigate } from 'react-router'
import { updateMe } from '../api/client'
import type { User } from '../api/types'
import { offlineSupported } from '../offline/store'
import { useLibraryScope } from '../state/libraryScope'
import { usePendingRequests } from '../state/kidRequests'
import { session } from '../state/session'
import { getLanguagePref, useLang, useT, type LangPref } from '../i18n'
import { chooseInterfaceLanguage } from '../state/uiLanguage'
import { AboutSheet } from './AboutSheet'
import { AppPasswordSheet } from './AppPasswordSheet'
import { Avatar } from './Avatar'
import { DevicesSheet } from './DevicesSheet'
import { NotifySheet } from './NotifySheet'
import { PlaybackPrefsSheet } from './PlaybackPrefsSheet'
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

const LANG_PREFS = [
  { value: 'system', label: 'common.uiLanguage.system' },
  { value: 'ru', label: 'common.uiLanguage.ru' },
  { value: 'en', label: 'common.uiLanguage.en' },
] as const satisfies readonly { value: LangPref; label: string }[]

export function ProfileMenu({ user, onSwitchProfile }: { user: User; onSwitchProfile?: () => void }) {
  const t = useT()
  useLang()
  const pref = getLanguagePref()
  const library = useLibraryScope()
  const [open, setOpen] = useState(false)
  const [appPassword, setAppPassword] = useState(false)
  const [devices, setDevices] = useState(false)
  const [playback, setPlayback] = useState(false)
  const [notify, setNotify] = useState(false)
  const [about, setAbout] = useState(false)
  const [hideFailed, setHideFailed] = useState(false)
  const navigate = useNavigate()
  const buttonRef = useRef<HTMLButtonElement>(null)
  // requests waiting for an admin's answer (none where requests are closed)
  const pending = usePendingRequests(user.id, Boolean(user.requests))

  // «Скрывать прогресс просмотра»: an adult's own choice (a kid's is the admin's, in Профили)
  const toggleHidden = () => {
    setHideFailed(false)
    updateMe({ hideProgress: !user.hideProgress })
      .then((u) => session.updateUser(u))
      .catch(() => setHideFailed(true))
  }

  const items: Item[] = [
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
    ...(user.isAdmin
      ? [
          {
            id: 'manage',
            label: t('common.manage'),
            icon: 'gear' as const,
            run: () => navigate(pending > 0 ? '/manage/requests' : '/manage'),
            badge: pending,
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
    // what this adult follows (the admin sees every follow in «Управление → Слежение»);
    // a library-only server has no «Следить»
    ...(!__LIBRARY_EDITION__ && !user.isAdmin && !user.isKid && !user.libraryOnly
      ? [
          {
            id: 'follows',
            label: t('common.myFollows'),
            icon: 'eye' as const,
            run: () => navigate('/follows'),
          },
        ]
      : []),
    // what is saved for listening and reading without the server (and the space it takes)
    ...(offlineSupported()
      ? [
          {
            id: 'offline',
            label: t('comp.profile.onDevice'),
            icon: 'download' as const,
            run: () => navigate('/offline'),
          },
        ]
      : []),
    // «Звук и субтитры»: an adult's own defaults (a kid's are the admin's, in Профили)
    ...(!user.isKid
      ? [
          {
            id: 'playback',
            label: t('comp.profile.playback'),
            icon: 'captions' as const,
            run: () => setPlayback(true),
          },
        ]
      : []),
    // «Уведомления на почту»: an adult's own (a kid's are the admin's, in Профили)
    ...(!user.isKid
      ? [
          {
            id: 'notify',
            label: t('comp.profile.notify'),
            icon: 'send' as const,
            run: () => setNotify(true),
          },
        ]
      : []),
    // Subsonic music apps and OPDS readers sign in with it (kids too).
    {
      id: 'app-password',
      label: t('comp.profile.appPassword'),
      icon: 'lock',
      run: () => setAppPassword(true),
    },
    // the Vault app on iPhone, iPad and Mac signed in to this profile
    {
      id: 'devices',
      label: t('comp.profile.devices'),
      icon: 'pip',
      run: () => setDevices(true),
    },
    // «О сервере»: the version, the licence, the source offer (AGPL §13) and the TMDB notice
    {
      id: 'about',
      label: t('comp.about.menu'),
      icon: 'info',
      run: () => setAbout(true),
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
        // the profile, its places, the library and the language: taller than a usual menu (still cut to the window)
        maxHeight={720}
        className={styles.menu}
        onOpenChange={setOpen}
        trigger={(tp) => (
          <button
            {...tp}
            ref={(el) => {
              buttonRef.current = el
              if (typeof tp.ref === 'function') tp.ref(el)
              else if (tp.ref) (tp.ref as { current: HTMLButtonElement | null }).current = el
            }}
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
            <Avatar name={user.name} avatar={user.avatar} size={36} />
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
              <Avatar name={user.name} avatar={user.avatar} size={44} />
              <div>
                <div className={styles.name}>{user.name}</div>
                <div className={styles.role}>{t(user.isAdmin ? 'common.role.admin' : user.isKid ? 'common.role.kid' : 'comp.profile.role')}</div>
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
            {/* a family setting: in the «Библиотека» group, not a group of its own */}
            {!user.isKid && (
              <>
                {!library.available && <MenuSeparator />}
                <MenuItem label={t('comp.profile.hideProgress')} checked={!!user.hideProgress} multi onSelect={toggleHidden} />
                {/* shown here, announced by the live region outside the menu (a menu holds only items) */}
                {hideFailed && (
                  <p className={styles.failed} aria-hidden>
                    {t('comp.profile.hideProgressFailed')}
                  </p>
                )}
              </>
            )}
            {/* «Язык интерфейса»: per browser, for every profile (only the admin has Settings) */}
            <MenuSeparator />
            <div role="group" aria-label={t('common.uiLanguage')}>
              <MenuHeading>{t('common.uiLanguage')}</MenuHeading>
              {LANG_PREFS.map((o) => (
                <MenuItem key={o.value} label={t(o.label)} lang={o.value === 'system' ? undefined : o.value} checked={pref === o.value} onSelect={() => chooseInterfaceLanguage(o.value)} />
              ))}
            </div>
          </>
        )}
      </Menu>
      <p className="visually-hidden" role="alert">
        {open && hideFailed ? t('comp.profile.hideProgressFailed') : ''}
      </p>
      {appPassword && (
        <AppPasswordSheet
          userName={user.name}
          passwordless={!user.hasPassword}
          onClose={() => {
            setAppPassword(false)
            buttonRef.current?.focus()
          }}
        />
      )}
      {playback && (
        <PlaybackPrefsSheet
          onClose={() => {
            setPlayback(false)
            buttonRef.current?.focus()
          }}
        />
      )}
      {notify && (
        <NotifySheet
          onClose={() => {
            setNotify(false)
            buttonRef.current?.focus()
          }}
        />
      )}
      {about && (
        <AboutSheet
          onClose={() => {
            setAbout(false)
            buttonRef.current?.focus()
          }}
        />
      )}
      {devices && (
        <DevicesSheet
          onClose={() => {
            setDevices(false)
            buttonRef.current?.focus()
          }}
        />
      )}
    </div>
  )
}
