// «Хранилище» like macOS: one bar with a coloured segment per category and
// «Свободно N ГБ из M». A click shows the legend with sizes.
import { useEffect, useState } from 'react'
import { getUsage } from '../../api/files'
import type { FileUsage } from '../../api/types'
import { Menu } from '../../ui/Menu'
import { formatSize } from './model'
import styles from './StorageBar.module.css'
import { useT, type Key } from '../../i18n'

export const CATEGORIES: Record<string, { label: Key; color: string }> = {
  movies: { label: 'common.movies', color: 'var(--blue)' },
  series: { label: 'common.series', color: 'var(--purple)' },
  music: { label: 'common.music', color: 'var(--pink)' },
  books: { label: 'common.books', color: 'var(--orange)' },
  downloads: { label: 'files.storage.downloads', color: 'var(--green)' },
  'stream-cache': { label: 'files.storage.streamCache', color: 'var(--teal)' },
  other: { label: 'files.storage.other', color: 'var(--gray)' },
}

let cached: { at: number; usage: FileUsage } | null = null
/** For tests. */
export const resetUsageCache = () => {
  cached = null
}

function useUsage(refresh: number) {
  const [usage, setUsage] = useState<FileUsage | null>(cached?.usage ?? null)
  const [failed, setFailed] = useState(false)
  useEffect(() => {
    if (cached && Date.now() - cached.at < 60_000 && refresh === 0) return
    const ac = new AbortController()
    getUsage(ac.signal).then(
      (u) => {
        cached = { at: Date.now(), usage: u }
        setUsage(u)
        setFailed(false)
      },
      () => {
        if (!ac.signal.aborted) setFailed(true)
      },
    )
    return () => ac.abort()
  }, [refresh])
  return { usage, failed }
}

export function StorageBar({ refresh = 0, large }: { refresh?: number; large?: boolean }) {
  const { usage: u, failed } = useUsage(refresh)
  const t = useT()
  if (!u || u.total <= 0) {
    if (!failed) return null
    return (
      <div className={`${styles.card} ${large ? styles.large : ''}`} role="status">
        <span className={styles.title}>{t('files.storage')}</span>
        <span className={styles.free}>{t('files.storage.failed')}</span>
      </div>
    )
  }
  const pct = (n: number) => `${Math.max(0, (n / u.total) * 100)}%`
  const parts = u.categories.filter((c) => c.bytes > 0)
  const full = u.used / u.total
  const bar = (
    <span className={styles.bar} role="img" aria-label={t('files.storage.usedOf', { used: formatSize(u.used), total: formatSize(u.total) })}>
      {parts.map((c) => (
        <span key={c.id} className={styles.seg} style={{ width: pct(c.bytes), background: CATEGORIES[c.id]?.color ?? 'var(--gray)' }} />
      ))}
    </span>
  )
  const free = (
    <span className={`${styles.free} ${full >= 0.85 ? styles.warn : ''}`}>
      {full >= 0.95 ? `${t('files.storage.almostFull')} · ` : ''}
      {t('files.storage.freeOf', { free: formatSize(u.free), total: formatSize(u.total) })}
    </span>
  )
  const legend = (
    <ul className={styles.legend}>
      {parts.map((c) => (
        <li key={c.id}>
          <span className={styles.dot} style={{ background: CATEGORIES[c.id]?.color ?? 'var(--gray)' }} />
          <span className={styles.lname}>{CATEGORIES[c.id] ? t(CATEGORIES[c.id]!.label) : c.id}</span>
          <span className={styles.lsize}>{formatSize(c.bytes)}</span>
        </li>
      ))}
      <li>
        <span className={`${styles.dot} ${styles.dotFree}`} />
        <span className={styles.lname}>{t('files.storage.free')}</span>
        <span className={styles.lsize}>{formatSize(u.free)}</span>
      </li>
    </ul>
  )

  if (large) {
    return (
      <section className={`${styles.card} ${styles.large}`} aria-label={t('files.storage')}>
        <div className={styles.head}>
          <span className={styles.title}>{t('files.storage')}</span>
          <span className={styles.used}>{t('files.storage.of', { used: formatSize(u.used), total: formatSize(u.total) })}</span>
        </div>
        {bar}
        {legend}
      </section>
    )
  }

  return (
    <Menu
      label={t('files.storage')}
      placement="top-start"
      className={styles.pop}
      trigger={(tp) => (
        <button {...tp} type="button" className={styles.card} aria-label={t('files.storage.details')}>
          <span className={styles.title}>{t('files.storage')}</span>
          {bar}
          {free}
        </button>
      )}
    >
      {() => (
        <div className={styles.popBody}>
          <div className={styles.head}>
            <span className={styles.title}>{t('files.storage.mediaDisk')}</span>
            <span className={styles.used}>
              {t('files.storage.of', { used: formatSize(u.used), total: formatSize(u.total) })}
            </span>
          </div>
          {bar}
          {legend}
        </div>
      )}
    </Menu>
  )
}
