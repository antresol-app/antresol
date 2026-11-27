// «Управление → Задачи» (admin): what the server does in the background, in
// one place — the library scan, the cleaned voice-overs, the scrub preview
// frames, the search for intros and credits, the loudness of the music, the
// audiobooks' transcripts —
// each with how much is done, whether it runs now (or why it stands still)
// and when it last ran. Updated while the page is open. The settings of
// each stay where they belong (a row opens them: «Медиатека», «Обработка»);
// this page only shows progress. One endpoint (/api/admin/tasks).
import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router'
import { APP_NAME } from '../../brand'
import { errorText, getTasks } from '../../api/client'
import type { LibraryStatus, TaskProgress, TasksView, VocleanStatus } from '../../api/types'
import { formatPercent, t as tr, useT, type Key } from '../../i18n'
import { ago } from '../../state/format'
import { usePolling } from '../../state/usePolling'
import { GroupedSection, ListLinkRow, ListRow } from '../../ui/List'
import { Notice } from '../../ui/Notice'
import { SettingsIcon, type SettingsGlyph, type SettingsTint } from '../../ui/SettingsIcon'
import { Spinner } from '../../ui/Spinner'
import m from './Manage.module.css'

/** How often the progress is read again while the page is open. */
export const TASKS_POLL_MS = 5000

type T = ReturnType<typeof useT>

/** A worker's name, tile and the settings screen it is set up on ('' none). */
const WORKERS: Record<string, { label: Key; glyph: SettingsGlyph; tint: SettingsTint; to: string }> = {
  trickplay: { label: 'tasks.trickplay', glyph: 'eye', tint: 'teal', to: '/manage/settings/processing' },
  segments: { label: 'tasks.segments', glyph: 'playRect', tint: 'red', to: '/manage/settings/processing' },
  loudness: { label: 'tasks.loudness', glyph: 'note', tint: 'pink', to: '' },
  transcripts: { label: 'tasks.transcripts', glyph: 'captions', tint: 'orange', to: '/manage/settings/ai' },
}

const percent = (done: number, total: number) => (total > 0 ? formatPercent(Math.floor((done / total) * 100)) : '')

/** A worker's line: «Готово 120 из 3400 · идёт сейчас», with what it is on and what failed. */
export function workerLine(p: TaskProgress, t: T): string {
  const parts: string[] = []
  if (p.total > 0) parts.push(t('tasks.done', { done: p.done, total: p.total }))
  else if (p.lastRun) parts.push(t('tasks.nothing'))
  if (p.state) parts.push(t(`tasks.state.${p.state}`))
  else if (p.running) parts.push(t('tasks.running'))
  if (p.now) parts.push(t('tasks.now', { name: p.now, percent: formatPercent(p.nowPercent ?? 0) }))
  if (!p.running && p.lastRun) parts.push(t('tasks.lastRun', { ago: ago(p.lastRun) }))
  if (parts.length === 0) parts.push(t('tasks.notYet'))
  if (p.error) parts.push(t('tasks.error', { error: p.error }))
  return parts.join(' · ')
}

/** The scan's line: running, waiting, or the last one's counts and when. */
export function scanLine(s: LibraryStatus, t: T): string {
  if (s.running) return t('tasks.scan.running')
  if (s.queued) return t('tasks.scan.queued')
  if (s.error) return t('tasks.scan.failed', { error: s.error })
  if (!s.finishedAt) return t('tasks.notYet')
  const counts = [t('settings.rescan.movies', { count: s.movies }), t('settings.rescan.episodes', { count: s.episodes })]
  if (s.albums) counts.push(t('settings.rescan.albums', { count: s.albums }))
  if (s.books) counts.push(t('settings.rescan.books', { count: s.books }))
  return t('tasks.scan.last', { ago: ago(s.finishedAt), list: counts.join(', ') })
}

/** The cleaned voice-overs' line (their own counts: done, queued, failed, not eligible). */
export function vocleanLine(v: VocleanStatus, t: T): string {
  const counts = t('settings.voclean.counts', { done: v.done, queued: v.queued, failed: v.failed, notEligible: v.notEligible })
  return v.backfill ? `${counts} · ${t('tasks.voclean.backfill')}` : counts
}

export function Tasks() {
  const t = useT()
  const navigate = useNavigate()
  const [view, setView] = useState<TasksView | null>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    document.title = `${tr('manage.section.tasks')} — ${APP_NAME}`
  }, [])
  usePolling(
    (signal) =>
      getTasks(signal).then(
        (v) => {
          setView(v)
          setError('')
        },
        (e: unknown) => !signal.aborted && setError(errorText(e, tr('tasks.loadFailed'))),
      ),
    TASKS_POLL_MS,
  )

  const row = (key: string, label: string, glyph: SettingsGlyph, tint: SettingsTint, line: string, value: string, to: string) =>
    to ? (
      <ListLinkRow key={key} icon={glyph} tint={tint} label={label} hint={line} value={value} onOpen={() => navigate(to)} />
    ) : (
      <ListRow key={key} leading={<SettingsIcon glyph={glyph} tint={tint} />} label={label} hint={line}>
        {value && <span className={m.rowHint}>{value}</span>}
      </ListRow>
    )

  return (
    <section aria-labelledby="tasks-title">
      <header className={m.header}>
        <h2 id="tasks-title" className={m.title}>
          {t('manage.section.tasks')}
        </h2>
      </header>
      {error && <Notice tone="destructive">{error}</Notice>}
      {!view && !error && (
        <div className={m.spinnerRow} role="status">
          <Spinner track className={m.spinner} /> {t('tasks.loading')}
        </div>
      )}
      {view && (
        <GroupedSection footer={t('tasks.footer')}>
          {view.scan && row('scan', t('tasks.scan'), 'stack', 'indigo', scanLine(view.scan, t), '', '/manage/settings/library')}
          {view.voclean &&
            row(
              'voclean',
              t('settings.voclean.group'),
              'speaker',
              'purple',
              vocleanLine(view.voclean, t),
              percent(view.voclean.done, view.voclean.done + view.voclean.queued),
              '/manage/settings/processing',
            )}
          {view.workers.map((w) => {
            const known = WORKERS[w.id]
            return row(w.id, known ? t(known.label) : w.id, known?.glyph ?? 'sliders', known?.tint ?? 'gray', workerLine(w, t), percent(w.done, w.total), known?.to ?? '')
          })}
        </GroupedSection>
      )}
    </section>
  )
}
