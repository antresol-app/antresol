// A row of items saved on this device: cover, title, «Загружено · 120 МБ»
// and a remove button. Tapping an item plays or opens it (the parent decides).
import { useState } from 'react'
import { imageUrl } from '../api/client'
import { formatSize } from '../components/ActionBar'
import { Icon, type IconName } from '../components/Icon'
import { IconButton } from '../ui/IconButton'
import { useT, type Key } from '../i18n'
import { removeOffline, type OfflineEntry } from './store'
import styles from './OfflineShelf.module.css'

export interface OfflineShelfProps {
  title: string
  entries: OfflineEntry[]
  onOpen: (e: OfflineEntry) => void
  /** The item being opened (spinner on it). */
  busy?: string | null
}

const ICON: Record<OfflineEntry['kind'], IconName> = { album: 'music', book: 'book', audiobook: 'headphones', comic: 'book' }
const ACTION: Record<OfflineEntry['kind'], Key> = { album: 'common.listen', book: 'common.read', audiobook: 'common.listen', comic: 'common.read' }

function Item({ e, onOpen, busy }: { e: OfflineEntry; onOpen: () => void; busy: boolean }) {
  const t = useT()
  const [failed, setFailed] = useState(false)
  const src = imageUrl(e.poster, 480)
  const square = e.kind === 'album'
  const vols = e.volumes?.length ?? 0
  const size = [vols > 1 ? t('offline.volumes', { n: vols }) : '', e.bytes > 0 ? formatSize(e.bytes) : ''].filter(Boolean).join(' · ')
  return (
    <li className={styles.item}>
      <button type="button" className={styles.open} onClick={onOpen} aria-label={t('offline.openItem', { action: t(ACTION[e.kind]), title: e.title })} aria-busy={busy || undefined}>
        <span className={`${styles.art} ${square ? styles.square : ''}`}>
          {src && !failed ? (
            <img src={src} alt="" loading="lazy" decoding="async" draggable={false} onError={() => setFailed(true)} />
          ) : (
            <span className={styles.fallback}>
              <Icon name={ICON[e.kind]} size={34} strokeWidth={1.4} />
            </span>
          )}
          {busy && <span className={styles.spinner} aria-hidden />}
        </span>
        <span className={styles.name}>{e.title}</span>
        {e.subtitle && <span className={styles.sub}>{e.subtitle}</span>}
      </button>
      <div className={styles.meta}>
        <span className={styles.saved}>
          <Icon name="checkCircle" size={13} strokeWidth={2.2} />
          <span className={styles.lines}>
            {t('offline.downloaded')}
            {size && <span className={styles.size}>{size}</span>}
          </span>
        </span>
        <IconButton size="xs" variant="plain" icon="trash" className={styles.remove} label={t('offline.removeItem', { title: e.title })} onClick={() => void removeOffline(e)} />
      </div>
    </li>
  )
}

export function OfflineShelf({ title, entries, onOpen, busy }: OfflineShelfProps) {
  if (entries.length === 0) return null
  const id = `offline-${title}`
  return (
    <section className={styles.shelf} aria-labelledby={id}>
      <h2 id={id} className={styles.title}>
        {title}
      </h2>
      <ul className={styles.row} aria-label={title}>
        {entries.map((e) => (
          <Item key={`${e.kind}:${e.id}`} e={e} onOpen={() => onOpen(e)} busy={busy === `${e.kind}:${e.id}`} />
        ))}
      </ul>
    </section>
  )
}
