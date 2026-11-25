// «Настройки» of the signed-in profile (every profile, from the profile
// menu; an admin's are also the first row of «Управление», drawn in its frame): the photo (an adult's), the language, «Звук и субтитры», mail, the Kindle address, the app password, devices, «Ключи API и ИИ» (an adult's, pages/ApiKeysSettings.tsx),
// «Избранные каналы первыми» (the profile's channel list, on the web and in
// the apps: /api/channels/prefs), what is saved on this device and «О сервере». One short list: a row opens
// its sheet, or a page («Аккаунты и синхронизация», pages/AccountsSettings.tsx).
// What applies to the whole server is the admin's, in «Управление → Настройки
// сервера». A kid's sound and mail are the admin's (Профили), as «Скрывать
// прогресс» is.
import { useEffect, useLayoutEffect, useState, type ReactNode } from 'react'
import { Navigate, useLocation, useNavigate } from 'react-router'
import { getChannelPrefs, setChannelPrefs } from '../api/channels'
import { updateMe } from '../api/client'
import type { AccountsState } from '../api/types'
import { APP_NAME } from '../brand'
import { AboutSheet } from '../components/AboutSheet'
import { AppPasswordSheet } from '../components/AppPasswordSheet'
import { DevicesSheet } from '../components/DevicesSheet'
import { KindleAddressRow } from '../components/KindleAddressRow'
import { NotifySheet } from '../components/NotifySheet'
import { PlaybackPrefsSheet } from '../components/PlaybackPrefsSheet'
import { ProfilePhotoRow } from '../components/ProfilePhotoRow'
import { getLanguagePref, useLang, useT, type LangPref } from '../i18n'
import { offlineSupported } from '../offline/store'
import { loadAccountsState, resetAccounts } from '../state/accounts'
import { session, useSession } from '../state/session'
import { chooseInterfaceLanguage } from '../state/uiLanguage'
import { GroupedSection, ListLinkRow, ListRow } from '../ui/List'
import { Select } from '../ui/Select'
import { Toggle } from '../ui/Toggle'
import { ManageFrame } from './manage/Manage'
import m from './manage/Manage.module.css'
import styles from './ProfileSettings.module.css'

type Open = '' | 'playback' | 'notify' | 'appPassword' | 'devices' | 'about'

export function ProfileSettings() {
  const t = useT()
  useLang()
  const { user } = useSession()
  const navigate = useNavigate()
  const [open, setOpen] = useState<Open>('')
  const [pref, setPref] = useState<LangPref>(getLanguagePref)
  const [hideFailed, setHideFailed] = useState(false)
  // «Избранные каналы первыми» (null: not read yet)
  const [favFirst, setFavFirst] = useState<boolean | null>(null)
  const [favFirstFailed, setFavFirstFailed] = useState(false)
  const { search } = useLocation()
  // the linked accounts, for the row that leads to their page (null: this server has none)
  const [accounts, setAccounts] = useState<AccountsState | null>(null)

  // a fresh read of the accounts: the cached link state may be a session old
  // (a layout effect: it runs before the load below)
  useLayoutEffect(() => {
    resetAccounts()
  }, [])
  const adult = !!user && !user.isKid
  useEffect(() => {
    if (!adult) return
    let live = true
    void loadAccountsState().then((a) => live && setAccounts(a?.shikimori ? a : null))
    return () => {
      live = false
    }
  }, [adult, user?.id])

  useEffect(() => {
    document.title = `${t('common.settings')} — ${APP_NAME}`
  }, [t])

  useEffect(() => {
    const ac = new AbortController()
    getChannelPrefs(ac.signal).then((p) => setFavFirst(p?.favoritesFirst !== false), () => undefined)
    return () => ac.abort()
  }, [user?.id])

  if (!user) return null
  // the old address of Shikimori's sign-in coming back (and the links made from it): the accounts page has it now
  if (new URLSearchParams(search).get('account') === 'shikimori') return <Navigate to={`/settings/accounts${search}`} replace />
  const close = () => setOpen('')
  const linked = accounts ? [accounts.shikimori, accounts.myshows, accounts.goodreads].filter((a) => a?.linked).length : 0

  const toggleFavFirst = (on: boolean) => {
    setFavFirst(on)
    setFavFirstFailed(false)
    setChannelPrefs({ favoritesFirst: on }).catch(() => {
      setFavFirst(!on)
      setFavFirstFailed(true)
    })
  }

  const toggleHidden = (on: boolean) => {
    setHideFailed(false)
    updateMe({ hideProgress: on })
      .then((u) => session.updateUser(u))
      .catch(() => setHideFailed(true))
  }

  // an admin's own settings are the first row of «Управление»: drawn in its
  // frame, beside the server's sections (on a phone with the back link to
  // its list); everyone else's are a page of their own
  const Frame = user.isAdmin ? AdminFrame : PlainFrame
  return (
    <Frame>
      <section aria-labelledby="prefs-title" className={styles.column}>
        <header className={m.header}>
          <h1 id="prefs-title" className={m.title}>
            {user.isAdmin ? t('manage.profile') : t('common.settings')}
          </h1>
        </header>

        {/* a kid's photo is the admin's (Профили), as «Скрывать прогресс» is */}
        {adult && (
          <GroupedSection>
            {/* the answer without a photo has no «photo» at all: say so, or a merge keeps the old one */}
            <ProfilePhotoRow user={user} onUser={(u) => session.updateUser({ ...u, photo: u.photo })} />
          </GroupedSection>
        )}

        <GroupedSection>
          {/* on a 320 pt phone the pop-up gives way before the label breaks inside a word */}
          <ListRow shrink label={t('common.uiLanguage')}>
            <Select
              label={t('common.uiLanguage')}
              placement="bottom-end"
              value={pref}
              options={[
                { value: 'system', label: t('common.uiLanguage.system') },
                { value: 'ru', label: t('common.uiLanguage.ru') },
                { value: 'en', label: t('common.uiLanguage.en') },
              ]}
              onChange={(v: LangPref) => {
                setPref(v)
                chooseInterfaceLanguage(v)
              }}
            />
          </ListRow>
        </GroupedSection>

        {/* a kid's sound and «Скрывать прогресс» are the admin's; the channel order is everyone's own */}
        {(adult || favFirst !== null) && (
          <GroupedSection title={t('prefs.watching')}>
            {adult && <ListLinkRow icon="speaker" tint="pink" label={t('prefs.playback')} onOpen={() => setOpen('playback')} />}
            {adult && (
              <ListRow label={t('comp.profile.hideProgress')} hint={t('prefs.hideProgress.hint')} status={hideFailed ? t('comp.profile.hideProgressFailed') : undefined}>
                <Toggle label={t('comp.profile.hideProgress')} checked={!!user.hideProgress} onChange={toggleHidden} />
              </ListRow>
            )}
            {favFirst !== null && (
              <ListRow label={t('prefs.favoritesFirst')} status={favFirstFailed ? t('prefs.favoritesFirst.failed') : undefined}>
                <Toggle label={t('prefs.favoritesFirst')} checked={favFirst} onChange={toggleFavFirst} />
              </ListRow>
            )}
          </GroupedSection>
        )}

        {adult && (
          <GroupedSection title={t('prefs.notifications')}>
            <ListLinkRow icon="bell" tint="red" label={t('prefs.notify')} onOpen={() => setOpen('notify')} />
          </GroupedSection>
        )}

        {/* the Send to Kindle address is the profile's own (the sending mailbox is the server's) */}
        {adult && (
          <GroupedSection title={t('prefs.books')}>
            <KindleAddressRow user={user} />
          </GroupedSection>
        )}

        {adult && accounts && (
          <GroupedSection>
            <ListLinkRow icon="link" tint="cyan" label={t('acct.section')} value={linked ? t('acct.section.linked', { count: linked }) : t('acct.section.none')} onOpen={() => navigate('/settings/accounts')} />
          </GroupedSection>
        )}

        <GroupedSection title={t('prefs.apps')}>
          <ListLinkRow icon="key" tint="gray" label={t('prefs.appPassword')} hint={t('prefs.appPassword.hint')} onOpen={() => setOpen('appPassword')} />
          <ListLinkRow icon="devices" tint="blue" label={t('prefs.devices')} onOpen={() => setOpen('devices')} />
          {adult && <ListLinkRow icon="sparkles" tint="purple" label={t('apikeys.title')} hint={t('apikeys.row.hint')} onOpen={() => navigate('/settings/api-keys')} />}
          {offlineSupported() && <ListLinkRow icon="phoneDown" tint="green" label={t('comp.profile.onDevice')} onOpen={() => navigate('/offline')} />}
        </GroupedSection>

        <GroupedSection>
          <ListLinkRow icon="info" tint="gray" label={t('prefs.about')} onOpen={() => setOpen('about')} />
        </GroupedSection>
      </section>

      {open === 'playback' && <PlaybackPrefsSheet onClose={close} />}
      {open === 'notify' && <NotifySheet onClose={close} />}
      {open === 'appPassword' && <AppPasswordSheet userName={user.name} passwordless={!user.hasPassword} onClose={close} />}
      {open === 'devices' && <DevicesSheet onClose={close} />}
      {open === 'about' && <AboutSheet onClose={close} />}
    </Frame>
  )
}

const AdminFrame = ({ children }: { children: ReactNode }) => <ManageFrame nested={false}>{children}</ManageFrame>
const PlainFrame = ({ children }: { children: ReactNode }) => <div className={m.page}>{children}</div>

export default ProfileSettings
