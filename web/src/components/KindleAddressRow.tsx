// «Адрес Kindle» in a profile's own «Настройки»: the profile's Send to
// Kindle address, saved when the field is left (PATCH /api/me). The mailbox
// books leave from is the server's, one for all profiles (Настройки сервера
// → Почта и уведомления); the hint says to approve it in Amazon. A kid's is
// the admin's (Профили), so the row is an adult's only.
import { useEffect, useState } from 'react'
import { errorText, updateMe } from '../api/client'
import type { User } from '../api/types'
import { useT } from '../i18n'
import { session } from '../state/session'
import { ListRow } from '../ui/List'
import { TextField } from '../ui/TextField'

export function KindleAddressRow({ user }: { user: User }) {
  const t = useT()
  const stored = user.kindleEmail ?? ''
  const [value, setValue] = useState(stored)
  const [error, setError] = useState('')
  const [saved, setSaved] = useState(false)
  useEffect(() => setValue(stored), [stored])
  const save = () => {
    const v = value.trim()
    if (v === stored) return
    setError('')
    setSaved(false)
    updateMe({ kindleEmail: v }).then(
      (u) => {
        session.updateUser(u)
        setSaved(true)
      },
      (e: unknown) => setError(errorText(e, t('settings.saveFailed'))),
    )
  }
  return (
    <ListRow wide label={t('prefs.kindle')} hint={t('prefs.kindle.hint')} status={saved ? t('prefs.kindle.saved') : undefined}>
      <TextField
        label={t('prefs.kindle')}
        hideLabel
        type="email"
        value={value}
        placeholder="name_123@kindle.com"
        onChange={(e) => {
          setValue(e.target.value)
          setError('')
          setSaved(false)
        }}
        onBlur={save}
        onKeyDown={(e) => e.key === 'Enter' && save()}
        error={error || null}
        autoComplete="off"
        spellCheck={false}
        autoCapitalize="off"
      />
    </ListRow>
  )
}
