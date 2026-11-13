// The System Settings list. GroupedSection: a section's name over a rounded
// glass group of rows, an optional footnote under it. ListRow: one setting
// or one item — optional leading media (a poster, an avatar), the label and
// its hint on the left, the control or the trailing actions on the right.
//   stack — the control goes under the text on phones (a segmented control)
//   wide  — always under the text (an ordered list, a form)
// ListLinkRow: a row that opens something (a sheet, a page), as a System
// Settings row with a chevron: the whole row is the button.
import type { ReactNode } from 'react'
import { Icon, type IconName } from '../components/Icon'
import styles from './List.module.css'

export function GroupedSection({ title, footer, children, id, className }: { title?: ReactNode; footer?: ReactNode; children: ReactNode; id?: string; className?: string }) {
  return (
    <section className={[styles.section, className].filter(Boolean).join(' ')} aria-labelledby={title && id ? id : undefined}>
      {title && (
        <h3 className={styles.title} id={id}>
          {title}
        </h3>
      )}
      <ul className={styles.group}>{children}</ul>
      {footer && <p className={styles.foot}>{footer}</p>}
    </section>
  )
}

export interface ListRowProps {
  label: ReactNode
  hint?: ReactNode
  /** A live line under the hint («Сохранено», a scan's result). */
  status?: ReactNode
  /** A poster, an avatar or an icon before the text. */
  leading?: ReactNode
  /** The control, or the trailing actions. */
  children?: ReactNode
  /** The label's id (a control's aria-labelledby). */
  id?: string
  stack?: boolean
  wide?: boolean
  className?: string
}

export function ListRow({ label, hint, status, leading, children, id, stack, wide, className }: ListRowProps) {
  return (
    <li className={[styles.row, stack ? styles.stack : '', wide ? styles.wide : '', className].filter(Boolean).join(' ')}>
      {leading && <div className={styles.leading}>{leading}</div>}
      <div className={styles.text}>
        <div className={styles.label} id={id}>
          {label}
        </div>
        {hint && <p className={styles.hint}>{hint}</p>}
        {status && (
          <p className={styles.hint} role="status">
            {status}
          </p>
        )}
      </div>
      {children != null && children !== false && <div className={styles.control}>{children}</div>}
    </li>
  )
}

export interface ListLinkRowProps {
  label: ReactNode
  /** One short line under the label. */
  hint?: ReactNode
  /** The current choice, dimmed before the chevron («Включены», «2»). */
  value?: ReactNode
  /** A white icon on a coloured tile before the text, as System Settings has. */
  icon?: IconName
  /** The tile's colour: a palette token (`var(--blue)`). */
  tint?: string
  onOpen: () => void
  className?: string
}

export function ListLinkRow({ label, hint, value, icon, tint, onOpen, className }: ListLinkRowProps) {
  return (
    <li className={[styles.row, styles.link, className].filter(Boolean).join(' ')}>
      <button type="button" className={styles.linkButton} onClick={onOpen}>
        {icon && (
          <span className={styles.tile} style={{ background: tint ?? 'var(--gray)' }} aria-hidden>
            <Icon name={icon} size={16} strokeWidth={2.2} />
          </span>
        )}
        <span className={styles.text}>
          <span className={styles.label}>{label}</span>
          {hint && <span className={styles.hint}>{hint}</span>}
        </span>
        {value != null && value !== '' && <span className={styles.value}>{value}</span>}
        <Icon name="chevronRight" size={16} className={styles.chevron} />
      </button>
    </li>
  )
}
