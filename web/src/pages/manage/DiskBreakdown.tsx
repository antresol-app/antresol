// «Статистика → Медиадиск» → a category: what takes up its space, largest
// first, like the lists behind macOS's Storage settings. Movies, series,
// music and books list their folders; «Торренты» the download data that is
// not in the library; «Другое» the folders of the media disk outside the
// library's sections and what no folder explains. «Другое» needs a walk of
// the disk the first time (the server runs it once, in the background):
// the sheet asks again until it is done.
import { useEffect, useState } from 'react'
import { ApiError, getDiskBreakdown } from '../../api/client'
import type { DiskBreakdown } from '../../api/types'
import { formatSize } from '../../components/ActionBar'
import type { DiskSegment } from '../../components/DiskMeter'
import { Sheet } from '../../ui/Sheet'
import { useT, type Key } from '../../i18n'
import { Icon } from '../../components/Icon'
import { FileIcon } from '../files/FileIcon'
import styles from './DiskBreakdown.module.css'

/**
 * A pending «Другое» is asked again after POLL_MS, then less and less often
 * (×POLL_GROWTH, at most POLL_MAX_MS), and no more after POLL_GIVE_UP_MS:
 * the sheet then says to look later (the server keeps walking).
 */
const POLL_MS = 1500
const POLL_GROWTH = 1.5
const POLL_MAX_MS = 15_000
const POLL_GIVE_UP_MS = 3 * 60_000

const HINTS: Record<string, Key> = {
  downloads: 'stats.disk.hint.downloads',
  'stream-cache': 'stats.disk.hint.cache',
  other: 'stats.disk.hint.other',
}

type Load = { status: 'loading' } | { status: 'error'; message: string } | { status: 'ready'; data: DiskBreakdown }

export function DiskBreakdownSheet({ segment, onClose }: { segment: DiskSegment; onClose: () => void }) {
  const t = useT()
  const [load, setLoad] = useState<Load>({ status: 'loading' })
  // asked for long enough: the walk goes on on the server, the sheet stops asking
  const [gaveUp, setGaveUp] = useState(false)
  const pending = load.status === 'loading' || (load.status === 'ready' && load.data.pending)

  useEffect(() => {
    const ac = new AbortController()
    let timer = 0
    let delay = POLL_MS
    const since = Date.now()
    const ask = () =>
      getDiskBreakdown(segment.id, ac.signal).then(
        (data) => {
          setLoad({ status: 'ready', data })
          if (!data.pending) return
          if (Date.now() - since + delay > POLL_GIVE_UP_MS) return setGaveUp(true)
          timer = window.setTimeout(ask, delay)
          delay = Math.min(POLL_MAX_MS, delay * POLL_GROWTH)
        },
        (e: unknown) => !ac.signal.aborted && setLoad({ status: 'error', message: e instanceof ApiError ? e.message : t('common.loadFailed') }),
      )
    void ask()
    return () => {
      ac.abort()
      window.clearTimeout(timer)
    }
  }, [segment.id])

  // a pending answer may carry the last result (a fresh walk runs): show it meanwhile
  const data = load.status === 'ready' && (!load.data.pending || load.data.entries.length > 0) ? load.data : null
  const total = data?.bytes || segment.bytes
  const share = (n: number) => (total > 0 ? Math.min(1, n / total) : 0)
  const name = (n: string) => (n === '.vault-trash' ? t('stats.disk.trash') : n)
  const rows: { key: string; label: string; bytes: number; dir?: boolean; trash?: boolean; muted?: boolean; hint?: string }[] = data
    ? [
        ...data.entries.map((e) => ({ key: e.name, label: name(e.name), bytes: e.bytes, dir: e.dir, trash: e.name === '.vault-trash' })),
        ...(data.more ? [{ key: '+more', label: t('stats.disk.more', { count: data.more.count }), bytes: data.more.bytes, muted: true }] : []),
        ...(data.rest ? [{ key: '+rest', label: t('stats.disk.rest'), bytes: data.rest, muted: true, hint: t('stats.disk.restHint') }] : []),
      ]
    : []

  return (
    <Sheet
      title={`${segment.label} — ${formatSize(total)}`}
      message={t(HINTS[segment.id] ?? 'stats.disk.hint.library')}
      role="dialog"
      size="large"
      dismissOnOutside
      onCancel={onClose}
      actions={[]}
    >
      {pending && (
        <div className={data ? `${styles.status} ${styles.compact}` : styles.status} role="status">
          {!gaveUp && <span className={styles.spinner} aria-hidden />}{' '}
          {gaveUp ? t('stats.disk.later') : load.status !== 'ready' ? t('common.loading') : data ? t('stats.disk.updating') : t('stats.disk.counting')}
        </div>
      )}
      {data?.partial && <p className={styles.partial}>{t('stats.disk.partial')}</p>}
      {load.status === 'error' && (
        <p className={styles.problem} role="alert">
          {load.message}
        </p>
      )}
      {data && rows.length === 0 && <p className={styles.status}>{t('stats.disk.empty')}</p>}
      {rows.length > 0 && (
        <ul className={styles.list} style={{ ['--seg' as string]: segment.color }}>
          {rows.map((r) => (
            <li key={r.key} className={styles.row} data-muted={r.muted || undefined} title={r.hint}>
              <span className={styles.icon} aria-hidden>
                {r.trash ? <Icon name="trash" size={22} className={styles.trash} /> : !r.muted && <FileIcon entry={{ name: r.label, isDir: r.dir ?? true, kind: r.dir === false ? 'other' : 'folder' }} size={28} />}
              </span>
              <span className={styles.text}>
                <span className={styles.name} data-name>
                  {r.label}
                </span>
                <span className={styles.track} aria-hidden>
                  <span className={styles.fill} style={{ width: `${share(r.bytes) * 100}%` }} />
                </span>
              </span>
              <span className={styles.size}>{formatSize(r.bytes)}</span>
            </li>
          ))}
        </ul>
      )}
    </Sheet>
  )
}
