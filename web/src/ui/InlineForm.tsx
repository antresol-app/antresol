// One text field and its button on one line (link an account by its
// nickname): the field takes the room, the button keeps its width, both at
// a button's height. The field's label is its placeholder and its
// accessible name, so the row's own label and hint say what it is for and
// the field lines up with them. Below 700 px the button goes under the
// field, as wide as it.
import type { FormEvent } from 'react'
import { Button } from './Button'
import { TextField, type TextFieldProps } from './TextField'
import styles from './InlineForm.module.css'

export interface InlineFormProps {
  /** The field (its label is the placeholder and the accessible name). */
  field: Omit<TextFieldProps, 'size' | 'hideLabel'>
  /** The button's label («Связать»). */
  submit: string
  /** Submitted only when the field is not blank. */
  onSubmit: () => void
  loading?: boolean
  disabled?: boolean
  className?: string
}

export function InlineForm({ field, submit, onSubmit, loading, disabled, className }: InlineFormProps) {
  const blank = typeof field.value === 'string' ? !field.value.trim() : field.value == null
  const send = (e: FormEvent) => {
    e.preventDefault()
    if (!blank && !disabled && !loading) onSubmit()
  }
  return (
    <form className={[styles.form, className].filter(Boolean).join(' ')} onSubmit={send}>
      <TextField {...field} placeholder={field.placeholder ?? field.label} hideLabel size="medium" className={[styles.field, field.className].filter(Boolean).join(' ')} />
      <Button type="submit" loading={loading} disabled={disabled || blank} className={styles.submit}>
        {submit}
      </Button>
    </form>
  )
}
