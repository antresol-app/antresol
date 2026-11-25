// Chips: a few choices as capsules, each with an optional count (a playlist's
// groups, a channel's genres). The capsules are the small control height in the
// fill of a control; the chosen ones are white, as a segmented control's thumb.
//   one choice (default)  radio buttons: arrow keys and Home/End move the choice, a
//                         wide window wraps the capsules, a phone keeps one line that
//                         scrolls sideways out to the window's edges (--chips-bleed,
//                         the page's gutter): `onChange(value)`
//   multiple              toggles (aria-pressed) that always wrap, `value` is the list
//                         on: `onChange(value, on)`; `children` go after the chips
//                         (a «Ещё…» pop-up for the rest of a long list)
// Where the choices are 2–5 fixed words, use SegmentedControl instead.
import { useRef, type KeyboardEvent, type ReactNode } from 'react'
import styles from './Chips.module.css'

export interface ChipOption<T extends string> {
  value: T
  label: string
  count?: number
}

interface Common<T extends string> {
  /** The group's name for assistive tech. */
  label: string
  chips: ChipOption<T>[]
  formatCount?: (n: number) => string
}

export type ChipsProps<T extends string> = Common<T> &
  (
    | { multiple?: false; value: T; onChange: (v: T) => void; children?: undefined }
    | { multiple: true; value: readonly T[]; onChange: (v: T, on: boolean) => void; children?: ReactNode }
  )

export function Chips<T extends string>(props: ChipsProps<T>) {
  const { label, chips, formatCount = String } = props
  const ref = useRef<HTMLDivElement>(null)
  const content = (c: ChipOption<T>) => (
    <>
      <span className={styles.label}>{c.label}</span>
      {c.count !== undefined && <span className={styles.count}>{formatCount(c.count)}</span>}
    </>
  )
  if (props.multiple) {
    const { value, onChange, children } = props
    return (
      <div className={styles.chips} role="group" aria-label={label}>
        {chips.map((c) => {
          const on = value.includes(c.value)
          return (
            <button key={c.value} type="button" aria-pressed={on} className={styles.chip} onClick={() => onChange(c.value, !on)}>
              {content(c)}
            </button>
          )
        })}
        {children}
      </div>
    )
  }
  const { value, onChange } = props
  // one tab stop: the chosen chip, or the first while none is chosen
  const stop = (i: number) => (chips.some((c) => c.value === value) ? chips[i]!.value === value : i === 0)
  const move = (e: KeyboardEvent, i: number) => {
    const step = e.key === 'ArrowRight' || e.key === 'ArrowDown' ? 1 : e.key === 'ArrowLeft' || e.key === 'ArrowUp' ? -1 : 0
    const to = e.key === 'Home' ? 0 : e.key === 'End' ? chips.length - 1 : step ? (i + step + chips.length) % chips.length : -1
    if (to < 0) return
    e.preventDefault()
    onChange(chips[to]!.value)
    ref.current?.querySelectorAll<HTMLButtonElement>('[role="radio"]')[to]?.focus()
  }
  return (
    <div className={styles.scroller}>
      <div ref={ref} className={styles.chips} role="radiogroup" aria-label={label}>
        {chips.map((c, i) => {
          const on = c.value === value
          return (
            <button key={c.value} type="button" role="radio" aria-checked={on} tabIndex={stop(i) ? 0 : -1} className={styles.chip} onClick={() => onChange(c.value)} onKeyDown={(e) => move(e, i)}>
              {content(c)}
            </button>
          )
        })}
      </div>
    </div>
  )
}
