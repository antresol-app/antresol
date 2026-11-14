// Toggle chips: several choices on at once (a channel's genres, countries,
// decades), each a capsule that turns white when on, with an optional
// count. The look of FilterChips; they always wrap. `children` go after
// the chips (a «Ещё…» pop-up for the rest of a long list).
import type { ReactNode } from 'react'
import styles from './ToggleChips.module.css'

export interface ToggleChip<T extends string> {
  value: T
  label: string
  count?: number
}

export function ToggleChips<T extends string>({
  label,
  chips,
  value,
  onToggle,
  children,
  formatCount = String,
}: {
  label: string
  chips: ToggleChip<T>[]
  value: readonly T[]
  onToggle: (v: T, on: boolean) => void
  children?: ReactNode
  formatCount?: (n: number) => string
}) {
  return (
    <div className={styles.chips} role="group" aria-label={label}>
      {chips.map((c) => {
        const on = value.includes(c.value)
        return (
          <button key={c.value} type="button" aria-pressed={on} className={styles.chip} onClick={() => onToggle(c.value, !on)}>
            <span className={styles.label}>{c.label}</span>
            {c.count !== undefined && <span className={styles.count}>{formatCount(c.count)}</span>}
          </button>
        )
      })}
      {children}
    </div>
  )
}
