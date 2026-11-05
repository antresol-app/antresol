// «Экранное время» of a kid profile (an administrator's, in «Профили»):
// today's time by title, the last seven days as bars, the limits (minutes
// a day for video and, separately, music and audiobooks; the allowed hours
// on weekdays and at weekends) and «Бонус на сегодня +30 мин». The bonus
// is given at once; the limits on «Сохранить».
import { useEffect, useId, useState } from 'react'
import { addScreenBonus, ApiError, getScreenTime, saveScreenLimits } from '../api/client'
import type { ScreenLimits, ScreenReport, ScreenWindow } from '../api/types'
import { humanMinutes } from '../player/timeline'
import { locale, t as tr, useT } from '../i18n'
import { Button } from '../ui/Button'
import { Sheet } from '../ui/Sheet'
import { Toggle } from '../ui/Toggle'
import styles from './ScreenTimeSheet.module.css'

export interface ScreenTimeSheetProps {
  userId: number
  name: string
  onClose: () => void
}

/** The bonus of the button. */
export const BONUS_MIN = 30

const DEFAULT_WINDOW: ScreenWindow = { from: '08:00', to: '20:30' }

/** "1 ч 5 мин" of seconds ("0 мин" for nothing). */
export function spent(sec: number): string {
  const m = Math.round(sec / 60)
  return m <= 0 ? tr('player.time.min', { m: 0 }) : humanMinutes(m)
}

/** The weekday of a YYYY-MM-DD day, short, in the interface language. */
function weekday(day: string): string {
  const [y, m, d] = day.split('-').map(Number)
  return new Intl.DateTimeFormat(locale(), { weekday: 'short' }).format(new Date(y!, (m ?? 1) - 1, d ?? 1))
}

type Draft = {
  video: string
  videoOn: boolean
  audio: string
  audioOn: boolean
  weekday: ScreenWindow | null
  weekend: ScreenWindow | null
  dayStart: string
}

const draftOf = (l: ScreenLimits): Draft => ({
  video: String(l.videoMin ?? 60),
  videoOn: l.videoMin !== null,
  audio: String(l.audioMin ?? 60),
  audioOn: l.audioMin !== null,
  weekday: l.weekday,
  weekend: l.weekend,
  dayStart: l.dayStart || '04:00',
})

const minutesOf = (s: string): number | null => {
  const n = Number(s)
  return s.trim() !== '' && Number.isInteger(n) && n >= 0 && n <= 1440 ? n : null
}

export function ScreenTimeSheet({ userId, name, onClose }: ScreenTimeSheetProps) {
  const t = useT()
  const id = useId()
  const [report, setReport] = useState<ScreenReport | null>(null)
  const [draft, setDraft] = useState<Draft | null>(null)
  const [loadError, setLoadError] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    const ac = new AbortController()
    getScreenTime(userId, ac.signal).then(
      (r) => {
        setReport(r)
        setDraft(draftOf(r.limits))
      },
      (e: unknown) => !ac.signal.aborted && setLoadError(e instanceof ApiError ? e.message : t('comp.screen.loadFailed')),
    )
    return () => ac.abort()
  }, [userId, t])

  const change = (patch: Partial<Draft>) => setDraft((d) => (d ? { ...d, ...patch } : d))
  const videoMin = draft ? minutesOf(draft.video) : null
  const audioMin = draft ? minutesOf(draft.audio) : null
  const invalid = !!draft && ((draft.videoOn && videoMin === null) || (draft.audioOn && audioMin === null))

  const save = async () => {
    if (!draft || invalid) return
    setBusy(true)
    setError('')
    try {
      await saveScreenLimits(userId, {
        videoMin: draft.videoOn ? videoMin : null,
        audioMin: draft.audioOn ? audioMin : null,
        weekday: draft.weekday,
        weekend: draft.weekend,
        dayStart: draft.dayStart,
      })
      onClose()
    } catch (e) {
      setError(e instanceof ApiError ? e.message : t('comp.screen.saveFailed'))
    } finally {
      setBusy(false)
    }
  }

  const bonus = async () => {
    setBusy(true)
    setError('')
    try {
      setReport(await addScreenBonus(userId, BONUS_MIN))
    } catch (e) {
      setError(e instanceof ApiError ? e.message : t('comp.screen.saveFailed'))
    } finally {
      setBusy(false)
    }
  }

  const v = report?.status.video
  const max = Math.max(60, ...(report?.days ?? []).map((d) => d.videoSec + d.audioSec))
  const hours = (key: 'weekday' | 'weekend', label: string) => {
    if (!draft) return null
    const w = draft[key]
    return (
      <div className={styles.limit}>
        <div className={styles.limitHead}>
          <span id={`${id}-${key}`}>{label}</span>
          <Toggle label={`${label}: ${t('comp.screen.hoursOn')}`} checked={w !== null} onChange={(on) => change({ [key]: on ? DEFAULT_WINDOW : null })} />
        </div>
        {w && (
          <div className={styles.times} role="group" aria-labelledby={`${id}-${key}`}>
            <label className={styles.time}>
              <span>{t('comp.screen.from')}</span>
              <input type="time" value={w.from} required onChange={(e) => e.target.value && change({ [key]: { ...w, from: e.target.value } })} />
            </label>
            <label className={styles.time}>
              <span>{t('comp.screen.to')}</span>
              <input type="time" value={w.to} required onChange={(e) => e.target.value && change({ [key]: { ...w, to: e.target.value } })} />
            </label>
          </div>
        )}
      </div>
    )
  }
  const minutes = (on: boolean, value: string, valid: boolean, label: string, set: (p: Partial<Draft>) => void, k: 'video' | 'audio') => (
    <div className={styles.limit}>
      <div className={styles.limitHead}>
        <span>{label}</span>
        <Toggle label={`${label}: ${t('comp.screen.limitOn')}`} checked={on} onChange={(x) => set({ [`${k}On`]: x })} />
      </div>
      {on && (
        <label className={styles.time}>
          <span>{t('comp.screen.minutes')}</span>
          <input
            type="number"
            min={0}
            max={1440}
            step={5}
            inputMode="numeric"
            value={value}
            aria-invalid={!valid}
            onChange={(e) => set({ [k]: e.target.value })}
          />
          {!valid && <span className={styles.bad}>{t('comp.screen.badMinutes')}</span>}
        </label>
      )}
    </div>
  )

  return (
    <Sheet
      title={t('comp.screen.titleFor', { name })}
      role="dialog"
      size="wide"
      busy={busy}
      onCancel={onClose}
      actions={[
        { label: t('common.cancel'), kind: 'cancel', run: onClose },
        { label: t('common.save'), kind: 'primary', disabled: !draft || invalid, run: () => void save() },
      ]}
    >
      <div className={styles.form}>
        {!report && !loadError && (
          <p className={styles.hint} role="status">
            {t('common.loading')}
          </p>
        )}
        {loadError && (
          <p className={styles.error} role="alert">
            {loadError}
          </p>
        )}
        {report && report.passwordless.length > 0 && (
          <p className={styles.warning} role="note">
            {t('comp.screen.passwordless', { names: report.passwordless.join(', ') })}
          </p>
        )}
        {report && v && (
          <section className={styles.section} aria-labelledby={`${id}-today`}>
            <h3 className={styles.label} id={`${id}-today`}>
              {t('comp.screen.today')}
            </h3>
            <p className={styles.big}>
              {v.limitMin !== null
                ? t('comp.screen.usedOf', { used: spent(v.usedSec), limit: humanMinutes(v.limitMin + v.bonusMin) || spent(0) })
                : t('comp.screen.used', { used: spent(v.usedSec) })}
            </p>
            {v.limitMin !== null && (
              <div className={styles.meter} aria-hidden>
                <span style={{ width: `${Math.min(100, (v.usedSec / Math.max(60, (v.limitMin + v.bonusMin) * 60)) * 100)}%` }} />
              </div>
            )}
            {report.status.audio && <p className={styles.hint}>{t('comp.screen.listened', { used: spent(report.status.audio.usedSec) })}</p>}
            {v.message && <p className={styles.hint}>{v.message}</p>}
            <div className={styles.row}>
              <Button size="small" variant="secondary" icon="plus" disabled={busy} onClick={() => void bonus()}>
                {t('comp.screen.bonus')}
              </Button>
              {v.bonusMin > 0 && <span className={styles.hint}>{t('comp.screen.bonusGiven', { time: humanMinutes(v.bonusMin) })}</span>}
            </div>
            {report.titles.length === 0 ? (
              <p className={styles.hint}>{t('comp.screen.nothing')}</p>
            ) : (
              <ul className={styles.titles}>
                {report.titles.map((x) => (
                  <li key={`${x.category}-${x.itemId}`} className={styles.titleRow}>
                    <span className={styles.titleName}>{x.title || '—'}</span>
                    <span className={styles.hint}>{spent(x.seconds)}</span>
                  </li>
                ))}
              </ul>
            )}
          </section>
        )}
        {report && (
          <section className={styles.section} aria-labelledby={`${id}-week`}>
            <h3 className={styles.label} id={`${id}-week`}>
              {t('comp.screen.week')}
            </h3>
            <ol className={styles.chart}>
              {report.days.map((d) => {
                const sec = d.videoSec + d.audioSec
                return (
                  <li key={d.day} className={styles.bar} aria-label={t('comp.screen.weekAria', { day: weekday(d.day), time: spent(sec) })}>
                    <span className={styles.barTrack} aria-hidden>
                      <span className={styles.barFill} style={{ height: `${(sec / max) * 100}%` }} />
                    </span>
                    <span className={styles.barDay} aria-hidden>
                      {weekday(d.day)}
                    </span>
                  </li>
                )
              })}
            </ol>
          </section>
        )}
        {draft && (
          <section className={styles.section} aria-labelledby={`${id}-limits`}>
            <h3 className={styles.label} id={`${id}-limits`}>
              {t('comp.screen.limits')}
            </h3>
            {minutes(draft.videoOn, draft.video, videoMin !== null, t('comp.screen.video'), change, 'video')}
            {minutes(draft.audioOn, draft.audio, audioMin !== null, t('comp.screen.audio'), change, 'audio')}
            {hours('weekday', t('comp.screen.weekday'))}
            {hours('weekend', t('comp.screen.weekend'))}
            <div className={styles.limit}>
              <label className={styles.time}>
                <span>{t('comp.screen.dayStart')}</span>
                <input type="time" value={draft.dayStart} required onChange={(e) => e.target.value && change({ dayStart: e.target.value })} />
              </label>
              <p className={styles.hint}>{t('comp.screen.dayStartHint')}</p>
            </div>
            <p className={styles.hint}>{t('comp.screen.note')}</p>
          </section>
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
