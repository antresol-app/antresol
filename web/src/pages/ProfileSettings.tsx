// «Настройки» of the signed-in profile (every profile, from the profile
// menu; an admin's are also the first row of «Управление», drawn in its frame): the photo (an adult's), the language, «Звук и субтитры», mail, the app password, devices,
// what is saved on this device and «О сервере». One short list: a row opens
// its sheet. What applies to the whole server is the admin's, in
// «Управление → Настройки сервера». A kid's sound and mail are the admin's
// (Профили), as «Скрывать прогресс» is.
import { useEffect, useLayoutEffect, useState, type ReactNode } from 'react'
import { useNavigate, useSearchParams } from 'react-router'
import { updateMe } from '../api/client'
import { APP_NAME } from '../brand'
import { AboutSheet } from '../components/AboutSheet'
import { GoodreadsAccount } from '../components/accounts/GoodreadsAccount'
import { MyShowsAccount } from '../components/accounts/MyShowsAccount'
import { ShikimoriAccount } from '../components/accounts/ShikimoriAccount'
import { AppPasswordSheet } from '../components/AppPasswordSheet'
import { DevicesSheet } from '../components/DevicesSheet'
import { NotifySheet } from '../components/NotifySheet'
import { PlaybackPrefsSheet } from '../components/PlaybackPrefsSheet'
import { ProfilePhotoRow } from '../components/ProfilePhotoRow'
import { getLanguagePref, useLang, useT, type Key, type LangPref } from '../i18n'
import { offlineSupported } from '../offline/store'
import { showNotice } from '../state/notice'
import { resetAccounts } from '../state/accounts'
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
  const [params, setParams] = useSearchParams()

  // back from Shikimori's sign-in (the server's redirect): say how it went, clear the address
  useEffect(() => {
    if (params.get('account') !== 'shikimori') return
    const error = params.get('error')
    const known = ['session', 'state', 'denied', 'exchange', 'account']
    resetAccounts() // the link's state changed on the server
    showNotice(error ? t(`acct.err.${known.includes(error) ? error : 'exchange'}` as Key) : t('acct.ok'), 5000)
    setParams({}, { replace: true })
  }, [params, setParams, t])

  // a fresh read of the accounts: the cached link state may be a session old
  // (a layout effect: it runs before the account blocks' own loads start)
  useLayoutEffect(() => {
    resetAccounts()
  }, [])

  useEffect(() => {
    document.title = `${t('common.settings')} — ${APP_NAME}`
  }, [t])

  if (!user) return null
  const adult = !user.isKid
  const close = () => setOpen('')

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

        {adult && (
          <GroupedSection title={t('prefs.watching')}>
            <ListLinkRow icon="speaker" tint="pink" label={t('prefs.playback')} onOpen={() => setOpen('playback')} />
            <ListRow label={t('comp.profile.hideProgress')} hint={t('prefs.hideProgress.hint')} status={hideFailed ? t('comp.profile.hideProgressFailed') : undefined}>
              <Toggle label={t('comp.profile.hideProgress')} checked={!!user.hideProgress} onChange={toggleHidden} />
            </ListRow>
          </GroupedSection>
        )}

        {adult && (
          <GroupedSection title={t('prefs.notifications')}>
            <ListLinkRow icon="bell" tint="red" label={t('prefs.notify')} onOpen={() => setOpen('notify')} />
          </GroupedSection>
        )}

        {adult && <ShikimoriAccount />}
        {adult && <MyShowsAccount />}
        {adult && <GoodreadsAccount />}

        <GroupedSection title={t('prefs.apps')}>
          <ListLinkRow icon="key" tint="gray" label={t('prefs.appPassword')} hint={t('prefs.appPassword.hint')} onOpen={() => setOpen('appPassword')} />
          <ListLinkRow icon="devices" tint="blue" label={t('prefs.devices')} onOpen={() => setOpen('devices')} />
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
