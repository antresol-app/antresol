// The player bar: what plays, as a glass capsule floating at the bottom of
// every page (above the tab bar on phones) — the Music app's mini player.
// The artwork and two lines open the full player; the controls sit on the
// trailing edge; a hairline of progress runs along the bottom. It knows
// nothing about audio: MiniPlayer feeds it. Its height and place are the
// --playerbar-* tokens, which pages and toasts read to keep clear of it.
import { useState, type ReactNode } from 'react'
import { Icon, type IconName } from '../components/Icon'
import styles from './PlayerBar.module.css'

export interface PlayerBarProps {
  /** The landmark's name («Сейчас играет»). */
  label: string
  title: string
  subtitle?: string
  /** The artwork's URL; the glyph stands in without one or when it fails. */
  artwork?: string
  fallbackIcon: IconName
  /** Opens the full player; without it the text is not a button. */
  onOpen?: () => void
  /** The open button's accessible name. */
  openLabel?: string
  /** 0…1 */
  progress: number
  /** PlayerBarButton × 3: back, play/pause (prominent), forward. */
  children: ReactNode
}

export function PlayerBar({ label, title, subtitle, artwork, fallbackIcon, onOpen, openLabel, progress, children }: PlayerBarProps) {
  const [failed, setFailed] = useState('')
  const pct = Math.min(100, Math.max(0, progress * 100))
  const info = (
    <>
      <span className={styles.artwork}>
        {artwork && failed !== artwork ? (
          <img src={artwork} alt="" draggable={false} onError={() => setFailed(artwork)} />
        ) : (
          <Icon name={fallbackIcon} size={20} />
        )}
      </span>
      <span className={styles.text}>
        <span className={styles.title}>{title}</span>
        {subtitle && <span className={styles.subtitle}>{subtitle}</span>}
      </span>
    </>
  )
  return (
    <aside className={`${styles.bar} glass`} aria-label={label} data-testid="mini-player" data-player-bar>
      {onOpen ? (
        <button type="button" className={styles.info} onClick={onOpen} aria-label={openLabel}>
          {info}
        </button>
      ) : (
        <span className={styles.info}>{info}</span>
      )}
      <span className={styles.controls}>{children}</span>
      <span className={styles.progress} aria-hidden>
        <span className={styles.fill} style={{ width: `${pct}%` }} />
      </span>
    </aside>
  )
}

export interface PlayerBarButtonProps {
  icon: IconName
  label: string
  onClick: () => void
  /** Play/pause: a larger glyph. */
  prominent?: boolean
  busy?: boolean
}

/** One of the bar's controls: a borderless 44 pt target with a glyph. */
export function PlayerBarButton({ icon, label, onClick, prominent, busy }: PlayerBarButtonProps) {
  return (
    <button type="button" className={styles.button} aria-label={label} aria-busy={busy || undefined} onClick={onClick}>
      <Icon name={icon} size={prominent ? 24 : 22} />
    </button>
  )
}
