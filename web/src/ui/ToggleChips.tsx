// Several choices on at once (a channel's genres, countries, decades): `Chips`
// with `multiple`. Kept under this name for the pages and addons written
// against it. `children` go after the chips (a «Ещё…» pop-up for the rest of a
// long list).
import type { ReactNode } from 'react'
import { Chips, type ChipOption } from './Chips'

export type ToggleChip<T extends string> = ChipOption<T>

export function ToggleChips<T extends string>({
  label,
  chips,
  value,
  onToggle,
  children,
  formatCount,
}: {
  label: string
  chips: ToggleChip<T>[]
  value: readonly T[]
  onToggle: (v: T, on: boolean) => void
  children?: ReactNode
  formatCount?: (n: number) => string
}) {
  return (
    <Chips multiple label={label} chips={chips} value={value} onChange={onToggle} formatCount={formatCount}>
      {children}
    </Chips>
  )
}
