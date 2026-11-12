// An ordered list of names, the way iOS and macOS Settings edit one
// (ui/SortableList: each row has its name, a remove button and a drag
// handle (≡) that drags with a mouse or a finger; ↑/↓ (Home/End) on the
// handle move the row and the new place is read out). «+ Добавить…» under the list opens a menu of
// what can still be added — and, with `field`, a text field for any name
// (studios). The first row is the main one; the caller's hint says so.
// «Мои языки», «Озвучка», «Любимые озвучки», «Звук и субтитры» use it.
import { useRef, useState } from 'react'
import { Button } from '../ui/Button'
import { MenuItem, MenuSeparator, Menu } from '../ui/Menu'
import { SortableList } from '../ui/SortableList'
import { TextField } from '../ui/TextField'
import styles from './RankedList.module.css'

export interface RankedChoice {
  value: string
  label: string
}

export interface RankedListProps {
  /** The list's accessible name (or `labelledBy`). */
  label?: string
  labelledBy?: string
  items: string[]
  /** How an item reads (a language code → «Русский»); the item itself by default. */
  itemLabel?: (v: string) => string
  /** These many rows always stay (their remove button is off). */
  min?: number
  /** No «Добавить» once the list has this many. */
  max?: number
  /** A reorder or a removal. */
  onChange: (items: string[]) => void
  /** What the add menu offers (not in the list yet), for what is typed in `field`. */
  choices: (query: string) => RankedChoice[]
  /** A choice of the menu, or the field's text, was added. */
  onAdd: (value: string) => void
  /** «Добавить язык»: the button and the menu's name. */
  addLabel: string
  /** A text field at the top of the add menu: any name, added on Enter. */
  field?: { label: string; maxLength?: number }
  /** Shown in place of an empty list. */
  empty?: string
}

export { moveItem } from '../ui/SortableList'

export function RankedList({ label, labelledBy, items, itemLabel = (v) => v, min = 0, max, onChange, choices, onAdd, addLabel, field, empty }: RankedListProps) {
  const addRef = useRef<HTMLButtonElement>(null)
  const [query, setQuery] = useState('')

  const offered = choices(query)
  const full = max !== undefined && items.length >= max
  const canAdd = !full && (field !== undefined || choices('').length > 0)

  return (
    <div className={styles.box}>
      <SortableList
        label={label}
        labelledBy={labelledBy}
        items={items}
        getKey={(v) => v}
        itemLabel={itemLabel}
        renderItem={(v) => <span className={styles.name}>{itemLabel(v)}</span>}
        onChange={onChange}
        removable
        min={min}
        empty={empty && <p className={styles.empty}>{empty}</p>}
        focusWhenEmpty={() => addRef.current}
      />
      {canAdd && (
        <Menu
          label={addLabel}
          placement="bottom-start"
          focusItem={!field}
          onOpenChange={(open) => !open && setQuery('')}
          trigger={(p) => (
            <Button
              {...p}
              ref={(el) => {
                addRef.current = el
                if (typeof p.ref === 'function') p.ref(el)
                else if (p.ref) (p.ref as { current: HTMLButtonElement | null }).current = el
              }}
              variant="plain"
              size="small"
              icon="plusCircle"
            >
              {addLabel}
            </Button>
          )}
        >
          {(close) => (
            <>
              {field && (
                <div className={styles.field}>
                  <TextField
                    label={field.label}
                    size="small"
                    hideLabel
                    placeholder={field.label}
                    value={query}
                    maxLength={field.maxLength}
                    autoComplete="off"
                    autoFocus
                    onChange={(e) => setQuery(e.target.value)}
                    onKeyDown={(e) => {
                      if (e.key !== 'Enter' || e.nativeEvent.isComposing) return
                      e.preventDefault()
                      if (!query.trim()) return
                      onAdd(query)
                      close()
                    }}
                  />
                </div>
              )}
              {field && offered.length > 0 && <MenuSeparator />}
              {offered.map((c) => (
                <MenuItem
                  key={c.value}
                  label={c.label}
                  onSelect={() => {
                    onAdd(c.value)
                    close()
                  }}
                />
              ))}
            </>
          )}
        </Menu>
      )}
    </div>
  )
}
