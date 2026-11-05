// Placeholder for sections that later tasks fill in (Фильмы, Сериалы, Поиск …).
import { APP_NAME } from '../brand'
import { useEffect, type ReactNode } from 'react'
import { Icon, type IconName } from '../components/Icon'
import styles from './ComingSoon.module.css'
import { useT } from '../i18n'

export function ComingSoon({ title, icon, text, children }: { title: string; icon: IconName; text?: string; children?: ReactNode }) {
  const t = useT()
  useEffect(() => {
    document.title = `${title} — ${APP_NAME}`
  }, [title])
  return (
    <div className={styles.page}>
      <h1 className={styles.title}>{title}</h1>
      <div className={styles.body}>
        <Icon name={icon} size={52} strokeWidth={1.3} />
        <p className={styles.headline}>{t('browse.comingSoon')}</p>
        <p className={styles.text}>{text ?? t('browse.comingSoonText')}</p>
        {children && <div className={styles.actions}>{children}</div>}
      </div>
    </div>
  )
}
