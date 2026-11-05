// iOS switch: a 51×31 track with a white knob, green when on. role="switch";
// Space/Enter toggle (native button). The label is the accessible name.
import styles from './Toggle.module.css'

export interface ToggleProps {
  label: string
  checked: boolean
  onChange: (on: boolean) => void
  disabled?: boolean
  /** Visually hide the label (it is still read out). */
  hideLabel?: boolean
  /** id of the help text read after the label. */
  describedBy?: string
}

export function Toggle({ label, checked, onChange, disabled, hideLabel = true, describedBy }: ToggleProps) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={hideLabel ? label : undefined}
      aria-describedby={describedBy}
      disabled={disabled}
      className={styles.toggle}
      onClick={() => onChange(!checked)}
    >
      <span className={styles.track} aria-hidden>
        <span className={styles.knob} />
      </span>
      {!hideLabel && <span className={styles.label}>{label}</span>}
    </button>
  )
}
