// «Настройки сервера» (admin): what applies to the whole server, a screen per
// group, /manage/settings/<group>, one row per setting; the groups are rows
// of «Управление»'s list (Manage.tsx), each in its place there. A profile's own settings (the language,
// sound, notifications) are in pages/ProfileSettings.tsx. Changes save
// by themselves (like iOS); «Качество вне дома» applies at once on the
// server. The core build has only the library's settings (docs/build-tags.md).
// An addon's own settings are its pages.
import { APP_NAME } from '../../brand'
import { useCallback, useEffect, useRef, useState } from 'react'
import { getLibraryStatus, getSettings, saveSettings, scanLibrary, errorText } from '../../api/client'
import type { LangCode, LibraryStatus, MailSettings, Settings as Values } from '../../api/types'
import { Button } from '../../ui/Button'
import { Sheet } from '../../ui/Sheet'
import { langChoices, langLabel } from '../../components/langs'
import { Icon } from '../../components/Icon'
import { RankedList } from '../../components/RankedList'
import { SegmentedControl } from '../../ui/SegmentedControl'
import { MultiSelect } from '../../ui/Select'
import { StudioList } from '../../components/StudioList'
import { TextField } from '../../ui/TextField'
import { Toggle } from '../../ui/Toggle'
import { GroupedSection, ListLinkRow, ListRow } from '../../ui/List'
import { Navigate, useNavigate, useParams } from 'react-router'
import { useIsCompact } from '../../state/useMediaQuery'
import { useT } from '../../i18n'
import { ShikimoriAdmin } from '../../components/accounts/ShikimoriAdmin'
import { AISettingsSection } from './AISettings'
import { ExternalServicesSection } from './ExternalServicesSettings'
import { PublicAddressSection } from './PublicAddressSettings'
import { StorageSection } from './StorageSettings'
import { TelegramSection } from './TelegramSettings'
import { TranscodingSection } from './TranscodingSettings'
import { VocleanSection } from './VocleanSettings'
import { Spinner } from '../../ui/Spinner'
import m from './Manage.module.css'
import styles from './Settings.module.css'

const DEFAULT_LANGS: LangCode[] = ['ru', 'en']

/**
 * «Языки» changed: subtitles keep only these, in their order (the server
 * does the same), and at least the first.
 */
export function withLanguages(v: Values, languages: LangCode[]): Partial<Values> {
  const subs = languages.filter((c) => (v.subtitleLangs ?? []).includes(c))
  return { languages, subtitleLangs: subs.length ? subs : [languages[0]!] }
}

/** The screens of «Настройки сервера», in their order in «Управление» (Manage.tsx: MANAGE_SECTIONS). */
export type SettingsGroup = 'library' | 'watching' | 'processing' | 'music' | 'subtitles' | 'kindle' | 'ai' | 'accounts' | 'address' | 'external'

interface GroupInfo {
  id: SettingsGroup
  label: 'settings.group.library' | 'settings.watching' | 'settings.group.processing' | 'common.music' | 'common.subtitles' | 'settings.mailGroup' | 'settings.group.ai' | 'acct.section' | 'settings.group.address' | 'ext.title'
}

/** The groups of the page. */
export function settingsGroups(): GroupInfo[] {
  return [
    { id: 'library', label: 'settings.group.library' },
    { id: 'watching', label: 'settings.watching' },
    { id: 'processing', label: 'settings.group.processing' },
    { id: 'music', label: 'common.music' },
    { id: 'subtitles', label: 'common.subtitles' },
    { id: 'kindle', label: 'settings.mailGroup' },
    { id: 'ai', label: 'settings.group.ai' },
    { id: 'accounts', label: 'acct.section' },
    { id: 'address', label: 'settings.group.address' },
    { id: 'external', label: 'ext.title' },
  ]
}

/** «Место для превью» choices, GB. */
const TRICKPLAY_BUDGETS = [5, 10, 20, 50]

export function Settings() {
  const [values, setValues] = useState<Values | null>(null)
  const [error, setError] = useState('')
  const [saved, setSaved] = useState<'' | 'saving' | 'saved'>('')
  const timer = useRef<number | undefined>(undefined)
  const latest = useRef<Values | null>(null)
  const pending = useRef(false)
  const t = useT()
  const group = useParams().group as SettingsGroup | undefined
  const compact = useIsCompact()

  useEffect(() => {
    document.title = `${t('settings.serverTitle')} — ${APP_NAME}`
    const ac = new AbortController()
    getSettings(ac.signal).then(
      (v) => {
        latest.current = v
        setValues(v)
      },
      (e: unknown) => !ac.signal.aborted && setError(errorText(e, t('settings.loadFailed'))),
    )
    return () => ac.abort()
  }, [])

  const flush = useCallback(async () => {
    const v = latest.current
    if (!v) return
    setSaved('saving')
    try {
      const out = await saveSettings(v)
      setError('')
      setSaved('saved')
      // keep what the person is typing; only take the server's view when nothing changed meanwhile
      if (latest.current === v) {
        latest.current = out
        setValues(out)
      }
    } catch (e) {
      setSaved('')
      setError(errorText(e, t('settings.saveFailed')))
    }
  }, [])

  const change = (patch: Partial<Values>, wait = 0) => {
    if (!latest.current) return
    const next = { ...latest.current, ...patch }
    latest.current = next
    setValues(next)
    window.clearTimeout(timer.current)
    pending.current = true
    timer.current = window.setTimeout(() => {
      pending.current = false
      void flush()
    }, wait)
  }
  // leaving with a change still waiting (a number field's pause): send it
  useEffect(
    () => () => {
      window.clearTimeout(timer.current)
      if (pending.current) void flush()
    },
    [flush],
  )

  if (!values) {
    return (
      <section>
        <h2 className={m.title}>{t('settings.serverTitle')}</h2>
        {error ? (
          <div className={`${m.problem} ${m.problemTop}`} role="alert">
            {error}
          </div>
        ) : (
          <div className={m.spinnerRow} role="status">
            <Spinner track className={m.spinner} /> {t('settings.loading')}
          </div>
        )}
      </section>
    )
  }

  const languages = values.languages?.length ? values.languages : DEFAULT_LANGS
  const mine = languages.map((c) => ({ value: c, label: langLabel(c) }))
  const groups = settingsGroups()
  // «Перекодирование» became part of «Обработка»
  if ((group as string) === 'transcoding') return <Navigate to="/manage/settings/processing" replace />
  const current = groups.find((g) => g.id === group)
  if (!current) {
    // the groups are rows of «Управление» (Manage.tsx), not a list of their own:
    // the old address of the list, or a group this build does not have, lands
    // on the first group (on a phone: on «Управление»'s list)
    return <Navigate to={compact ? '/manage' : `/manage/settings/${groups[0]!.id}`} replace />
  }
  const at = (id: SettingsGroup) => group === id
  return (
    <section aria-labelledby="settings-title" className={styles.page}>
      <header className={m.header}>
        <div>
          <h2 id="settings-title" className={m.title}>
            {t(current.label)}
          </h2>
          <p className={m.subtitle} aria-live="polite">
            {saved === 'saving' ? t('settings.saving') : saved === 'saved' ? t('settings.saved') : t('settings.autosave')}
          </p>
        </div>
      </header>
      {error && (
        <div className={m.problem} role="alert">
          {error}
        </div>
      )}

      {at('library') && values.metadataLanguage !== undefined && (
        <GroupedSection>
          <ListRow stack label={t('settings.metadataLanguage')} hint={t('settings.metadataLanguage.hint')}>
            <SegmentedControl
              label={t('settings.metadataLanguage')}
              segments={[
                { value: 'ru', label: t('common.uiLanguage.ru') },
                { value: 'en', label: t('common.uiLanguage.en') },
              ]}
              value={values.metadataLanguage}
              onChange={(v) => change({ metadataLanguage: v })}
            />
          </ListRow>
        </GroupedSection>
      )}

      {at('watching') && (
      <GroupedSection>
        <ListRow
          stack
          label={t('settings.remoteQuality')}
          hint={t('settings.remoteQuality.hint')}
        >
          <SegmentedControl
            label={t('settings.remoteQuality')}
            segments={[
              { value: 'original', label: t('common.original') },
              { value: '1080', label: '1080p' },
              { value: '720', label: '720p' },
              { value: '480', label: '480p' },
            ]}
            value={values.remoteQuality ?? '1080'}
            onChange={(v) => change({ remoteQuality: v as Values['remoteQuality'] })}
          />
        </ListRow>
      </GroupedSection>
      )}

      {at('watching') && (
      <GroupedSection title={t('settings.languages')}>
        <ListRow
          wide
          label={t('settings.myLanguages')}
          hint={t('settings.myLanguages.hint')}
          id="my-langs"
        >
          <RankedList
            labelledBy="my-langs"
            items={languages}
            itemLabel={(c) => langLabel(c as LangCode)}
            min={1}
            onChange={(next) => change(withLanguages(values, next as LangCode[]))}
            choices={() => langChoices(languages)}
            onAdd={(c) => change(withLanguages(values, [...languages, c as LangCode]))}
            addLabel={t('comp.langs.add')}
          />
        </ListRow>
        {languages.includes('ru') && (
          <ListRow
            wide
            label={t('settings.studios')}
            hint={t('settings.studios.hint')}
            id="studios"
          >
            <StudioList labelledBy="studios" studios={values.studios ?? []} onChange={(studios) => change({ studios })} />
          </ListRow>
        )}
      </GroupedSection>
      )}

      {at('music') && (
      <GroupedSection>
        <ListRow stack label={t('settings.remoteMusic')} hint={t('settings.remoteMusic.hint')}>
          <SegmentedControl
            label={t('settings.remoteMusic')}
            segments={[
              { value: 'lossless', label: t('settings.remoteMusic.lossless') },
              { value: 'high', label: t('settings.remoteMusic.high') },
              { value: 'low', label: t('settings.remoteMusic.low') },
            ]}
            value={values.remoteMusic ?? 'high'}
            onChange={(v) => change({ remoteMusic: v as Values['remoteMusic'] })}
          />
        </ListRow>
      </GroupedSection>
      )}

      {at('subtitles') && (
      <GroupedSection>
        <ListRow
          label={t('settings.autoSubtitles')}
          hint={values.subtitlesAvailable ? t('settings.autoSubtitles.hint', { app: APP_NAME }) : t('settings.autoSubtitles.noKey')}
        >
          <Toggle
            label={t('settings.autoSubtitles')}
            checked={Boolean(values.subtitlesAvailable && values.autoSubtitles)}
            disabled={!values.subtitlesAvailable}
            onChange={(on) => change({ autoSubtitles: on })}
          />
        </ListRow>
        <ListRow stack label={t('settings.subtitleLangs')} hint={t('settings.subtitleLangs.hint', { app: APP_NAME })}>
          <MultiSelect
            label={t('settings.subtitleLangs')}
            placement="bottom-end"
            options={mine}
            value={languages.filter((c) => (values.subtitleLangs ?? DEFAULT_LANGS).includes(c))}
            min={1}
            onChange={(v) => change({ subtitleLangs: v })}
          />
        </ListRow>
      </GroupedSection>
      )}

      {at('kindle') && (
        <GroupedSection>
          <MailRow values={values} saved={saved} error={error} onChange={(mail) => change({ mail })} />
        </GroupedSection>
      )}
      {at('kindle') && <TelegramSection />}

      {at('processing') && <TranscodingSection title={t('settings.group.transcoding')} />}

      {at('processing') && (
        <GroupedSection title={t('settings.trickplayGroup')}>
          <ListRow label={t('settings.trickplay')} hint={t('settings.trickplay.hint')}>
            <Toggle label={t('settings.trickplay')} checked={values.trickplay ?? true} onChange={(on) => change({ trickplay: on })} />
          </ListRow>
          {(values.trickplay ?? true) && (
            <ListRow stack label={t('settings.trickplayBudget')} hint={t('settings.trickplayBudget.hint')}>
              <SegmentedControl
                label={t('settings.trickplayBudget')}
                segments={TRICKPLAY_BUDGETS.map((gb) => ({ value: String(gb), label: t('settings.trickplayBudget.gb', { n: gb }) }))}
                value={String(values.trickplayBudgetGB ?? 10)}
                onChange={(v) => change({ trickplayBudgetGB: Number(v) })}
              />
            </ListRow>
          )}
        </GroupedSection>
      )}

      {at('ai') && <AISettingsSection ai={values.ai} keySet={values.aiKeySet ?? false} fromEnv={values.aiFromEnv} onChange={(ai) => change({ ai })} />}

      {at('processing') && <VocleanSection />}

      {at('accounts') && <AccountsSection />}

      {at('address') && <PublicAddressSection values={values} onChange={(patch) => change(patch)} />}

      {at('external') && <ExternalServicesSection />}

      {at('library') && <StorageSection />}

      {at('library') && (
        <GroupedSection title={t('settings.rescanGroup')}>
          <Rescan />
        </GroupedSection>
      )}
    </section>
  )
}

/**
 * «Аккаунты и синхронизация» of the server: what applies to every profile
 * (Shikimori's application, which lets marks go back there) and the way to
 * the admin's own accounts, which are a profile's settings like anyone's.
 */
function AccountsSection() {
  const t = useT()
  const navigate = useNavigate()
  return (
    <>
      <ShikimoriAdmin />
      <GroupedSection>
        <ListLinkRow icon="link" tint="cyan" label={t('acct.admin.mine')} hint={t('acct.admin.mine.hint')} onOpen={() => navigate('/settings/accounts')} />
      </GroupedSection>
    </>
  )
}

/**
 * «Почта для отправки»: one row like the others — whether the mailbox is set
 * up (and which) and «Настроить…»; the steps and the SMTP fields are in a
 * sheet, set up once and rarely seen again.
 */
function MailRow({ values, saved, error, onChange }: { values: Values; saved: '' | 'saving' | 'saved'; error: string; onChange: (m: MailSettings) => void }) {
  const t = useT()
  const [open, setOpen] = useState(false)
  const mail = values.mail
  const ready = Boolean(mail?.host && mail.from && (!mail.username || values.mailPasswordSet))
  // not set up yet: the steps are what is needed first
  const [guide, setGuide] = useState(!ready)
  // a field saves when it is left: leave it before the sheet goes (Enter in a
  // field runs «Готово», and an unmounted field never blurs)
  const close = () => {
    if (document.activeElement instanceof HTMLElement) document.activeElement.blur()
    setOpen(false)
  }
  return (
    <ListRow label={t('settings.mail')} hint={t('settings.mail.hint')} status={ready ? t('settings.mail.ready', { from: mail!.from }) : t('settings.mail.notReady')}>
      <Button size="small" variant="secondary" onClick={() => setOpen(true)}>
        {t('settings.mail.configure')}
      </Button>
      {open && (
        <Sheet
          title={t('settings.mailGroup')}
          role="dialog"
          size="wide"
          onCancel={() => close()}
          closeButton={false}
          actions={[{ label: t('common.done'), kind: 'primary', run: () => close() }]}
        >
          <div className={styles.mailBlock}>
            {/* the fields first; the long how-to only on demand (already set up: rarely needed) */}
            {guide ? (
              <MailGuide />
            ) : (
              <Button size="small" variant="plain" icon="info" onClick={() => setGuide(true)}>
                {t('settings.mail.showGuide')}
              </Button>
            )}
            <MailFields mail={mail} passwordSet={values.mailPasswordSet ?? false} onChange={onChange} saved={saved} error={error} />
          </div>
        </Sheet>
      )}
    </ListRow>
  )
}

/** What to do, in order: the owner of the server sets this up once, outside the app too. */
function MailGuide() {
  const t = useT()
  const out = (href: string, label: string) => (
    <a href={href} target="_blank" rel="noopener noreferrer">
      {label}
    </a>
  )
  return (
    <ol className={styles.mailGuide} aria-label={t('settings.mail.guide')}>
      <li>
        {t('settings.mail.step1')} {out('https://myaccount.google.com/apppasswords', t('settings.mail.step1Link'))}
      </li>
      <li>{t('settings.mail.step2')}</li>
      <li>
        {t('settings.mail.step3')} {out('https://www.amazon.com/myk', t('settings.mail.step3Link'))}
      </li>
      <li>
        {t('settings.mail.step4')} <a href="/manage/profiles">{t('settings.mail.step4Link')}</a>
      </li>
      <li>{t('settings.mail.step5')}</li>
    </ol>
  )
}

/**
 * The SMTP account of «Отправить на Kindle». Saved when a field is left, not
 * on every key: half an address is not worth an error. The stored password
 * never comes back, so its field stays empty and an empty one keeps it.
 */
function MailFields({ mail, passwordSet, onChange, saved: saveState, error }: { mail?: MailSettings; passwordSet: boolean; onChange: (m: MailSettings) => void; saved: '' | 'saving' | 'saved'; error: string }) {
  const t = useT()
  const [draft, setDraft] = useState<MailSettings>({ host: mail?.host ?? '', port: mail?.port ?? 587, username: mail?.username ?? '', from: mail?.from ?? '', password: '' })
  const saved = useRef(draft)
  // the screen's own save line is a long way up from here
  const [touched, setTouched] = useState(false)
  const commit = () => {
    // leaving a field untouched saves nothing
    if (JSON.stringify(saved.current) === JSON.stringify(draft)) return
    saved.current = { ...draft, password: '' }
    setTouched(true)
    onChange(draft)
  }
  const field = (patch: Partial<MailSettings>) => setDraft((d) => ({ ...d, ...patch }))
  return (
    <div className={styles.mailFields}>
      <TextField label={t('settings.mail.host')} placeholder="smtp.gmail.com" autoComplete="off" spellCheck={false} value={draft.host} onChange={(e) => field({ host: e.target.value })} onBlur={() => commit()} />
      <TextField label={t('settings.mail.port')} type="number" min={1} max={65535} inputMode="numeric" value={draft.port || ''} onChange={(e) => field({ port: Number(e.target.value) })} onBlur={() => commit()} />
      <TextField label={t('settings.mail.username')} autoComplete="off" spellCheck={false} value={draft.username} onChange={(e) => field({ username: e.target.value })} onBlur={() => commit()} />
      <TextField
        label={t('settings.mail.password')}
        hint={passwordSet ? t('settings.mail.passwordSet') : t('settings.mail.passwordHint')}
        type="password"
        revealable
        autoComplete="new-password"
        value={draft.password ?? ''}
        onChange={(e) => field({ password: e.target.value })}
        onBlur={() => {
          if (!draft.password) return
          commit()
          field({ password: '' })
        }}
      />
      <TextField label={t('settings.mail.from')} hint={t('settings.mail.fromHint')} type="email" autoComplete="off" spellCheck={false} value={draft.from} onChange={(e) => field({ from: e.target.value })} onBlur={() => commit()} />
      {touched && (error || saveState) && (
        <p className={error ? styles.mailError : m.rowHint} role={error ? 'alert' : 'status'}>
          {error || (saveState === 'saving' ? t('settings.mail.saving') : t('settings.mail.saved'))}
        </p>
      )}
    </div>
  )
}

function Rescan() {
  const [status, setStatus] = useState<LibraryStatus | null>(null)
  const [error, setError] = useState('')
  const [started, setStarted] = useState(false)
  // Polls only from «Начать» until the scan has finished.
  const [polling, setPolling] = useState(false)
  const running = Boolean(status && (status.running || status.queued))
  const t = useT()

  useEffect(() => {
    let live = true
    getLibraryStatus().then((s) => live && setStatus(s), () => {})
    return () => void (live = false)
  }, [])
  // the empty pages of the core build link here (RescanLink): the row is last on a long page
  useEffect(() => {
    if (window.location.hash === '#rescan') document.getElementById('rescan')?.scrollIntoView?.({ block: 'center' })
  }, [])
  useEffect(() => {
    if (!polling) return
    let live = true
    const id = window.setInterval(() => {
      getLibraryStatus().then(
        (s) => {
          if (!live) return
          setStatus(s)
          if (!s.running && !s.queued) setPolling(false)
        },
        () => {},
      )
    }, 1500)
    return () => {
      live = false
      window.clearInterval(id)
    }
  }, [polling])

  const run = async () => {
    setError('')
    try {
      await scanLibrary()
      setStarted(true)
      setStatus((s) => (s ? { ...s, queued: true } : s))
      setPolling(true)
    } catch (e) {
      setError(errorText(e, t('settings.rescan.startFailed')))
    }
  }

  let hint = t('settings.rescan.hint')
  if (running) hint = t('settings.rescan.running')
  else if (started && status) {
    const counts = [
      t('settings.rescan.movies', { count: status.movies }),
      t('settings.rescan.episodes', { count: status.episodes }),
      ...(status.albums ? [t('settings.rescan.albums', { count: status.albums })] : []),
      t('settings.rescan.books', { count: status.books }),
      ...(status.audiobooks ? [t('settings.rescan.audiobooks', { count: status.audiobooks })] : []),
      ...(status.comics ? [t('settings.rescan.comics', { count: status.comics })] : []),
    ]
    hint = t('settings.rescan.done', { list: counts.join(', ') })
  }
  if (error) hint = error
  return (
    <ListRow id="rescan" label={t('settings.rescan')} hint={hint}>
      <Button size="small" variant="secondary" icon="restart" loading={running} disabled={running} onClick={() => void run()}>
        {running ? t('settings.rescan.inProgress') : t('settings.rescan.start')}
      </Button>
      {started && !running && !error && <Icon name="checkCircle" size={20} className={m.ok} />}
    </ListRow>
  )
}
