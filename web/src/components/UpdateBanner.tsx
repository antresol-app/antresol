// «Вышла новая версия»: an administrator's banner in the window's bottom corner when a
// newer Antresol release is out (state/updateCheck: asked at most every six
// hours), with «Подробнее» to «Состояние» (/manage/health), where the
// update's steps are, and «Скрыть» for this release on this device. Not on
// that page itself.
import { useEffect, useState } from 'react'
import { useLocation } from 'react-router'
import { useT } from '../i18n'
import { useSession } from '../state/session'
import { hideRelease, newerRelease } from '../state/updateCheck'
import { Button, ButtonLink } from '../ui/Button'
import { Notice } from '../ui/Notice'
import styles from './UpdateBanner.module.css'

const HEALTH = '/manage/health'

export function UpdateBanner() {
  const t = useT()
  const { user } = useSession()
  const { pathname } = useLocation()
  const [tag, setTag] = useState('')
  const admin = Boolean(user?.isAdmin)
  useEffect(() => {
    if (!admin) return
    const ac = new AbortController()
    newerRelease(Date.now(), ac.signal).then(setTag, () => undefined)
    return () => ac.abort()
  }, [admin])
  if (!admin || !tag || pathname.startsWith(HEALTH)) return null
  const hide = () => {
    hideRelease(tag)
    setTag('')
  }
  return (
    <div className={styles.banner}>
      <Notice
        tone="accent"
        icon="sparkles"
        title={t('update.banner.title', { version: tag })}
        actions={
          <>
            <ButtonLink to={HEALTH} size="small">
              {t('update.banner.details')}
            </ButtonLink>
            <Button variant="plain" size="small" onClick={hide}>
              {t('update.banner.hide')}
            </Button>
          </>
        }
      >
        {t('update.banner.message', { page: t('manage.section.health') })}
      </Notice>
    </div>
  )
}
