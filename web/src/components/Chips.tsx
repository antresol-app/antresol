// Pill-shaped choices in a wrapping row: toggles (aria-pressed, «Языки
// субтитров») or a radio group (the online picker). The one chip style.
import { forwardRef, type ButtonHTMLAttributes, type HTMLAttributes } from 'react'
import styles from './Chips.module.css'

export function Chips({ className, ...rest }: HTMLAttributes<HTMLDivElement>) {
  return <div className={[styles.chips, className].filter(Boolean).join(' ')} {...rest} />
}

export type ChipProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  /** Chosen: the accent fill. */
  on: boolean
}

export const Chip = forwardRef<HTMLButtonElement, ChipProps>(function Chip({ on, className, type = 'button', ...rest }, ref) {
  return <button ref={ref} type={type} className={[styles.chip, on ? styles.on : '', className].filter(Boolean).join(' ')} {...rest} />
})
