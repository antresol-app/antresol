// The video bars' sound control: the speaker (mute) and a horizontal volume
// slider beside it, at the leading edge of the tools row. The slider is always
// there — it never opens, grows or pops up — so nothing else on the bar moves
// when the volume changes. Apple's own full-screen media controls (WebKit's
// macOS ones in Safari) keep the volume exactly there, at the left, with the
// transport in the middle and the rest at the right, and other big web players keep it in
// its left cluster. Shared by the film and the live channel player
// (VideoPlayer.tsx, pages/tv/LivePlayer.tsx).
import { Icon } from '../components/Icon'
import { IconButton } from '../ui/IconButton'
import { useT } from '../i18n'
import { Slider } from '../ui/Slider'
import styles from './VolumeControl.module.css'

export interface VolumeControlProps {
  /** 0–1. */
  volume: number
  muted: boolean
  /** The slider moved to a value (0–1). */
  onVolume: (volume: number) => void
  onToggleMute: () => void
  /** The speaker's names: «Выключить звук» while there is sound, «Включить звук» while there is none. */
  muteLabel: string
  unmuteLabel: string
  /** Its tooltip; without one, the name and the key («Выключить звук (M)»). */
  title?: string
}

export function VolumeControl({ volume, muted, onVolume, onToggleMute, muteLabel, unmuteLabel, title }: VolumeControlProps) {
  const t = useT()
  const silent = muted || volume === 0
  const shown = muted ? 0 : volume
  const label = silent ? unmuteLabel : muteLabel
  return (
    <div className={styles.volume}>
      <IconButton
        icon={<Icon name={silent ? 'speakerMute' : 'speaker'} size={24} />}
        label={label}
        tooltip={title ?? `${label} (M)`}
        variant="plain"
        className={styles.mute}
        onClick={onToggleMute}
      />
      <Slider
        label={t('player.volume')}
        value={shown}
        onChange={onVolume}
        min={0}
        max={1}
        step={0.05}
        valueText={`${Math.round(shown * 100)}%`}
        // a press with the pointer leaves the slider at once: the arrow keys
        // go on seeking and changing the volume by the player's own steps
        // (a range input would keep them for itself); the keyboard's Tab
        // still lands on it and the arrows then move it
        onPointerUp={(e) => e.currentTarget.blur()}
        className={styles.range}
      />
    </div>
  )
}
