// The one linear progress bar: a 4px track with a rounded fill, the colour
// of the state (accent while it runs, grey while paused, orange on a
// problem, white for what was watched). A progressbar for assistive tech
// when it has a label; decorative (aria-hidden) without one.
import { formatPercent } from '../i18n'
import styles from './ProgressBar.module.css'

export type ProgressTone = 'accent' | 'paused' | 'warning' | 'label'

export function ProgressBar({ value, tone = 'accent', label, className }: { value: number; tone?: ProgressTone; label?: string; className?: string }) {
  const pct = Math.min(100, Math.max(0, value * 100))
  const a11y = label
    ? { role: 'progressbar', 'aria-label': label, 'aria-valuemin': 0, 'aria-valuemax': 100, 'aria-valuenow': Math.floor(pct), 'aria-valuetext': formatPercent(Math.floor(pct)) }
    : { 'aria-hidden': true }
  return (
    <div className={[styles.track, className].filter(Boolean).join(' ')} data-tone={tone} {...a11y}>
      {/* the whole track's width, shifted left by what is still to go: a transform moves on the compositor, a width would lay out the row on every frame */}
      <div className={styles.fill} style={{ transform: `translateX(${Math.round((pct - 100) * 1000) / 1000}%)` }} />
    </div>
  )
}
