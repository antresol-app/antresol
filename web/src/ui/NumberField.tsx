// A number with its unit, as in System Settings («2 МБ/с», «10 ГБ»): one
// filled field of the small control height, the number and the unit on one
// line of text (one baseline), centred on their capitals like every control
// label; tabular digits. The field is as wide as the number it shows (never
// narrower than `chars` digits), so «2 МБ/с» is no wider than it needs. It
// keeps what is typed (it may be empty or «0,» for a moment), reports every
// valid value (≥ `min`, ≤ `max`) and, left with something invalid, shows the
// saved value again. A comma is a decimal point; `decimals` 0 (default)
// takes whole numbers.
import { useEffect, useId, useRef, useState } from 'react'
import styles from './NumberField.module.css'

/** Parses «2», «0,5», «1.5»; NaN for empty or partial input. */
export function parseNumber(text: string): number {
  const t = text.trim().replace(',', '.')
  return t === '' || t === '.' ? NaN : Number(t)
}

export interface NumberFieldProps {
  /** The accessible name (the row's label says it on screen). */
  label: string
  value: number
  onChange: (v: number) => void
  /** «ГБ», «МБ/с»: after the number, inside the field. */
  unit?: string
  min?: number
  max?: number
  /** Digits after the point it takes (and shows). */
  decimals?: number
  /** The narrowest the number gets, in digits (it grows with what it shows). */
  chars?: number
  disabled?: boolean
  id?: string
  className?: string
}

export function NumberField({ label, value, onChange, unit, min = 0, max = Infinity, decimals = 0, chars = 1, disabled, id, className }: NumberFieldProps) {
  const shown = (v: number) => (Number.isFinite(v) ? String(+v.toFixed(decimals)).replace('.', ',') : '')
  const [text, setText] = useState(() => shown(value))
  const editing = useRef(false)
  const input = useRef<HTMLInputElement>(null)
  const auto = useId()
  // follow the saved value while the field is not being edited
  useEffect(() => {
    if (!editing.current) setText(shown(value))
  }, [value])
  const allowed = decimals > 0 ? /^\d*[.,]?\d*$/ : /^\d*$/
  const valid = (v: number) => Number.isFinite(v) && v >= min && v <= max
  return (
    <span
      className={[styles.field, disabled ? styles.disabled : '', className].filter(Boolean).join(' ')}
      style={{ ['--nf-chars' as string]: Math.max(chars, text.length), ['--nf-min' as string]: chars }}
      // a press on the unit or the finger's margin around the field types into it
      onMouseDown={(e) => {
        if (disabled || e.target === input.current) return
        e.preventDefault()
        input.current?.focus()
      }}
    >
      <span className={styles.text}>
        <input
          ref={input}
          id={id ?? auto}
          type="text"
          inputMode={decimals > 0 ? 'decimal' : 'numeric'}
          autoComplete="off"
          className={styles.input}
          aria-label={label}
          value={text}
          disabled={disabled}
          onFocus={() => (editing.current = true)}
          onChange={(e) => {
            const raw = e.target.value
            if (!allowed.test(raw)) return
            const [, frac = ''] = raw.split(/[.,]/)
            if (frac.length > decimals) return
            setText(raw)
            const v = parseNumber(raw)
            if (valid(v)) onChange(v)
          }}
          onBlur={() => {
            editing.current = false
            if (!valid(parseNumber(text))) setText(shown(value))
          }}
        />
        {unit && (
          <span className={styles.unit} aria-hidden>
            {unit}
          </span>
        )}
      </span>
    </span>
  )
}
