// Option B of the downloads redesign, kept in the gallery for comparison:
// a card per download, the poster at the card's full height, the title and
// status beside it, the App Store ring (progress around a pause/resume
// glyph) as the one action in the top-right corner, «⋯» under it.
import type { Torrent } from '../../api/types'
import { imageUrl } from '../../api/client'
import { formatSize } from '../../state/format'
import { torrentStatusLabel } from '../../state/format'
import { Card } from '../Card'
import { IconButton } from '../IconButton'
import { ProgressBar } from '../ProgressBar'
import { percent, ProgressRing } from '../StatusRing'
import styles from './Gallery.module.css'

export function DownloadTile({ t }: { t: Torrent }) {
  const finished = t.status === 'done'
  const paused = t.status === 'paused' || t.status === 'waiting_space'
  const pct = percent(t.done, t.size)
  return (
    <Card as="li" padding="sm" className={styles.tile}>
      <div className={styles.tilePoster}>{t.poster && <img src={imageUrl(t.poster)} alt="" />}</div>
      <div className={styles.tileMain}>
        <p className={styles.cardTitle}>{t.title}</p>
        <p className={styles.note}>
          {torrentStatusLabel(t.status, t.mode)} · {finished ? formatSize(t.uploaded) : `${pct} % · ${formatSize(t.done)} / ${formatSize(t.size)}`}
        </p>
        {!finished && t.status !== 'error' && <ProgressBar value={t.size > 0 ? t.done / t.size : 0} tone={paused ? 'paused' : 'accent'} />}
      </div>
      <div className={styles.tileActions}>
        {!finished && t.status !== 'error' && (
          <span className={styles.ringWrap}>
            <IconButton size="sm" icon={paused ? 'play' : 'pause'} label={paused ? 'Продолжить' : 'Пауза'} />
            <ProgressRing diameter={36} mode={paused ? 'paused' : 'active'} pct={pct} label="Прогресс" />
          </span>
        )}
        <IconButton size="sm" icon="ellipsis" label="Ещё" variant="plain" />
      </div>
    </Card>
  )
}
