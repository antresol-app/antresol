// A playlist's artwork: a 2×2 mosaic of its first four album covers, one
// cover across the whole square when there is only one, a gradient with a
// note when there are none.
import { useState } from 'react'
import { imageUrl } from '../../api/client'
import { Icon } from '../../components/Icon'
import styles from './PlaylistCover.module.css'
import { useT } from '../../i18n'

export interface PlaylistCoverProps {
  covers: string[]
  name: string
  className?: string
}

function Tile({ src }: { src: string }) {
  const [failed, setFailed] = useState(false)
  if (failed) return <span className={styles.tileFallback} />
  return <img src={imageUrl(src)} alt="" loading="lazy" decoding="async" draggable={false} onError={() => setFailed(true)} />
}

export function PlaylistCover({ covers, name, className }: PlaylistCoverProps) {
  const t = useT()
  const list = covers.filter(Boolean).slice(0, 4)
  const mosaic = list.length >= 2
  // with 2 or 3 covers the mosaic repeats them to fill four tiles
  const tiles = mosaic ? [0, 1, 2, 3].map((i) => list[i] ?? list[(i + 1) % list.length]!) : list
  return (
    <div className={[styles.cover, mosaic ? styles.mosaic : '', className].filter(Boolean).join(' ')} aria-label={t('music.playlistCover', { name })} role="img">
      {tiles.length === 0 ? (
        <span className={styles.empty}>
          <Icon name="music" size={40} strokeWidth={1.3} />
        </span>
      ) : (
        tiles.map((src, i) => <Tile key={`${i}-${src}`} src={src} />)
      )}
    </div>
  )
}
