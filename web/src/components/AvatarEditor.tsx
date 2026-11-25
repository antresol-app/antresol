// The avatar part of a profile's form: the preview, «Загрузить фото…» (framed
// in PhotoCropSheet) or «Заменить… / Убрать фото», and the eight colours of
// the circle the letter stands on when there is no photo. It only collects the
// choice: `change` is the photo's staged change (none yet, a new photo, or its
// removal) and the form applies it with its Save, after the profile exists
// (setUserPhoto / removeUserPhoto), so Cancel throws it away like every other
// field.
import { useEffect, useState } from 'react'
import { Button } from '../ui/Button'
import { useT } from '../i18n'
import { Avatar } from './Avatar'
import { usePhotoPick } from './PhotoPick'
import styles from './AvatarEditor.module.css'

/** What the form does to the photo when it is saved. */
export type PhotoChange = { kind: 'set'; blob: Blob } | { kind: 'remove' } | null

/** The gradients of the letter's circle (grad-1…8). */
export const AVATAR_COLORS = Array.from({ length: 8 }, (_, i) => `grad-${i + 1}`)

export interface AvatarEditorProps {
  name: string
  /** The circle's gradient (grad-N). */
  avatar: string
  onAvatar: (id: string) => void
  /** The profile's photo now (absent: none, or a profile not created yet). */
  photo?: string
  change: PhotoChange
  onChange: (c: PhotoChange) => void
}

export function AvatarEditor({ name, avatar, onAvatar, photo, change, onChange }: AvatarEditorProps) {
  const t = useT()
  const pick = usePhotoPick((blob) => onChange({ kind: 'set', blob }))

  // the new photo's preview: a local address for as long as it is staged
  const [staged, setStaged] = useState('')
  const blob = change?.kind === 'set' ? change.blob : null
  useEffect(() => {
    if (!blob) return setStaged('')
    const url = URL.createObjectURL(blob)
    setStaged(url)
    return () => URL.revokeObjectURL(url)
  }, [blob])
  const shown = change?.kind === 'set' ? staged || undefined : change?.kind === 'remove' ? undefined : photo
  const has = !!shown || change?.kind === 'set'

  return (
    <div className={styles.editor}>
      <div className={styles.preview}>
        <Avatar name={name || '?'} avatar={avatar} photo={shown} size={72} />
      </div>
      <div className={styles.actions}>
        <Button size="small" variant="secondary" icon="plus" onClick={pick.open}>
          {has ? t('comp.photo.replace') : t('comp.photo.upload')}
        </Button>
        {has && (
          <Button size="small" variant="destructive" icon="trash" onClick={() => onChange(photo ? { kind: 'remove' } : null)}>
            {t('comp.photo.remove')}
          </Button>
        )}
      </div>
      {pick.error && (
        <p className={styles.error} role="alert">
          {pick.error}
        </p>
      )}
      <div className={styles.colors} role="radiogroup" aria-label={t('profiles.avatarColor')}>
        {AVATAR_COLORS.map((a) => (
          <button
            key={a}
            type="button"
            role="radio"
            aria-checked={a === avatar}
            aria-label={t('profiles.color', { n: a.slice(5) })}
            className={styles.swatch}
            style={{ background: `var(--${a})` }}
            onClick={() => onAvatar(a)}
          />
        ))}
      </div>
      {pick.element}
    </div>
  )
}
