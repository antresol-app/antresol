// «Настройки» of the signed-in profile (every profile, from the profile
// menu): the language, «Звук и субтитры», mail, the app password, devices,
// what is saved on this device and «О сервере». One short list: a row opens
// its sheet. What applies to the whole server is the admin's, in
// «Управление → Настройки сервера». A kid's sound and mail are the admin's
// (Профили), as «Скрывать прогресс» is.
import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router'
import { updateMe } from '../api/client'
import { APP_NAME } from '../brand'
import { AboutSheet } from '../components/AboutSheet'
import { AppPasswordSheet } from '../components/AppPasswordSheet'
import { DevicesSheet } from '../components/DevicesSheet'
import { NotifySheet } from '../components/NotifySheet'
import { PlaybackPrefsSheet } from '../components/PlaybackPrefsSheet'
import { getLanguagePref, useLang, useT, type LangPref } from '../i18n'
import { offlineSupported } from '../offline/store'
import { session, useSession } from '../state/session'
import { chooseInterfaceLanguage } from '../state/uiLanguage'
import { GroupedSection, ListLinkRow, ListRow } from '../ui/List'
import { Select } from '../ui/Select'
import { Toggle } from '../ui/Toggle'
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

  return (
    <div className={m.page}>
      <section aria-labelledby="prefs-title" className={styles.column}>
        <header className={m.header}>
          <h1 id="prefs-title" className={m.title}>
            {t('common.settings')}
          </h1>
        </header>

        <GroupedSection>
          <ListRow label={t('common.uiLanguage')}>
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
            <ListLinkRow icon="captions" tint="var(--blue)" label={t('prefs.playback')} onOpen={() => setOpen('playback')} />
            <ListRow label={t('comp.profile.hideProgress')} hint={t('prefs.hideProgress.hint')} status={hideFailed ? t('comp.profile.hideProgressFailed') : undefined}>
              <Toggle label={t('comp.profile.hideProgress')} checked={!!user.hideProgress} onChange={toggleHidden} />
            </ListRow>
          </GroupedSection>
        )}

        {adult && (
          <GroupedSection title={t('prefs.notifications')}>
            <ListLinkRow icon="send" tint="var(--red)" label={t('prefs.notify')} onOpen={() => setOpen('notify')} />
          </GroupedSection>
        )}

        <GroupedSection title={t('prefs.apps')}>
          <ListLinkRow icon="lock" tint="var(--gray)" label={t('prefs.appPassword')} hint={t('prefs.appPassword.hint')} onOpen={() => setOpen('appPassword')} />
          <ListLinkRow icon="pip" tint="var(--indigo)" label={t('prefs.devices')} onOpen={() => setOpen('devices')} />
          {offlineSupported() && <ListLinkRow icon="download" tint="var(--green)" label={t('comp.profile.onDevice')} onOpen={() => navigate('/offline')} />}
        </GroupedSection>

        <GroupedSection>
          <ListLinkRow icon="info" tint="var(--gray)" label={t('prefs.about')} onOpen={() => setOpen('about')} />
        </GroupedSection>
      </section>

      {open === 'playback' && <PlaybackPrefsSheet onClose={close} />}
      {open === 'notify' && <NotifySheet onClose={close} />}
      {open === 'appPassword' && <AppPasswordSheet userName={user.name} passwordless={!user.hasPassword} onClose={close} />}
      {open === 'devices' && <DevicesSheet onClose={close} />}
      {open === 'about' && <AboutSheet onClose={close} />}
    </div>
  )
}

export default ProfileSettings
