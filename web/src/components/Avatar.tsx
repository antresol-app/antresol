import { useEffect, useState } from 'react'
import { locale } from '../i18n'
import styles from './Avatar.module.css'

export interface AvatarProps {
  name: string
  /** The coloured circle's gradient (grad-1…8): the letter's ground, and the stand-in for a photo. */
  avatar: string
  /** The profile's photo (its address; absent: none): drawn in place of the letter. */
  photo?: string
  size?: number
  className?: string
}

/**
 * Circular avatar like Apple TV profiles: the profile's photo, or its initial
 * on a gradient. The gradient is also under the photo while it loads, and the
 * letter stands in again if it never does (a lost connection, a removed file).
 */
export function Avatar({ name, avatar, photo, size = 32, className }: AvatarProps) {
  const id = /^grad-[1-8]$/.test(avatar) ? avatar : 'grad-8'
  const initial = name.trim().charAt(0).toLocaleUpperCase(locale()) || '?'
  const [failed, setFailed] = useState(false)
  useEffect(() => setFailed(false), [photo])
  const shown = !!photo && !failed
  return (
    <span
      className={[styles.avatar, shown && styles.hasPhoto, className].filter(Boolean).join(' ')}
      style={{ width: size, height: size, fontSize: Math.round(size * 0.42), background: `var(--${id})` }}
      aria-hidden
    >
      {shown ? <img className={styles.photo} src={photo} alt="" draggable={false} decoding="async" onError={() => setFailed(true)} /> : initial}
    </span>
  )
}
