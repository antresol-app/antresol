// The disk as one bar: a coloured segment per category and the free space,
// like «Хранилище» in macOS settings, with a legend underneath. With
// `onPick`, a category's segment and its legend item open what it is made
// of (the legend items are the buttons; the segments follow the pointer).
import { formatSize } from './ActionBar'
import { t as tr, useT, type Key } from '../i18n'
import styles from './DiskMeter.module.css'

export interface DiskSegment {
  id: string
  label: string
  bytes: number
  color: string
}

export interface DiskMeterProps {
  total: number
  free: number
  segments: DiskSegment[]
  /** Short title above the bar. */
  title?: string
  /** A category was picked (its segment or legend item). */
  onPick?: (s: DiskSegment) => void
  /** The tooltip of a pickable category. */
  pickHint?: string
}

/** Names (catalog keys) and colours (palette tokens) of /api/files/usage categories for the meter. */
export const DISK_CATEGORIES: Record<string, { label: Key; color: string }> = {
  movies: { label: 'comp.disk.movies', color: 'var(--blue)' },
  series: { label: 'comp.disk.series', color: 'var(--purple)' },
  music: { label: 'comp.disk.music', color: 'var(--pink)' },
  books: { label: 'comp.disk.books', color: 'var(--orange)' },
  // another build's own folders
  ...(__LIBRARY_EDITION__
    ? {}
    : {
        downloads: { label: 'comp.disk.downloads' as Key, color: 'var(--green)' },
        'stream-cache': { label: 'comp.disk.cache' as Key, color: 'var(--teal)' },
      }),
  other: { label: 'comp.disk.other', color: 'var(--gray)' },
}

export function segmentsOf(categories: { id: string; bytes: number }[]): DiskSegment[] {
  return categories
    .filter((c) => c.bytes > 0)
    .map((c) => {
      const cat = DISK_CATEGORIES[c.id]
      return { id: c.id, bytes: c.bytes, label: cat ? tr(cat.label) : c.id, color: cat?.color ?? 'var(--gray)' }
    })
}

export function DiskMeter({ total, free, segments, title, onPick, pickHint }: DiskMeterProps) {
  const t = useT()
  title ??= t('comp.disk.title')
  const used = Math.max(0, total - free)
  const pct = (n: number) => `${total > 0 ? Math.max(0, (n / total) * 100) : 0}%`
  const level = total > 0 ? used / total : 0
  return (
    <section className={styles.meter} aria-label={title}>
      <div className={styles.head}>
        <h3 className={styles.title}>{title}</h3>
        <span className={`${styles.summary} ${level >= 0.85 ? styles.warn : ''}`}>
          {level >= 0.95 ? t('comp.disk.almostFull') : level >= 0.85 ? t('comp.disk.runningOut') : ''}
          {t('comp.disk.freeOf', { free: formatSize(free), total: formatSize(total) })}
        </span>
      </div>
      <div className={styles.bar} role="img" aria-label={t('comp.disk.usedOf', { used: formatSize(used), total: formatSize(total) })}>
        {segments.map((s) => (
          <span
            key={s.id}
            className={`${styles.seg} ${onPick ? styles.pickable : ''}`}
            style={{ width: pct(s.bytes), background: s.color }}
            title={onPick ? `${s.label} · ${formatSize(s.bytes)}` : undefined}
            onClick={onPick ? () => onPick(s) : undefined}
          />
        ))}
      </div>
      <ul className={styles.legend}>
        {segments.map((s) => {
          const inner = (
            <>
              <span className={styles.dot} style={{ background: s.color }} />
              <span className={styles.name}>{s.label}</span>
              <span className={styles.size}>{formatSize(s.bytes)}</span>
            </>
          )
          return (
            <li key={s.id}>
              {onPick ? (
                <button type="button" className={styles.item} title={pickHint} onClick={() => onPick(s)}>
                  {inner}
                </button>
              ) : (
                <span className={styles.item}>{inner}</span>
              )}
            </li>
          )
        })}
        <li>
          <span className={styles.item}>
            <span className={`${styles.dot} ${styles.free}`} />
            <span className={styles.name}>{t('comp.disk.free')}</span>
            <span className={styles.size}>{formatSize(free)}</span>
          </span>
        </li>
      </ul>
    </section>
  )
}
