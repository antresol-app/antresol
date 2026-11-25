// A busy ring: something is on its way and the viewer waits (a button whose
// request is out, a sheet's confirmed action, a panel that loads). It takes
// the colour of the text around it (currentColor); the ring turns by the
// global `.spin` class (design/motion.css), so the whole app has one
// keyframes and Reduce motion slows it in one place.
//   size   xs 16 · sm 18 (the default) · md 20 · lg 24 — the `--icon-*` glyph sizes.
//          Without one the ring is as big as `--spinner-size` says (a button's
//          `1em`, set on the class the caller passes) and as thick as `--spinner-width`.
//   track  a faint full ring under the turning head, for a ring over artwork
//          or video (`--spinner-track-art`).
//   label  its name for assistive tech; without one it is decoration and the
//          control around it says what is happening (`aria-busy`).
import styles from './Spinner.module.css'

export type SpinnerSize = 'xs' | 'sm' | 'md' | 'lg'

export interface SpinnerProps {
  size?: SpinnerSize
  track?: boolean
  label?: string
  className?: string
}

export function Spinner({ size, track, label, className }: SpinnerProps) {
  return (
    <span
      className={['spin', styles.spinner, size ? styles[size] : '', track ? styles.track : '', className].filter(Boolean).join(' ')}
      {...(label ? { role: 'img', 'aria-label': label } : { 'aria-hidden': true })}
    />
  )
}
