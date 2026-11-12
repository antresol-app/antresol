// Filter chips: one choice out of many (a playlist's groups), each with
// its count. Capsules of the small control height in the fill of a
// control; the chosen one is white, as a segmented control's thumb. A wide
// window wraps them; a phone keeps one line that scrolls sideways out to
// the window's edges (--chips-bleed, the page's gutter). Where the
// choices are 2–5 fixed words, use SegmentedControl instead.
import { useRef, type KeyboardEvent } from 'react'
import styles from './FilterChips.module.css'

export interface Chip<T extends string> {
  value: T
  label: string
  count?: number
}

export function FilterChips<T extends string>({ label, chips, value, onChange, formatCount = String }: { label: string; chips: Chip<T>[]; value: T; onChange: (v: T) => void; formatCount?: (n: number) => string }) {
  const ref = useRef<HTMLDivElement>(null)
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
            <button key={c.value} type="button" role="radio" aria-checked={on} tabIndex={on ? 0 : -1} className={styles.chip} onClick={() => onChange(c.value)} onKeyDown={(e) => move(e, i)}>
              <span className={styles.label}>{c.label}</span>
              {c.count !== undefined && <span className={styles.count}>{formatCount(c.count)}</span>}
            </button>
          )
        })}
      </div>
    </div>
  )
}
