// The one progress ring of the app, like the rings of the Activity app and
// the App Store's download button. Two forms share one drawing:
//   StatusRing   — a ring with its own middle: the percent, a check once
//                  done, «!» on an error. One size and one stroke for every
//                  state. Decorative: the row's status line says it in words.
//   ProgressRing — the ring alone, laid over a round box of `diameter` (the
//                  round download button, a pending card's disc); a
//                  progressbar for assistive tech.
// Colour says the state: accent while it runs, grey while paused, orange on
// an error, green when done.
import { formatPercent } from '../i18n'
import { Icon } from '../components/Icon'
import styles from './StatusRing.module.css'

export type RingState = 'active' | 'paused' | 'error' | 'done'

/** 0..100, rounded down so a nearly finished download never reads 100 early. */
export function percent(done: number, size: number): number {
  if (!(size > 0)) return 0
  return Math.min(100, Math.max(0, Math.floor((done / size) * 100)))
}

/** The circles: a track and an arc from 12 o'clock, clockwise. `pct` null: a short arc circles (no percent yet). */
function Ring({ d, stroke, pct }: { d: number; stroke: number; pct: number | null }) {
  const r = (d - stroke) / 2
  // pathLength=100: the dash lengths are plain percents
  const dash = pct === null ? '28 72' : `${pct} ${100 - pct}`
  return (
    <svg className={styles.svg} width={d} height={d} viewBox={`0 0 ${d} ${d}`} aria-hidden focusable="false">
      <circle className={styles.track} cx={d / 2} cy={d / 2} r={r} strokeWidth={stroke} />
      <circle data-arc className={styles.arc} cx={d / 2} cy={d / 2} r={r} strokeWidth={stroke} pathLength={100} strokeDasharray={dash} />
    </svg>
  )
}

const SIZE = 40
const STROKE = 3

/** A ring with the percent (a check when done, «!» on an error) in its middle. */
export function StatusRing({ state, value, size = SIZE }: { state: RingState; value: number; size?: number }) {
  const v = state === 'done' ? 1 : Math.min(1, Math.max(0, value))
  const pct = Math.floor(v * 100)
  return (
    <span className={styles.ring} data-state={state} style={{ width: size, height: size }} aria-hidden>
      <Ring d={size} stroke={STROKE} pct={pct} />
      <span className={styles.middle}>{state === 'done' ? <Icon name="check" size={18} strokeWidth={3} /> : state === 'error' ? '!' : `${pct}%`}</span>
    </span>
  )
}

export interface ProgressRingProps {
  /** Outer diameter in px: the box of the round thing it circles. */
  diameter: number
  /** `starting`: no percent yet, a short arc circles. */
  mode: 'starting' | 'active' | 'paused'
  /** 0..100 */
  pct: number
  label: string
}

/** The ring alone over a round box (it does not take pointer events). */
export function ProgressRing({ diameter, mode, pct, label }: ProgressRingProps) {
  return (
    <span
      className={styles.overlay}
      data-state={mode === 'paused' ? 'paused' : 'active'}
      data-starting={mode === 'starting' || undefined}
      role="progressbar"
      aria-label={label}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={mode === 'starting' ? undefined : pct}
      aria-valuetext={mode === 'starting' ? undefined : formatPercent(pct)}
    >
      <Ring d={diameter} stroke={2} pct={mode === 'starting' ? null : pct} />
    </span>
  )
}
