// «Настройки»: one row per setting, each with a plain label. Changes save
// by themselves (like iOS); «Качество вне дома» (and the sources build's upload
// limit and cache size) apply at once on the server. The core build
// has only the library's settings: no release preferences, online viewing
// or sharing (docs/build-tags.md).
import { APP_NAME } from '../../brand'
import { useCallback, useEffect, useRef, useState } from 'react'
import { getLibraryStatus, getSettings, getTorrentEngine, onlineStatus, saveSettings, scanLibrary, errorText } from '../../api/client'
import type { LangCode, LibraryStatus, MailSettings, OnlineStatus, Settings as Values, TorrentEngineStatus, Voice } from '../../api/types'
import { Button } from '../../ui/Button'
import { Sheet } from '../../ui/Sheet'
import { langChoices, langLabel } from '../../components/langs'
import { Icon } from '../../components/Icon'
import { RankedList } from '../../components/RankedList'
import { SegmentedControl } from '../../ui/SegmentedControl'
import { MultiSelect, Select } from '../../ui/Select'
import { StudioList } from '../../components/StudioList'
import { TextField } from '../../ui/TextField'
import { Toggle } from '../../ui/Toggle'
import { GroupedSection, ListRow } from '../../ui/List'
import { NumberField } from '../../ui/NumberField'
import { getLanguagePref, t, useT, type LangPref } from '../../i18n'
import { chooseInterfaceLanguage } from '../../state/uiLanguage'
import m from './Manage.module.css'
import styles from './Settings.module.css'

/** Slider stops for the movie size limit, GB; the last one means no limit. */
export const SIZE_STOPS = [2, 4, 6, 8, 10, 15, 20, 30, 40, 60, 80, 0]

export function sizeStop(gb: number): number {
  if (gb <= 0) return SIZE_STOPS.length - 1
  let best = 0
  for (let i = 0; i < SIZE_STOPS.length - 1; i++) if (Math.abs(SIZE_STOPS[i]! - gb) < Math.abs(SIZE_STOPS[best]! - gb)) best = i
  return best
}

const sizeLabel = (gb: number) => (gb <= 0 ? t('settings.size.noLimit') : t('settings.size.upTo', { gb }))

const DEFAULT_LANGS: LangCode[] = ['ru', 'en']

/**
 * «Языки» changed: every other choice keeps only these, in their order
 * (the server does the same); subtitles keep at least the first, «Озвучка»
 * keeps the original without Russian.
 */
export function withLanguages(v: Values, languages: LangCode[]): Partial<Values> {
  const keep = (l: LangCode[] | undefined) => languages.filter((c) => (l ?? []).includes(c))
  const subs = keep(v.subtitleLangs)
  let voices = (v.voices ?? [v.voice]).filter((x) => x !== 'dub' || languages.includes('ru'))
  if (voices.length === 0) voices = ['original']
  const out: Partial<Values> = { languages, subtitleLangs: subs.length ? subs : [languages[0]!] }
  if (__LIBRARY_EDITION__) return out
  return {
    ...out,
    bookLangs: keep(v.bookLangs),
    audiobookLangs: keep(v.audiobookLangs),
    comicLangs: keep(v.comicLangs),
    voices,
    voice: voices[0]!,
  }
}

/** «Место для превью» choices, GB. */
const TRICKPLAY_BUDGETS = [5, 10, 20, 50]

export function Settings() {
  type MusicFormat = NonNullable<Values['musicFormat']>
  const [values, setValues] = useState<Values | null>(null)
  const [error, setError] = useState('')
  const [saved, setSaved] = useState<'' | 'saving' | 'saved'>('')
  const timer = useRef<number | undefined>(undefined)
  const latest = useRef<Values | null>(null)
  const t = useT()

  useEffect(() => {
    document.title = `${t('common.settings')} — ${APP_NAME}`
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
    timer.current = window.setTimeout(() => void flush(), wait)
  }
  useEffect(() => () => window.clearTimeout(timer.current), [])

  if (!values) {
    return (
      <section>
        <h2 className={m.title}>{t('common.settings')}</h2>
        {error ? (
          <div className={m.problem} role="alert" style={{ marginTop: 16 }}>
            {error}
          </div>
        ) : (
          <div className={m.spinnerRow} role="status">
            <span className={m.spinner} aria-hidden /> {t('settings.loading')}
          </div>
        )}
      </section>
    )
  }

  const stop = sizeStop(values.maxSizeGB)
  const musicFormat: MusicFormat = values.musicFormat ?? (values.preferLossless === false ? 'mp3' : 'lossless')
  const mbs = values.uploadLimitKBs / 1024
  const languages = values.languages?.length ? values.languages : DEFAULT_LANGS
  const mine = languages.map((c) => ({ value: c, label: langLabel(c) }))
  const voices: Voice[] = values.voices?.length ? values.voices : [values.voice]
  const voiceOptions: { value: Voice; label: string }[] = __LIBRARY_EDITION__
    ? []
    : [...(languages.includes('ru') ? [{ value: 'dub' as const, label: t('settings.voice.dub') }] : []), { value: 'original', label: t('common.original') }]
  // the languages of one kind of media: a subset of «Мои языки», in their order
  const kindLangs = (key: 'bookLangs' | 'audiobookLangs' | 'comicLangs', label: string) => (
    <ListRow stack label={label} hint={__LIBRARY_EDITION__ ? '' : t('settings.kindLangs.hint')}>
      <MultiSelect
        label={label}
        placement="bottom-end"
        options={mine}
        value={languages.filter((c) => (values[key] ?? []).includes(c))}
        none={t('settings.kindLangs.none')}
        onChange={(v) => change({ [key]: v })}
      />
    </ListRow>
  )
  return (
    <section aria-labelledby="settings-title" className={styles.page}>
      <header className={m.header}>
        <div>
          <h2 id="settings-title" className={m.title}>
            {t('common.settings')}
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

      {/* one group: two separate cards would touch without a gap */}
      <GroupedSection>
        <InterfaceLanguage />
        {values.metadataLanguage !== undefined && (
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
        )}
      </GroupedSection>

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
      </GroupedSection>

      {/* the sources build's release preferences */}
      {!__LIBRARY_EDITION__ && (
        <>
          <GroupedSection title={t('settings.newVideo')}>
            <ListRow stack label={t('settings.videoQuality')} hint={t('settings.videoQuality.hint', { app: APP_NAME })}>
              <SegmentedControl
                label={t('settings.videoQuality')}
                segments={[
                  { value: '720', label: '720p' },
                  { value: '1080', label: '1080p' },
                  { value: '2160', label: '4K' },
                ]}
                value={String(values.quality)}
                onChange={(v) => change({ quality: Number(v) as Values['quality'] })}
              />
            </ListRow>
            <ListRow wide label={t('settings.voices')} hint={t('settings.voices.hint')} id="voices">
              <RankedList
                labelledBy="voices"
                items={voices}
                itemLabel={(v) => voiceOptions.find((o) => o.value === v)?.label ?? v}
                min={1}
                onChange={(next) => change({ voices: next as Voice[], voice: next[0] as Voice })}
                choices={() => voiceOptions.filter((o) => !voices.includes(o.value))}
                onAdd={(v) => change({ voices: [...voices, v as Voice], voice: voices[0] ?? (v as Voice) })}
                addLabel={t('settings.voices.add')}
              />
            </ListRow>
            <ListRow stack label={t('settings.maxSize')} hint={t('settings.maxSize.hint')}>
              {/* a dozen fixed sizes: a pop-up, not a slider that only looks continuous */}
              <Select
                label={t('settings.maxSize')}
                placement="bottom-end"
                value={String(SIZE_STOPS[stop])}
                options={SIZE_STOPS.map((gb) => ({ value: String(gb), label: sizeLabel(gb) }))}
                onChange={(v) => change({ maxSizeGB: Number(v) })}
              />
            </ListRow>
          </GroupedSection>
        </>
      )}

      <GroupedSection title={t('settings.watching')}>
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
        {languages.includes('ru') && (
          <ListRow
            wide
            label={t('settings.studios')}
            hint={t(__LIBRARY_EDITION__ ? 'settings.studios.hint' : 'settings.studios.hintFull')}
            id="studios"
          >
            <StudioList labelledBy="studios" studios={values.studios ?? []} onChange={(studios) => change({ studios })} />
          </ListRow>
        )}
        {!__LIBRARY_EDITION__ && <OnlineRow enabled={values.onlineEnabled ?? true} onChange={(on) => change({ onlineEnabled: on })} />}
      </GroupedSection>

      <GroupedSection title={t('common.music')}>
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
        {!__LIBRARY_EDITION__ && (
          <>
            <ListRow
              stack
              label={t('settings.musicFormat')}
              hint={t('settings.musicFormat.hint')}
            >
              <SegmentedControl
                label={t('settings.musicFormat')}
                segments={[
                  { value: 'lossless', label: t('settings.musicFormat.lossless') },
                  { value: 'mp3', label: 'MP3' },
                  { value: 'any', label: t('settings.musicFormat.any') },
                ]}
                value={musicFormat}
                onChange={(v) => change({ musicFormat: v as MusicFormat, preferLossless: v === 'lossless' })}
              />
            </ListRow>
            {musicFormat === 'mp3' && (
              <ListRow stack label={t('settings.bitrate')} hint={t('settings.bitrate.hint')}>
                <Select
                  label={t('settings.bitrate')}
                  placement="bottom-end"
                  options={[
                    { value: '320', label: t('settings.bitrate.320') },
                    { value: 'v0', label: 'V0 (VBR)' },
                    { value: '256', label: t('settings.bitrate.256') },
                    { value: 'any', label: t('settings.bitrate.any') },
                  ]}
                  value={values.mp3Bitrate ?? '320'}
                  onChange={(v) => change({ mp3Bitrate: v as Values['mp3Bitrate'] })}
                />
              </ListRow>
            )}
          </>
        )}
      </GroupedSection>

      <GroupedSection title={t('common.subtitles')}>
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

      {/* another build's settings */}
      {!__LIBRARY_EDITION__ && (
        <>
          <GroupedSection title={t('settings.booksGroup')}>
            {kindLangs('bookLangs', t('settings.bookLangs'))}
            {kindLangs('audiobookLangs', t('settings.audiobookLangs'))}
            {kindLangs('comicLangs', t('settings.comicLangs'))}
          </GroupedSection>

          <GroupedSection title={t('settings.seedingGroup')}>
            <IncomingRow />
            <ListRow label={t('settings.uploadLimit')} hint={t('settings.uploadLimit.hint')}>
              {values.uploadLimited && <UploadLimitField mbs={mbs} onChange={(v) => change({ uploadLimitKBs: Math.round(v * 1024) }, 700)} />}
              <Toggle
                label={t('settings.uploadLimit.toggle')}
                checked={values.uploadLimited}
                onChange={(on) => change({ uploadLimited: on, uploadLimitKBs: values.uploadLimitKBs > 0 ? values.uploadLimitKBs : 2048 })}
              />
            </ListRow>
            <ListRow label={t('settings.streamCache')} hint={t('settings.streamCache.hint')}>
              <NumberField
                label={t('settings.streamCache.aria')}
                unit={t('common.unit.GB')}
                min={1}
                value={Math.round(values.streamCacheGB)}
                onChange={(v) => change({ streamCacheGB: v }, 700)}
              />
            </ListRow>
          </GroupedSection>
        </>
      )}

      <GroupedSection title={t('settings.mailGroup')}>
        <MailRow values={values} saved={saved} error={error} onChange={(mail) => change({ mail })} />
      </GroupedSection>

      <GroupedSection title={t('common.library')}>
        <Rescan />
      </GroupedSection>
    </section>
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
          actions={[{ label: t('common.done'), kind: 'primary', run: () => close() }]}
        >
          <div className={styles.mailBlock}>
            <MailGuide />
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

/** What /api/online/status says, as the status line under «Смотреть онлайн». */
/** What /api/online/status said (null: not known — loading, an older server, or an error). */
export function onlineLine(st: OnlineStatus | null): string {
  if (!st) return ''
  if (!st.configured) return t('settings.online.notConfigured')
  if (!st.reachable) return t('settings.online.unreachable')
  return t('settings.online.sources', { n: st.sources })
}

/**
 * «Смотреть онлайн» (admin): the switch and the sidecar's status. The status
 * says whether the server has online sources configured, whatever the
 * switch; it is read once, so saving the switch never races it. Without a
 * configuration the switch cannot be turned on, but a stored «on» can
 * still be turned off.
 */
function OnlineRow({ enabled, onChange }: { enabled: boolean; onChange: (on: boolean) => void }) {
  const [st, setSt] = useState<OnlineStatus | null>(null)
  useEffect(() => {
    const ac = new AbortController()
    onlineStatus(ac.signal).then(setSt, () => {})
    return () => ac.abort()
  }, [])
  const notConfigured = st !== null && !st.configured
  const t = useT()
  return (
    <ListRow label={t('common.watchOnline')} hint={t('settings.online.hint')} status={onlineLine(st)}>
      <Toggle label={t('common.watchOnline')} checked={enabled} disabled={notConfigured && !enabled} onChange={onChange} />
    </ListRow>
  )
}

/**
 * «Входящие подключения» (admin): only when the server sees that no peer
 * has ever connected in (GET /api/torrent-engine), with the port to open.
 * Read once; nothing while it is fine, unknown or from an older server.
 */
function IncomingRow() {
  const [st, setSt] = useState<TorrentEngineStatus | null>(null)
  const t = useT()
  useEffect(() => {
    const ac = new AbortController()
    getTorrentEngine(ac.signal).then(setSt, () => {})
    return () => ac.abort()
  }, [])
  if (!st?.incomingBlocked) return null
  return (
    <ListRow
      label={
        <>
          <Icon name="warning" size={16} className={styles.incomingIcon} /> {t('settings.incoming')}
        </>
      }
      status={t('settings.incoming.blocked', { port: st.listenPort })}
    />
  )
}

/** The upload limit in МБ/с (more than 0; a hundredth at the finest). */
export function UploadLimitField({ mbs, onChange }: { mbs: number; onChange: (v: number) => void }) {
  const t = useT()
  return (
    <NumberField
      label={t('settings.uploadLimit.aria')}
      unit={t('common.perSecond', { size: t('common.unit.MB') })}
      value={mbs}
      min={0.01}
      decimals={2}
      onChange={onChange}
    />
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

/**
 * «Язык интерфейса»: applies in this browser at once and is saved for the
 * profile, so its other apps follow (state/uiLanguage.ts); «Как в системе»
 * follows the profile's own language, else the browser's.
 */
function InterfaceLanguage() {
  const t = useT()
  const [pref, setPref] = useState<LangPref>(getLanguagePref)
  return (
    <ListRow stack label={t('common.uiLanguage')} hint={t('settings.uiLanguage.hint')}>
      <SegmentedControl
        label={t('common.uiLanguage')}
        segments={[
          { value: 'system', label: t('common.uiLanguage.system') },
          { value: 'ru', label: t('common.uiLanguage.ru') },
          { value: 'en', label: t('common.uiLanguage.en') },
        ]}
        value={pref}
        onChange={(v: LangPref) => {
          setPref(v)
          chooseInterfaceLanguage(v)
        }}
      />
    </ListRow>
  )
}
