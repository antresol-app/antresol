// A checkbox with its label: a choice that changes what an action will do
// (a confirmation's «Удалить навсегда, минуя корзину»), not a setting that
// applies at once (that is a Toggle). The native input, so the keyboard,
// forms and assistive tech get a real checkbox; the label is its name and a
// finger's 44 pt target.
//   description    a line of what it does, under the label (footnote, secondary)
//   indeterminate  some of what it stands for are chosen (a group's box over its
//                  rows): the dash; a press makes it checked, the caller decides
//   no label       a box in a table's row: `aria-label` names it, and the row it
//                  sits in is the visible label
import { useEffect, useId, useRef, type ReactNode } from 'react'
import styles from './Checkbox.module.css'

interface Common {
  checked: boolean
  onChange: (on: boolean) => void
  disabled?: boolean
  indeterminate?: boolean
  description?: ReactNode
  /** destructive: the label turns red while it is checked (it makes the action irreversible). */
  tone?: 'default' | 'destructive'
  className?: string
}

export type CheckboxProps = Common & ({ label: ReactNode; 'aria-label'?: string } | { label?: undefined; 'aria-label': string })

export function Checkbox({ label, checked, onChange, disabled, indeterminate, description, tone = 'default', className, 'aria-label': ariaLabel }: CheckboxProps) {
  const id = useId()
  const input = useRef<HTMLInputElement>(null)
  // the browser clears `indeterminate` on every press, so it is set again after every render
  useEffect(() => {
    if (input.current) input.current.indeterminate = Boolean(indeterminate)
  })
  return (
    <label htmlFor={id} className={[styles.checkbox, tone === 'destructive' ? styles.destructive : '', className].filter(Boolean).join(' ')} data-disabled={disabled || undefined}>
      <input
        ref={input}
        id={id}
        type="checkbox"
        className={styles.box}
        checked={checked}
        disabled={disabled}
        aria-label={label === undefined ? ariaLabel : undefined}
        // the name is the label alone: the wrapping <label> would add the description to it
        aria-labelledby={label !== undefined && description ? `${id}-l` : undefined}
        aria-describedby={description ? `${id}-d` : undefined}
        onChange={(e) => {
          onChange(e.target.checked)
          // a caller that refuses the press renders nothing new, so the dash is put back here
          e.target.indeterminate = Boolean(indeterminate)
        }}
      />
      {(label !== undefined || description) && (
        <span className={styles.text}>
          {label !== undefined && (
            <span id={`${id}-l`} className={styles.label}>
              {label}
            </span>
          )}
          {description && (
            <span id={`${id}-d`} className={styles.description}>
              {description}
            </span>
          )}
        </span>
      )}
    </label>
  )
}
