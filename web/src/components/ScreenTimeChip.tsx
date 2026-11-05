// The kid's «Экранное время» on the home screen: a small chip with the
// time left today, or the kid-friendly message when it is over (or outside
// the allowed hours). Nothing for other profiles or when nothing limits it.
import { useSession } from '../state/session'
import { useScreenTime, videoLeft } from '../state/screenTime'
import { humanMinutes } from '../player/timeline'
import { useT } from '../i18n'
import { Icon } from './Icon'
import styles from './ScreenTimeChip.module.css'

export function ScreenTimeChip() {
  const t = useT()
  const { user } = useSession()
  const v = videoLeft(useScreenTime(Boolean(user?.isKid)))
  if (!v) return null
  if (!v.allowed) {
    return (
      <p className={`${styles.chip} ${styles.over}`} role="status">
        <Icon name="moon" size={16} />
        <span>{v.message}</span>
      </p>
    )
  }
  const time = humanMinutes(Math.max(1, Math.round((v.remainingSec ?? 0) / 60)))
  return (
    <p className={styles.chip} role="status" aria-label={t('comp.screen.leftAria', { time })}>
      <Icon name="clock" size={16} />
      <span aria-hidden>{t('comp.screen.left', { time })}</span>
    </p>
  )
}
