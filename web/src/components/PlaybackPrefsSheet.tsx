// «Звук и субтитры»: a profile's audio and subtitle defaults, kept on the
// server so they follow the profile to every browser and the app. The
// player uses them when nothing more specific applies (the tracks chosen
// in a series come first). An adult opens it from the profile menu; a
// kid's is the administrator's (Профили), as «Скрывать прогресс» is.
import { useEffect, useId, useState } from 'react'
import { ApiError, getPlaybackPrefs, resetPlaybackPrefs, savePlaybackPrefs, setPreferCleaned } from '../api/client'
import type { LangCode, PlaybackPrefs, SubBackground, SubMode, SubSize } from '../api/types'
import { useT } from '../i18n'
import look from '../player/SubtitleLook.module.css'
import { Button } from '../ui/Button'
import { langChoices, langLabel } from './langs'
import { RankedList } from './RankedList'
import { SegmentedControl } from '../ui/SegmentedControl'
import { Select } from '../ui/Select'
import { Sheet } from '../ui/Sheet'
import { Toggle } from '../ui/Toggle'
import styles from './PlaybackPrefsSheet.module.css'

export interface PlaybackPrefsSheetProps {
  /** Another profile's (an administrator in «Профили»); none: the signed-in one. */
  userId?: number
  /** That profile's name, for the title. */
  name?: string
  onClose: () => void
}

const SUB_MODES: SubMode[] = ['off', 'foreign', 'always']
const SIZES: SubSize[] = ['s', 'm', 'l', 'xl']
const BACKGROUNDS: SubBackground[] = ['none', 'translucent', 'solid']

type State = { status: 'loading' } | { status: 'error'; message: string } | { status: 'ready'; prefs: PlaybackPrefs }

export function PlaybackPrefsSheet({ userId, name, onClose }: PlaybackPrefsSheetProps) {
  const t = useT()
  const id = useId()
  const [state, setState] = useState<State>({ status: 'loading' })
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    const ac = new AbortController()
    getPlaybackPrefs(userId, ac.signal).then(
      (prefs) => setState({ status: 'ready', prefs }),
      (e: unknown) => !ac.signal.aborted && setState({ status: 'error', message: e instanceof ApiError ? e.message : t('comp.playback.loadFailed') }),
    )
    return () => ac.abort()
  }, [userId, t])

  const prefs = state.status === 'ready' ? state.prefs : null
  const change = (patch: Partial<PlaybackPrefs>) => setState((s) => (s.status === 'ready' ? { status: 'ready', prefs: { ...s.prefs, ...patch } } : s))

  const run = async (op: () => Promise<PlaybackPrefs>, close: boolean) => {
    setBusy(true)
    setError('')
    try {
      const p = await op()
      if (close) onClose()
      else setState({ status: 'ready', prefs: p })
    } catch (e) {
      setError(e instanceof ApiError ? e.message : t('comp.playback.saveFailed'))
    } finally {
      setBusy(false)
    }
  }
  const save = () => {
    if (!prefs) return
    const { custom: _, preferCleaned: __, ...input } = prefs
    void run(() => savePlaybackPrefs(input, userId), true)
  }
  // «Выбирать очищенную озвучку» is saved as it flips, apart from the rest
  // (which stays as edited here, saved or not)
  const [cleanedBusy, setCleanedBusy] = useState(false)
  const toggleCleaned = async (on: boolean) => {
    setCleanedBusy(true)
    setError('')
    change({ preferCleaned: on })
    try {
      const p = await setPreferCleaned(on, userId)
      change({ preferCleaned: Boolean(p.preferCleaned) })
    } catch (e) {
      change({ preferCleaned: !on })
      setError(e instanceof ApiError ? e.message : t('comp.playback.saveFailed'))
    } finally {
      setCleanedBusy(false)
    }
  }

  return (
    <Sheet
      title={name ? t('comp.playback.titleFor', { name }) : t('comp.playback.title')}
      role="dialog"
      size="wide"
      busy={busy}
      onCancel={onClose}
      actions={[
        { label: t('common.cancel'), kind: 'cancel', run: onClose },
        { label: t('common.save'), kind: 'primary', disabled: !prefs || prefs.audioLangs.length === 0, run: save },
      ]}
    >
      <div className={styles.form}>
        <p className={styles.hint}>{t('comp.playback.intro')}</p>
        {state.status === 'loading' && (
          <p className={styles.hint} role="status">
            {t('common.loading')}
          </p>
        )}
        {state.status === 'error' && (
          <p className={styles.error} role="alert">
            {state.message}
          </p>
        )}
        {prefs && (
          <>
            <section className={styles.section} aria-labelledby={`${id}-audio`}>
              <h3 className={styles.label} id={`${id}-audio`}>
                {t('comp.playback.audio')}
              </h3>
              <p className={styles.hint}>{t('comp.playback.audioHint')}</p>
              <RankedList
                labelledBy={`${id}-audio`}
                items={prefs.audioLangs}
                itemLabel={(c) => langLabel(c as LangCode)}
                min={1}
                onChange={(audioLangs) => change({ audioLangs: audioLangs as LangCode[] })}
                choices={() => langChoices(prefs.audioLangs)}
                onAdd={(c) => change({ audioLangs: [...prefs.audioLangs, c as LangCode] })}
                addLabel={t('comp.langs.add')}
              />
              <div className={styles.switchRow}>
                <div className={styles.switchText}>
                  <span aria-hidden>{t('comp.playback.preferCleaned')}</span>
                  <span className={styles.hint} id={`${id}-cleaned-hint`}>
                    {t('comp.playback.preferCleanedHint')}
                  </span>
                </div>
                <Toggle
                  label={t('comp.playback.preferCleaned')}
                  checked={Boolean(prefs.preferCleaned)}
                  describedBy={`${id}-cleaned-hint`}
                  disabled={cleanedBusy}
                  onChange={(on) => void toggleCleaned(on)}
                />
              </div>
            </section>
            <section className={styles.section}>
              <div className={styles.row}>
                <h3 className={styles.label}>
                  {t('comp.playback.subMode')}
                </h3>
                <Select
                  label={t('comp.playback.subMode')}
                  placement="bottom-end"
                  value={prefs.subMode}
                  options={SUB_MODES.map((m) => ({ value: m, label: t(`comp.playback.subMode.${m}`) }))}
                  onChange={(subMode) => change({ subMode })}
                />
              </div>
              <p className={styles.hint}>{t('comp.playback.subModeHint')}</p>
            </section>
            {prefs.subMode !== 'off' && (
              <section className={styles.section} aria-labelledby={`${id}-subs`}>
                <h3 className={styles.label} id={`${id}-subs`}>
                  {t('comp.playback.subLangs')}
                </h3>
                <p className={styles.hint}>{t('comp.playback.subLangsHint')}</p>
                <RankedList
                  labelledBy={`${id}-subs`}
                  items={prefs.subLangs}
                  itemLabel={(c) => langLabel(c as LangCode)}
                  onChange={(subLangs) => change({ subLangs: subLangs as LangCode[] })}
                  choices={() => langChoices(prefs.subLangs)}
                  onAdd={(c) => change({ subLangs: [...prefs.subLangs, c as LangCode] })}
                  addLabel={t('comp.langs.add')}
                  empty={t('comp.playback.subLangsNone')}
                />
              </section>
            )}
            <section className={styles.section} aria-labelledby={`${id}-style`}>
              <h3 className={styles.label} id={`${id}-style`}>
                {t('comp.playback.style')}
              </h3>
              <div className={styles.preview} data-size={prefs.subSize} data-bg={prefs.subBackground} aria-hidden>
                <span className={look.cue}>{t('comp.playback.preview')}</span>
              </div>
              <div className={styles.row}>
                <span className={styles.rowLabel} aria-hidden>
                  {t('comp.playback.size')}
                </span>
                <SegmentedControl
                  label={t('comp.playback.size')}
                  segments={SIZES.map((s) => ({ value: s, label: t(`comp.playback.sizeAria.${s}`), short: t(`comp.playback.size.${s}`) }))}
                  value={prefs.subSize}
                  onChange={(subSize) => change({ subSize })}
                />
              </div>
              <div className={styles.row}>
                <span className={styles.rowLabel} aria-hidden>
                  {t('comp.playback.background')}
                </span>
                <SegmentedControl
                  label={t('comp.playback.background')}
                  segments={BACKGROUNDS.map((b) => ({ value: b, label: t(`comp.playback.background.${b}`) }))}
                  value={prefs.subBackground}
                  onChange={(subBackground) => change({ subBackground })}
                />
              </div>
              <p className={styles.hint}>{t('comp.playback.styleHint')}</p>
            </section>
            <div className={styles.footer}>
              <p className={styles.hint} role="status">
                {prefs.custom ? '' : t('comp.playback.household')}
              </p>
              {prefs.custom && (
                <Button size="small" variant="secondary" disabled={busy} onClick={() => void run(() => resetPlaybackPrefs(userId), false)}>
                  {t('comp.playback.reset')}
                </Button>
              )}
            </div>
          </>
        )}
        {error && (
          <p className={styles.error} role="alert">
            {error}
          </p>
        )}
      </div>
    </Sheet>
  )
}
