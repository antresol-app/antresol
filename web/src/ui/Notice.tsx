// A notice: a tinted ground with a glyph, a message and, optionally, the
// actions that answer it («Подтвердить», «Оставить старый список»). For a
// state that waits for the viewer or a problem worth reading — not for a
// passing confirmation (that is a Toast). `as="li"` puts it inside a
// GroupedSection as the group's first row.
import type { ReactNode } from 'react'
import { Icon, type IconName } from '../components/Icon'
import styles from './Notice.module.css'

export type NoticeTone = 'warning' | 'accent' | 'destructive'

export function Notice({
  tone = 'warning',
  icon = tone === 'accent' ? 'info' : 'warning',
  title,
  children,
  actions,
  as: Tag = 'div',
  className,
}: {
  tone?: NoticeTone
  icon?: IconName
  title?: ReactNode
  children?: ReactNode
  actions?: ReactNode
  as?: 'div' | 'li'
  className?: string
}) {
  return (
    <Tag className={[styles.notice, styles[tone], className].filter(Boolean).join(' ')} role={tone === 'accent' ? 'status' : 'alert'}>
      <span className={styles.glyph} aria-hidden>
        <Icon name={icon} size={20} />
      </span>
      <div className={styles.text}>
        {title && <p className={styles.title}>{title}</p>}
        {children && <div className={styles.message}>{children}</div>}
      </div>
      {actions && <div className={styles.actions}>{actions}</div>}
    </Tag>
  )
}
