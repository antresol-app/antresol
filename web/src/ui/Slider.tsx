// A slider: one value out of a range, set by dragging or with the arrow keys
// (a volume, a speed, a page of a comic). The native range input, so the
// keyboard, touch and assistive tech get a real slider; its look is the video
// bars' volume slider: a thin track, filled in white up to the thumb, a white
// thumb that shows under the pointer and the focus (`thumb="always"`: always),
// the accent ring round the thumb for the keyboard, a 44 pt strip to the finger.
// Not for seeking in a stream (that is the player's scrubber, with its
// buffered ranges). The caller sizes it (`width`; it fills its column by default).
import type { CSSProperties, InputHTMLAttributes } from 'react'
import styles from './Slider.module.css'

export interface SliderProps extends Omit<InputHTMLAttributes<HTMLInputElement>, 'type' | 'value' | 'onChange' | 'min' | 'max' | 'step' | 'size'> {
  /** The slider's name for assistive tech. */
  label: string
  value: number
  onChange: (value: number) => void
  min?: number
  max?: number
  step?: number
  /** What the value reads as when the number alone does not say («80%», «1,5×»). */
  valueText?: string
  /** hover (default): the thumb shows under the pointer and the focus; always: it is always there. */
  thumb?: 'hover' | 'always'
}

export function Slider({ label, value, onChange, min = 0, max = 1, step = 0.05, valueText, thumb = 'hover', className, style, ...rest }: SliderProps) {
  const span = max - min
  const at = span > 0 ? Math.min(1, Math.max(0, (value - min) / span)) : 0
  return (
    <input
      {...rest}
      type="range"
      className={[styles.slider, thumb === 'always' ? styles.always : '', className].filter(Boolean).join(' ')}
      min={min}
      max={max}
      step={step}
      value={value}
      aria-label={label}
      aria-valuetext={valueText}
      style={{ ...style, '--v': at } as CSSProperties}
      onChange={(e) => onChange(Number(e.target.value))}
    />
  )
}
