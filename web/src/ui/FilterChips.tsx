// One choice of many with counts: `Chips` with its default. Kept under this
// name for the pages and addons written against it.
import { Chips, type ChipOption } from './Chips'

export type Chip<T extends string> = ChipOption<T>

export function FilterChips<T extends string>({ label, chips, value, onChange, formatCount }: { label: string; chips: Chip<T>[]; value: T; onChange: (v: T) => void; formatCount?: (n: number) => string }) {
  return <Chips label={label} chips={chips} value={value} onChange={onChange} formatCount={formatCount} />
}
