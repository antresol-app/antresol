// The app's pop-up button, in place of a native <select> (which cannot take
// the app's look): a glass capsule with the current choice and ⌃⌄ that opens
// the app's Menu — floating, in a portal, always inside the window, the
// choice scrolled into view; keyboard, focus return and the phone sheet come
// with it. Select picks one (radio items); MultiSelect several (checkbox
// items, the menu stays open). The accessible name says the setting and the
// choice: «Субтитры: Выключены».
import type { MouseEvent, ReactNode } from 'react'
import { useT } from '../i18n'
import { Icon } from '../components/Icon'
import { Menu, MenuItem, type MenuProps, type TriggerProps } from './Menu'
import styles from './Select.module.css'

export interface SelectOption<T extends string> {
  value: T
  label: string
  /** A second line in the menu. */
  detail?: string
}

interface Common {
  /** The setting's name: the menu's and (with the choice) the button's. */
  label: string
  /** Show `label` inside the capsule before the choice («Сезон 2 ⌃⌄»). */
  showLabel?: boolean
  disabled?: boolean
  placement?: MenuProps['placement']
  className?: string
  id?: string
}

function Trigger({ label, showLabel, text, disabled, className, id, p }: { label: string; showLabel?: boolean; text: string; disabled?: boolean; className?: string; id?: string; p: TriggerProps }) {
  return (
    <button
      type="button"
      id={id}
      className={[styles.trigger, 'glass', className].filter(Boolean).join(' ')}
      aria-label={`${label}: ${text}`}
      disabled={disabled}
      {...p}
      // ↓/↑ open it, as on a macOS pop-up button (the menu then focuses the choice)
      onKeyDown={(e) => {
        if ((e.key === 'ArrowDown' || e.key === 'ArrowUp') && !p['aria-expanded']) {
          e.preventDefault()
          p.onClick(e as unknown as MouseEvent)
        }
      }}
    >
      {showLabel && <span className={styles.label}>{label}</span>}
      <span className={styles.value}>{text}</span>
      <Icon name="chevronUpDown" size={14} strokeWidth={2.4} className={styles.chevron} />
    </button>
  )
}

export function Select<T extends string>({
  label,
  value,
  options,
  onChange,
  showLabel,
  disabled,
  placement = 'bottom-start',
  className,
  id,
}: Common & { value: T; options: SelectOption<T>[]; onChange: (v: T) => void }) {
  const t = useT()
  // a value none of the options has (a field never set): say so, no empty capsule
  const current = options.find((o) => o.value === value)?.label ?? t('comp.select.none')
  return (
    <Menu label={label} placement={placement} trigger={(p) => <Trigger label={label} showLabel={showLabel} text={current} disabled={disabled} className={className} id={id} p={p} />}>
      {(close) =>
        options.map((o) => (
          <MenuItem
            key={o.value}
            label={o.label}
            detail={o.detail}
            checked={o.value === value}
            onSelect={() => {
              close()
              if (o.value !== value) onChange(o.value)
            }}
          />
        ))
      }
    </Menu>
  )
}

/** Several of `options` (kept in the options' order); `min` stay chosen; `none` names an empty choice. */
export function MultiSelect<T extends string>({
  label,
  value,
  options,
  onChange,
  min = 0,
  none = '—',
  showLabel,
  disabled,
  placement = 'bottom-start',
  className,
  id,
  footer,
}: Common & { value: T[]; options: SelectOption<T>[]; onChange: (v: T[]) => void; min?: number; none?: string; footer?: ReactNode }) {
  const on = new Set(value)
  const chosen = options.filter((o) => on.has(o.value))
  const text = chosen.length ? chosen.map((o) => o.label).join(', ') : none
  return (
    <Menu label={label} placement={placement} trigger={(p) => <Trigger label={label} showLabel={showLabel} text={text} disabled={disabled} className={className} id={id} p={p} />}>
      {() => (
        <>
          {options.map((o) => {
            const checked = on.has(o.value)
            return (
              <MenuItem
                key={o.value}
                multi
                label={o.label}
                detail={o.detail}
                checked={checked}
                disabled={checked && on.size <= min}
                onSelect={() => {
                  const next = new Set(on)
                  if (checked) next.delete(o.value)
                  else next.add(o.value)
                  onChange(options.filter((x) => next.has(x.value)).map((x) => x.value))
                }}
              />
            )
          })}
          {footer}
        </>
      )}
    </Menu>
  )
}
