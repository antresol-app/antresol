// A started import: songs ready out of all, the albums being fetched (each
// with its state and «Повторить» when it can be tried again), the songs
// waiting for a decision and, once over, what could not be done.
import { useState } from 'react'
import type { ImportGroup, ImportGroupState, ImportJob } from '../../../api/types'
import { Button } from '../../../ui/Button'
import { Icon } from '../../../components/Icon'
import { useCanDownload } from '../../../state/canDownload'
import { formatSize } from '../../../state/format'
import { locale, t as tr, useT, type Key } from '../../../i18n'
import m from '../Manage.module.css'
import styles from './ImportProgress.module.css'

export const GROUP_STATE: Record<ImportGroupState, Key> = {
  waiting: 'import.state.waiting',
  searching: 'import.state.searching',
  downloading: 'import.state.downloading',
  waiting_space: 'import.state.waitingSpace',
  waiting_download: 'import.state.waitingDownload',
  done: 'import.state.done',
  have: 'import.state.have',
  not_found: 'import.state.notFound',
  no_format: 'import.state.noFormat',
  failed: 'import.state.failed',
}
// «Повторить» of an album waiting for another download: its own search
const RETRYABLE = new Set<ImportGroupState>(['not_found', 'no_format', 'failed', 'waiting_space', 'waiting_download'])
const TONE: Partial<Record<ImportGroupState, string>> = {
  done: 'ok',
  have: 'ok',
  downloading: 'busy',
  searching: 'busy',
  waiting_download: 'busy',
  waiting_space: 'warn',
  not_found: 'warn',
  no_format: 'warn',
  failed: 'bad',
}

/** What a group's state says: «Ждёт загрузку «…»» names the download it waits for. */
export function waitingDownloadLabel(name: string | undefined): string {
  return name ? tr('import.state.waitingFor', { name }) : tr(GROUP_STATE.waiting_download)
}

/** `local`: the server downloads nothing, so an album it lacks is «Нет в медиатеке». */
export function groupStateLabel(g: Pick<ImportGroup, 'state' | 'waitingFor'>, local = false): string {
  if (g.state === 'not_found' && local) return tr('import.state.notInLibrary')
  return g.state === 'waiting_download' ? waitingDownloadLabel(g.waitingFor) : (GROUP_STATE[g.state] ? tr(GROUP_STATE[g.state]) : g.state)
}

/** Songs ready (downloaded or already in the library) out of the songs to import. */
export function readyCounts(j: ImportJob): { ready: number; of: number } {
  const p = j.progress
  return { ready: p.done + p.have, of: Math.max(0, p.total - p.skipped) }
}

const hhmm = (iso: string) => new Date(iso).toLocaleTimeString(locale(), { hour: '2-digit', minute: '2-digit' })

export interface ImportProgressProps {
  job: ImportJob
  groups: ImportGroup[]
  /** anyFormat: «Скачать в другом формате». Absent where the server downloads nothing: no retry. */
  onRetry?: (groupId: number, anyFormat?: boolean) => Promise<void>
  onReview: () => void
  onCancel: () => void
  onDone: () => void
}

export function ImportProgress({ job, groups, onRetry, onReview, onCancel, onDone }: ImportProgressProps) {
  const t = useT()
  const local = !useCanDownload()
  const [retrying, setRetrying] = useState<number | null>(null)
  const p = job.progress
  const { ready, of } = readyCounts(job)
  const pct = of > 0 ? Math.round((ready / of) * 100) : 0
  const active = job.status === 'matching' || job.status === 'downloading'
  const pending = Math.max(0, p.total - (p.matched + p.review + p.notFound + p.have + p.queued + p.downloading + p.done + p.failed + p.skipped))
  const paused = job.status === 'matching' && job.matchPausedUntil && Date.parse(job.matchPausedUntil) > Date.now() ? job.matchPausedUntil : ''

  const phase =
    job.status === 'matching'
      ? t('import.phase.matching', { done: p.total - pending, total: p.total })
      : job.status === 'downloading'
        ? t('import.phase.downloading')
        : job.status === 'done'
          ? t('import.phase.done')
          : job.status === 'failed'
            ? t('import.phase.failed')
            : t('import.phase.canceled')

  const extra = [
    p.notFound > 0 && t('import.progress.notFound', { n: p.notFound }),
    p.failed > 0 && t('import.progress.failed', { n: p.failed }),
    p.skipped > 0 && t('import.progress.skipped', { n: p.skipped }),
  ].filter(Boolean) as string[]

  const retry = (id: number, anyFormat?: boolean) => {
    if (!onRetry) return
    setRetrying(id)
    void onRetry(id, anyFormat).finally(() => setRetrying(null))
  }

  return (
    <div className={styles.progress} data-testid="import-progress">
      <div className={`${styles.card} glass`}>
        <p className={styles.phase}>
          {active && <span className={m.spinner} aria-hidden />}
          {job.status === 'done' && <Icon name="checkCircle" size={18} className={styles.okIcon} aria-hidden />}
          {phase}
        </p>
        {/* not a live region: an import.progress comes every 2 s; hidden for a sign-in read that failed (nothing counted) */}
        {(p.total > 0 || job.status !== 'failed') && (
          <>
            <p id="import-ready" className={styles.ready} tabIndex={-1}>
              {t('import.progress.ready', { ready, of })}
            </p>
            <div className={styles.bar} role="progressbar" aria-label={t('import.progress.aria')} aria-valuemin={0} aria-valuemax={of} aria-valuenow={ready}>
              <span style={{ width: `${pct}%` }} />
            </div>
          </>
        )}
        {extra.length > 0 && <p className={styles.extra}>{extra.join(' · ')}</p>}
        {paused && (
          <p className={styles.paused} role="status">
            <Icon name="cloud" size={16} aria-hidden /> {t('import.progress.paused', { time: hhmm(paused) })}
          </p>
        )}
        {job.status === 'failed' && job.error && (
          <p className={styles.failed} role="alert">
            {job.error}
          </p>
        )}
        {job.report.savedAlbumsMissing > 0 && <p className={styles.extra}>{t('import.progress.savedMissing', { n: job.report.savedAlbumsMissing })}</p>}
        <div className={styles.actions}>
          {p.review > 0 && (
            <Button variant={active || job.status === 'done' ? 'primary' : 'secondary'} size="small" icon="warning" onClick={onReview}>
              {t('import.progress.review', { n: p.review })}
            </Button>
          )}
          {active ? (
            <Button variant="secondary" size="small" onClick={onCancel}>
              {t('import.cancel')}
            </Button>
          ) : (
            <Button variant={p.review > 0 ? 'secondary' : 'primary'} size="small" onClick={onDone}>
              {t('common.done')}
            </Button>
          )}
        </div>
      </div>

      {groups.length > 0 && (
        <>
          <h3 className={m.groupTitle}>{t('common.albums')}</h3>
          <ul className={m.group} aria-label={t('common.albums')}>
            {groups.map((g) => {
              const downloading = g.state === 'downloading' && g.bytes > 0
              return (
                <li key={g.id} className={`${m.row} ${styles.group}`} data-testid="import-group">
                  <div className={m.rowText}>
                    <p className={`${m.rowLabel} ${styles.album}`}>
                      {g.artist} — {g.album}
                    </p>
                    <p className={m.rowHint}>
                      {[g.year || '', t('import.songs', { count: g.songs }), g.bytes > 0 ? formatSize(g.bytes) : ''].filter(Boolean).join(' · ')}
                    </p>
                    {downloading && (
                      <div className={styles.groupBar} aria-hidden>
                        <span style={{ width: `${Math.min(100, Math.round((g.done / g.bytes) * 100))}%` }} />
                      </div>
                    )}
                    {(g.state === 'failed' || g.state === 'no_format') && g.error && <p className={styles.groupError}>{g.error}</p>}
                  </div>
                  <div className={m.rowControl}>
                    <span className={`${styles.state} ${styles[TONE[g.state] ?? ''] ?? ''}`}>{groupStateLabel(g, local)}</span>
                    {onRetry && g.state === 'no_format' && (
                      <Button size="small" loading={retrying === g.id} disabled={retrying !== null} onClick={() => retry(g.id, true)}>
                        {t('import.progress.anyFormat')}
                      </Button>
                    )}
                    {onRetry && RETRYABLE.has(g.state) && (
                      <Button size="small" variant="secondary" loading={retrying === g.id} disabled={retrying !== null} onClick={() => retry(g.id)}>
                        {t('common.retry')}
                      </Button>
                    )}
                  </div>
                </li>
              )
            })}
          </ul>
        </>
      )}
    </div>
  )
}
