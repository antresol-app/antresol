// Labelled text input in the Apple style: filled, borderless, the field height, with an
// optional show/hide toggle for passwords and an inline error.
import { forwardRef, useId, useState, type InputHTMLAttributes } from 'react'
import { useT } from '../i18n'
import { IconButton } from './IconButton'
import styles from './TextField.module.css'

export interface TextFieldProps extends Omit<InputHTMLAttributes<HTMLInputElement>, 'size'> {
  label: string
  /** Visually hide the label (still the accessible name). */
  hideLabel?: boolean
  hint?: string
  error?: string | null
  /** Adds a show/hide button to password fields. */
  revealable?: boolean
  /** small: the small control height (a search field in a menu). */
  size?: 'regular' | 'small'
}

export const TextField = forwardRef<HTMLInputElement, TextFieldProps>(function TextField(
  { label, hideLabel, hint, error, revealable, size = 'regular', type = 'text', className, id, ...rest },
  ref,
) {
  const autoId = useId()
  const inputId = id ?? autoId
  const hintId = `${inputId}-hint`
  const errId = `${inputId}-err`
  const t = useT()
  const [reveal, setReveal] = useState(false)
  const actualType = revealable && type === 'password' && reveal ? 'text' : type
  const describedBy = [error ? errId : '', hint ? hintId : ''].filter(Boolean).join(' ') || undefined

  return (
    <div className={[styles.field, error ? styles.invalid : '', size === 'small' ? styles.small : '', className].filter(Boolean).join(' ')}>
      <label htmlFor={inputId} className={hideLabel ? 'visually-hidden' : styles.label}>
        {label}
      </label>
      <div className={styles.control}>
        <input
          ref={ref}
          id={inputId}
          type={actualType}
          className={styles.input}
          aria-invalid={error ? true : undefined}
          aria-describedby={describedBy}
          {...rest}
        />
        {revealable && type === 'password' && (
          <IconButton
            variant="plain"
            size="sm"
            icon={reveal ? 'eyeSlash' : 'eye'}
            className={styles.reveal}
            label={reveal ? t('comp.textField.hidePassword') : t('comp.textField.showPassword')}
            aria-pressed={reveal}
            onClick={() => setReveal((v) => !v)}
          />
        )}
      </div>
      {error ? (
        <p id={errId} className={styles.error} role="alert">
          {error}
        </p>
      ) : hint ? (
        <p id={hintId} className={styles.hint}>
          {hint}
        </p>
      ) : null}
    </div>
  )
})
