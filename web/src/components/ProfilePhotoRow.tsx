// «Фото профиля» in a profile's own settings (pages/ProfileSettings): the
// photo beside its name, «Загрузить фото…» / «Заменить…» and «Убрать фото».
// A settings row applies at once (no Save): the framed square goes up
// (POST /api/me/photo) and the profile that comes back replaces the session's.
// An administrator sets a kid's in «Профили» (AvatarEditor); a kid has no row.
import { useState } from 'react'
import { errorText, removeMyPhoto, setMyPhoto } from '../api/client'
import type { User } from '../api/types'
import { Button } from '../ui/Button'
import { ListRow } from '../ui/List'
import { useT } from '../i18n'
import { Avatar } from './Avatar'
import { usePhotoPick } from './PhotoPick'
import styles from './ProfilePhotoRow.module.css'

export interface ProfilePhotoRowProps {
  user: Pick<User, 'name' | 'avatar' | 'photo'>
  /** The profile as the server has it now (the caller keeps it in the session). */
  onUser: (u: User) => void
}

export function ProfilePhotoRow({ user, onUser }: ProfilePhotoRowProps) {
  const t = useT()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const run = async (change: () => Promise<User>) => {
    setBusy(true)
    setError('')
    try {
      onUser(await change())
    } catch (e) {
      setError(errorText(e, t('comp.photo.saveFailed')))
    } finally {
      setBusy(false)
    }
  }
  const pick = usePhotoPick((blob) => void run(() => setMyPhoto(blob)))

  return (
    <ListRow stack leading={<Avatar name={user.name} avatar={user.avatar} photo={user.photo} size={44} />} label={t('comp.photo.label')} hint={t('comp.photo.rowHint')} status={error || pick.error || undefined}>
      <div className={styles.buttons}>
        <Button size="small" variant="secondary" icon="plus" loading={busy} disabled={busy} onClick={pick.open}>
          {user.photo ? t('comp.photo.replace') : t('comp.photo.upload')}
        </Button>
        {user.photo && (
          <Button size="small" variant="destructive" icon="trash" disabled={busy} onClick={() => void run(removeMyPhoto)}>
            {t('comp.photo.remove')}
          </Button>
        )}
      </div>
      {pick.element}
    </ListRow>
  )
}
