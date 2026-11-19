// A channel's picture is the programme on air: its backdrop (16:9), else
// its poster, else a neutral placeholder with the tv glyph. Never a
// coloured tile with the channel's name on it. `optional` (a list's rows):
// no picture is no box at all — an empty grey frame says nothing and makes
// rows with and without pictures look like two kinds of row.
import type { ChannelProgram } from '../../api/channels'
import { Icon } from '../../components/Icon'
import { Artwork } from '../../ui/Artwork'
import styles from './Tv.module.css'

export function ChannelArt({ program, width, sizes, eager, optional, className }: { program?: ChannelProgram; width: number; sizes?: string; eager?: boolean; optional?: boolean; className?: string }) {
  const backdrop = program?.backdrop
  const poster = program?.poster
  if (optional && !backdrop && !poster) return null
  const placeholder = optional ? null : (
    <span className={styles.artPlaceholder} data-art-placeholder>
      <Icon name="tv" size={Math.round(Math.min(40, Math.max(20, width / 6)))} strokeWidth={1.5} />
    </span>
  )
  return (
    <span className={[styles.art, optional && styles.artOptional, className].filter(Boolean).join(' ')}>
      {backdrop || poster ? (
        <Artwork
          key={backdrop || poster}
          src={backdrop || poster}
          width={width}
          sizes={sizes}
          eager={eager}
          className={backdrop ? styles.artImg : styles.artPoster}
          fallback={poster && backdrop ? <Artwork src={poster} width={width} className={styles.artPoster} fallback={placeholder} /> : placeholder}
        />
      ) : (
        placeholder
      )}
    </span>
  )
}
