// A channel's picture is the programme on air: its backdrop (16:9), else
// its poster, else a neutral placeholder with the tv glyph. Never a
// coloured tile with the channel's name on it.
import type { ChannelProgram } from '../../api/channels'
import { Icon } from '../../components/Icon'
import { Artwork } from '../../ui/Artwork'
import styles from './Tv.module.css'

export function ChannelArt({ program, width, sizes, eager, className }: { program?: ChannelProgram; width: number; sizes?: string; eager?: boolean; className?: string }) {
  const placeholder = (
    <span className={styles.artPlaceholder}>
      <Icon name="tv" size={Math.round(Math.min(40, Math.max(20, width / 6)))} strokeWidth={1.5} />
    </span>
  )
  const backdrop = program?.backdrop
  const poster = program?.poster
  return (
    <span className={[styles.art, className].filter(Boolean).join(' ')}>
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
