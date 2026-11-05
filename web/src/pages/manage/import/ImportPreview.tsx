// What a job would import, before anything happens: the counts, the size
// estimate against the free space, «Избранное» on or off and a checkbox per
// playlist. Each change is saved at once (PATCH); «Начать импорт» starts.
import type { ImportJob, ImportJobPatch } from '../../../api/types'
import { Button } from '../../../ui/Button'
import { DiskMeter } from '../../../components/DiskMeter'
import { Toggle } from '../../../ui/Toggle'
import { useCanDownload } from '../../../state/canDownload'
import { getLanguage, t as tr, useT } from '../../../i18n'
import m from '../Manage.module.css'
import styles from './ImportPreview.module.css'

/** Gigabytes as the page says them: «0,4», «120» (0.4 in English). */
export function gb(bytes: number): string {
  const v = Math.max(0, bytes) / 2 ** 30
  const digits = v < 10 ? 1 : 0
  const s = v.toFixed(digits)
  return (s === '0.0' && v > 0 ? '0.1' : s).replace('.', getLanguage() === 'ru' ? ',' : '.')
}

/** Why a playlist cannot be read, and the way round it, per service (neutral where the reason is unknown). */
export function unreadableHint(service: string): string {
  switch (service) {
    case 'spotify':
      return tr('import.unreadable.spotify')
    case 'youtube':
      return tr('import.unreadable.neutral') + tr('import.unreadable.takeout')
    case 'yandex':
    case 'deezer':
      return tr('import.unreadable.neutral') + tr('import.unreadable.csv')
    default:
      return tr('import.unreadable.neutral')
  }
}

export interface ImportPreviewProps {
  job: ImportJob
  onPatch: (patch: ImportJobPatch) => void
  onStart: () => void
  onCancel: () => void
  busy?: '' | 'start' | 'cancel'
}

export function ImportPreview({ job, onPatch, onStart, onCancel, busy = '' }: ImportPreviewProps) {
  const t = useT()
  const downloads = useCanDownload()
  const favorites = t('import.favorites')
  const c = job.counts
  // a server that shows nobody its disk (a library-only one) sends zeros: no space line
  const known = downloads && job.disk.total > 0
  const tight = known && job.estimatedBytes > job.disk.free
  return (
    <div className={styles.preview} data-testid="import-preview">
      <ul className={styles.counts} aria-label={t('import.preview.countsAria')}>
        <li>{t('import.preview.songs', { n: c.songs })}</li>
        <li>{t('import.preview.albums', { n: c.albums })}</li>
        <li>{t('import.preview.playlists', { n: c.playlists })}</li>
        {downloads && <li>{t('import.preview.about', { gb: gb(job.estimatedBytes) })}</li>}
      </ul>
      {known && (
        <div className={`${styles.disk} glass`}>
          <DiskMeter
            title={t('import.preview.free', { gb: gb(job.disk.free) })}
            total={job.disk.total}
            free={job.disk.free}
            segments={[{ id: 'used', label: t('import.preview.used'), bytes: Math.max(0, job.disk.total - job.disk.free), color: '#8e8e93' }]}
          />
          {tight && <p className={styles.tight}>{t('import.preview.tight')}</p>}
        </div>
      )}
      {job.report.unresolved > 0 && (
        <p className={m.groupFoot}>{t('import.preview.unresolved', { n: job.report.unresolved })}</p>
      )}

      <h3 className={m.groupTitle}>{t('import.preview.favoritesTitle')}</h3>
      <ul className={m.group}>
        <li className={m.row}>
          <div className={m.rowText}>
            <p className={m.rowLabel} id={`import-fav-${job.id}`}>
              {favorites}
            </p>
            <p className={m.rowHint}>{t('import.preview.liked', { n: c.liked })}</p>
          </div>
          <div className={m.rowControl}>
            <Toggle label={favorites} checked={job.favorites} disabled={busy !== ''} onChange={(on) => onPatch({ favorites: on })} />
          </div>
        </li>
      </ul>

      {job.playlists.length > 0 && (
        <>
          <h3 className={m.groupTitle}>{t('common.playlists')}</h3>
          <ul className={m.group} aria-label={t('common.playlists')}>
            {job.playlists.map((p) => {
              const id = `import-pl-${p.id}`
              return (
                <li key={p.id} className={`${m.row} ${p.readable ? '' : styles.unreadable}`}>
                  <input
                    id={id}
                    type="checkbox"
                    className={styles.checkbox}
                    checked={p.readable && p.selected}
                    disabled={!p.readable || busy !== ''}
                    onChange={(e) => onPatch({ playlists: { [String(p.id)]: e.target.checked } })}
                  />
                  <div className={m.rowText}>
                    <label htmlFor={id} className={m.rowLabel}>
                      {p.name}
                    </label>
                    <p className={m.rowHint}>{p.readable ? t('import.songs', { count: p.songs }) : unreadableHint(job.service)}</p>
                  </div>
                </li>
              )
            })}
          </ul>
        </>
      )}

      <div className={styles.actions}>
        <Button icon={downloads ? 'download' : undefined} loading={busy === 'start'} disabled={busy !== '' || c.songs === 0} onClick={onStart}>
          {t('import.start')}
        </Button>
        <Button variant="secondary" loading={busy === 'cancel'} disabled={busy !== ''} onClick={onCancel}>
          {t('import.cancel')}
        </Button>
        <p className={styles.hint}>{t(downloads ? 'import.preview.hint' : 'import.preview.hintLocal')}</p>
      </div>
    </div>
  )
}
