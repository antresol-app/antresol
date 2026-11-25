// Labelled text input in the Apple style: filled, borderless, the field height, with an
// optional show/hide toggle for passwords and an inline error. `type="search"`
// is the search field: a magnifier before the text and, with `onClear`, a
// clear button (and Escape) once something is typed. `type="time"` is a
// time of day in the same field (the browser's own picker, even digits).
// TextArea: the same field for several lines (a note, a list of addresses);
// it grows with a drag of its corner, never sideways.
import { forwardRef, useId, useState, type InputHTMLAttributes, type TextareaHTMLAttributes } from 'react'
import { useT } from '../i18n'
import { Icon } from '../components/Icon'
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
  /** small: the small control height (a search field in a menu or a toolbar);
   * medium: a button's height (a field beside its button, InlineForm). */
  size?: 'regular' | 'medium' | 'small'
  /** A search field's clear button (×) and Escape: empty the field. */
  onClear?: () => void
  /** The clear button's name (default «Очистить»). */
  clearLabel?: string
}

/** The ids of the line under a field, for the input's aria-describedby. */
const describedBy = (id: string, error?: string | null, hint?: string) => [error ? `${id}-err` : '', hint ? `${id}-hint` : ''].filter(Boolean).join(' ') || undefined

/** The line under a field: its error (read out), else its hint. */
function FieldMessage({ id, error, hint }: { id: string; error?: string | null; hint?: string }) {
  if (error) {
    return (
      <p id={`${id}-err`} className={styles.error} role="alert">
        {error}
      </p>
    )
  }
  return hint ? (
    <p id={`${id}-hint`} className={styles.hint}>
      {hint}
    </p>
  ) : null
}

export const TextField = forwardRef<HTMLInputElement, TextFieldProps>(function TextField(
  { label, hideLabel, hint, error, revealable, size = 'regular', type = 'text', className, id, onClear, clearLabel, onKeyDown, ...rest },
  ref,
) {
  const autoId = useId()
  const inputId = id ?? autoId
  const t = useT()
  const [reveal, setReveal] = useState(false)
  const actualType = revealable && type === 'password' && reveal ? 'text' : type
  const search = type === 'search'
  const filled = rest.value !== undefined && rest.value !== ''

  return (
    <div
      className={[styles.field, error ? styles.invalid : '', size === 'small' ? styles.small : '', size === 'medium' ? styles.medium : '', search ? styles.search : '', className]
        .filter(Boolean)
        .join(' ')}
    >
      <label htmlFor={inputId} className={hideLabel ? 'visually-hidden' : styles.label}>
        {label}
      </label>
      <div className={styles.control}>
        {search && <Icon name="search" size={size === 'small' ? 14 : 20} strokeWidth={size === 'small' ? 2.1 : 2} className={styles.glass} />}
        <input
          ref={ref}
          id={inputId}
          type={actualType}
          className={styles.input}
          aria-invalid={error ? true : undefined}
          aria-describedby={describedBy(inputId, error, hint)}
          onKeyDown={(e) => {
            if (e.key === 'Escape' && onClear && filled) {
              e.preventDefault()
              e.stopPropagation()
              onClear()
            }
            onKeyDown?.(e)
          }}
          {...rest}
        />
        {search && onClear && filled && (
          <button type="button" className={styles.clear} aria-label={clearLabel ?? t('search.clear')} onClick={onClear}>
            <Icon name="close" size={size === 'small' ? 10 : 12} strokeWidth={size === 'small' ? 3 : 2.8} />
          </button>
        )}
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
      <FieldMessage id={inputId} error={error} hint={hint} />
    </div>
  )
})

export interface TextAreaProps extends TextareaHTMLAttributes<HTMLTextAreaElement> {
  label: string
  /** Visually hide the label (still the accessible name). */
  hideLabel?: boolean
  hint?: string
  error?: string | null
}

export const TextArea = forwardRef<HTMLTextAreaElement, TextAreaProps>(function TextArea({ label, hideLabel, hint, error, rows = 4, className, id, ...rest }, ref) {
  const autoId = useId()
  const inputId = id ?? autoId
  return (
    <div className={[styles.field, error ? styles.invalid : '', className].filter(Boolean).join(' ')}>
      <label htmlFor={inputId} className={hideLabel ? 'visually-hidden' : styles.label}>
        {label}
      </label>
      <textarea ref={ref} id={inputId} rows={rows} className={[styles.input, styles.multiline].join(' ')} aria-invalid={error ? true : undefined} aria-describedby={describedBy(inputId, error, hint)} {...rest} />
      <FieldMessage id={inputId} error={error} hint={hint} />
    </div>
  )
})
