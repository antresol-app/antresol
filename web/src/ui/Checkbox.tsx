// A checkbox with its label: a choice that changes what an action will do
// (a confirmation's «Удалить навсегда, минуя корзину»), not a setting that
// applies at once (that is a Toggle). The native input, so the keyboard,
// forms and assistive tech get a real checkbox; the label is its name and a
// finger's 44 pt target.
import { useId, type ReactNode } from 'react'
import styles from './Checkbox.module.css'

export interface CheckboxProps {
  label: ReactNode
  checked: boolean
  onChange: (on: boolean) => void
  disabled?: boolean
  /** destructive: the label turns red while it is checked (it makes the action irreversible). */
  tone?: 'default' | 'destructive'
  className?: string
}

export function Checkbox({ label, checked, onChange, disabled, tone = 'default', className }: CheckboxProps) {
  const id = useId()
  return (
    <label htmlFor={id} className={[styles.checkbox, tone === 'destructive' ? styles.destructive : '', className].filter(Boolean).join(' ')} data-disabled={disabled || undefined}>
      <input id={id} type="checkbox" className={styles.box} checked={checked} disabled={disabled} onChange={(e) => onChange(e.target.checked)} />
      <span className={styles.label}>{label}</span>
    </label>
  )
}
