import { locale } from '../i18n'
import styles from './Avatar.module.css'

export interface AvatarProps {
  name: string
  avatar: string
  size?: number
  className?: string
}

/** Circular gradient avatar with the profile's initial, like Apple TV profiles. */
export function Avatar({ name, avatar, size = 32, className }: AvatarProps) {
  const id = /^grad-[1-8]$/.test(avatar) ? avatar : 'grad-8'
  const initial = name.trim().charAt(0).toLocaleUpperCase(locale()) || '?'
  return (
    <span
      className={[styles.avatar, className].filter(Boolean).join(' ')}
      style={{ width: size, height: size, fontSize: Math.round(size * 0.42), background: `var(--${id})` }}
      aria-hidden
    >
      {initial}
    </span>
  )
}
