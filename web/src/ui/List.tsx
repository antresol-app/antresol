// The System Settings list. GroupedSection: a section's name over a rounded
// glass group of rows, an optional footnote under it. ListRow: one setting
// or one item — optional leading media (a poster, an avatar), the label and
// its hint on the left, the control or the trailing actions on the right.
//   stack — the control goes under the text on phones (a segmented control)
//   wide  — always under the text (an ordered list, a form)
import type { ReactNode } from 'react'
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
