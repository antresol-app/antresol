// The round «download» button that sits next to «Смотреть»: an IconButton
// whose outline is the progress — a ProgressRing drawn exactly over the
// glass border fills clockwise.
import type { TorrentHint, TorrentStalled } from '../api/types'
import { useT } from '../i18n'
import { IconButton, IconButtonAnchor, IconButtonLink } from '../ui/IconButton'
import type { TriggerProps } from '../ui/Menu'
import { percent, ProgressRing } from '../ui/StatusRing'
import styles from './DownloadButton.module.css'

export type DownloadState =
  | { kind: 'idle' }
  | { kind: 'starting' }
  /** `stalled`: no headway, and why (the page shows it under the button). */
  | { kind: 'active'; done: number; size: number; stalled?: TorrentStalled; hint?: TorrentHint }
  | { kind: 'paused'; done: number; size: number }
  | { kind: 'done' }

export interface DownloadButtonProps {
  state: DownloadState
  onClick?: () => void
  size?: 'large' | 'small'
  disabled?: boolean
  /** Renders the button as a Menu trigger (the library page's version menu). */
  triggerProps?: TriggerProps
  /** Overrides the idle accessible name. */
  label?: string
  /**
   * A file of the library: the button is a plain download link. The browser
   * owns that download and the page cannot see its progress, so it has no
   * ring (use it with `idle`).
   */
  href?: string
  /**
   * A page of the app (a series card: its seasons are picked there): the
   * button is a router link, never a `download` one — that would save the
   * page as a file.
   */
  to?: string
}

// IconButton's lg / sm diameters (--control-h-lg / --control-h-sm).
const SIZE = { large: 'lg', small: 'sm' } as const
const DIAMETER = { large: 52, small: 36 } as const

export function DownloadButton({ state, onClick, size = 'large', disabled, triggerProps, label, href, to }: DownloadButtonProps) {
  const t = useT()
  const pct = state.kind === 'active' || state.kind === 'paused' ? percent(state.done, state.size) : 0

  let name: string
  if (state.kind === 'idle') name = label ?? t('comp.download.idle')
  else if (state.kind === 'starting') name = t('comp.download.starting')
  else if (state.kind === 'active') name = t('comp.download.active', { n: pct })
  else if (state.kind === 'paused') name = t('comp.download.paused', { n: pct })
  else name = t('comp.download.done')

  if (href) {
    return (
      <span className={styles.wrap}>
        <IconButtonAnchor size={SIZE[size]} icon="arrowDown" href={href} download label={name} />
      </span>
    )
  }

  if (to) {
    return (
      <span className={styles.wrap}>
        <IconButtonLink size={SIZE[size]} icon="arrowDown" to={to} label={name} />
      </span>
    )
  }

  // a finished download: the check stays, a click must not start another one
  const done = state.kind === 'done' && !triggerProps
  const hasRing = state.kind === 'starting' || state.kind === 'active' || state.kind === 'paused'

  return (
    <span className={styles.wrap}>
      <IconButton
        size={SIZE[size]}
        icon={state.kind === 'done' ? 'check' : 'arrowDown'}
        className={state.kind === 'done' ? styles.done : undefined}
        disabled={disabled}
        onClick={done ? undefined : onClick}
        aria-disabled={done || undefined}
        {...triggerProps}
        label={name}
      />
      {hasRing && (
        <ProgressRing
          diameter={DIAMETER[size]}
          mode={state.kind === 'starting' ? 'starting' : state.kind === 'paused' ? 'paused' : 'active'}
          pct={pct}
          label={t('comp.download.progress')}
        />
      )}
    </span>
  )
}
