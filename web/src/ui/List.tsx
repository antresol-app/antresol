// The System Settings list. GroupedSection: a section's name over a rounded
// glass group of rows, an optional footnote under it. ListRow: one setting
// or one item — optional leading media (a poster, an avatar), the label and
// its hint on the left, the control or the trailing actions on the right.
//   stack — the control goes under the text on phones (a segmented control)
//   wide  — always under the text (an ordered list, a form)
//   shrink — the control gives way: a pop-up in it cuts its choice with an
//            ellipsis (at most 40% of a wide row) before the label would
//            break inside a word
// On a phone a plain row (none of these) keeps its text at least
// --row-text-min wide: a control that would squeeze it goes under it.
// GroupedSection `dimmed`: the labels and hints go to the tertiary label, as a
// settings group does while the switch it depends on is off.
// ListLinkRow: a row that opens something (a sheet, a page), as a System
// Settings row with a chevron: the whole row is the button — or, with `to`,
// a link to an address of the app (a list's level: Back returns to it).
// ListSelectRow: a row that chooses (a source, a device, a playlist to add
// to): the link row's button with the choice's check at the trailing edge.
import type { ReactNode } from 'react'
import { Link } from 'react-router'
import { Icon } from '../components/Icon'
import { SettingsIcon, type SettingsGlyph, type SettingsTint } from './SettingsIcon'
import styles from './List.module.css'

export function GroupedSection({
  title,
  footer,
  children,
  id,
  className,
  dimmed,
}: {
  title?: ReactNode
  footer?: ReactNode
  children: ReactNode
  id?: string
  className?: string
  /** The rows' text is dimmed: what they set does nothing now (their controls are disabled by the caller). */
  dimmed?: boolean
}) {
  return (
    <section className={[styles.section, dimmed ? styles.dimmed : '', className].filter(Boolean).join(' ')} aria-labelledby={title && id ? id : undefined}>
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
  /** The control gives way to the text (a pop-up with a long choice). */
  shrink?: boolean
  className?: string
}

export function ListRow({ label, hint, status, leading, children, id, stack, wide, shrink, className }: ListRowProps) {
  return (
    <li className={[styles.row, stack ? styles.stack : '', wide ? styles.wide : '', shrink ? styles.shrink : '', className].filter(Boolean).join(' ')}>
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
  /** The value is a status: a dot in the tone before it, and the tone for a problem's words
   * (success: a dot and the dimmed word; neutral: a grey dot; warning, destructive: the dot and the word). */
  valueTone?: 'success' | 'neutral' | 'warning' | 'destructive'
  /** A white glyph on a coloured tile before the text, as System Settings has (ui/SettingsIcon). */
  icon?: SettingsGlyph
  /** The tile's colour, a palette name. */
  tint?: SettingsTint
  /** Something else before the text in place of the tile (a channel's logo). */
  leading?: ReactNode
  onOpen?: () => void
  /** The row is a link to this address of the app (no onOpen needed). */
  to?: string
  className?: string
}

export function ListLinkRow({ label, hint, value, valueTone, icon, tint, leading, onOpen, to, className }: ListLinkRowProps) {
  const inner = (
    <>
      {icon ? <SettingsIcon glyph={icon} tint={tint ?? 'gray'} /> : leading}
      <span className={styles.text}>
        <span className={styles.label}>{label}</span>
        {hint && <span className={styles.hint}>{hint}</span>}
      </span>
      {value != null && value !== '' && (
        <span className={styles.value} data-tone={valueTone}>
          {value}
        </span>
      )}
      <Icon name="chevronRight" size={16} className={styles.chevron} />
    </>
  )
  return (
    <li className={[styles.row, styles.link, className].filter(Boolean).join(' ')}>
      {to !== undefined ? (
        <Link to={to} className={styles.linkButton} onClick={onOpen}>
          {inner}
        </Link>
      ) : (
        <button type="button" className={styles.linkButton} onClick={onOpen}>
          {inner}
        </button>
      )}
    </li>
  )
}

export interface ListSelectRowProps {
  label: ReactNode
  /** One short line under the label. */
  hint?: ReactNode
  /** A picture, an icon or a badge before the text. */
  leading?: ReactNode
  /** The chosen one of the rows beside it: the check shows and the row reads as pressed.
   * Leave it out for a row that only acts («Другой источник…»), which has no check. */
  selected?: boolean
  disabled?: boolean
  /** An action row's word, as Settings has them: «Добавить модель…», «Проверить» (accent), «Удалить» (destructive). */
  tone?: 'accent' | 'destructive'
  onSelect: () => void
  className?: string
}

export function ListSelectRow({ label, hint, leading, selected, disabled, tone, onSelect, className }: ListSelectRowProps) {
  return (
    <li className={[styles.row, styles.link, tone ? styles[tone] : '', className].filter(Boolean).join(' ')}>
      <button type="button" className={styles.linkButton} aria-pressed={selected} disabled={disabled} onClick={onSelect}>
        {leading}
        <span className={styles.text}>
          <span className={styles.label}>{label}</span>
          {hint && <span className={styles.hint}>{hint}</span>}
        </span>
        {selected !== undefined && (
          <span className={styles.check} aria-hidden>
            {selected && <Icon name="check" size={16} strokeWidth={2.4} />}
          </span>
        )}
      </button>
    </li>
  )
}
