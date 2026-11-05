// A short message at the bottom of the screen: a glass capsule, centred,
// clear of everything that floats there — the tab bar on phones and the
// player bar while music plays (the --toast-bottom token knows both). One
// trailing button may undo what just happened («Отменить»).
//
// The live region is always mounted (a region inserted together with its
// text is often not read), so pass no children to keep it empty. `live`
// false hides it from assistive tech when another live region already
// reads the same words (the player bar's own status).
import type { ReactNode } from 'react'
import { createPortal } from 'react-dom'
import styles from './Toast.module.css'

export interface ToastProps {
  /** The message; null or empty: nothing on screen, the region stays. */
  children?: ReactNode
  /** One action at the trailing end («Отменить»). */
  action?: { label: string; onClick: () => void }
  /** A new key restarts the capsule's entrance for a new message. */
  messageKey?: string | number
  live?: boolean
  /** error: an alert, the text in the warning tone («Не удалось…»). */
  tone?: 'default' | 'error'
  className?: string
}

export function Toast({ children, action, messageKey, live = true, tone = 'default', className }: ToastProps) {
  const shown = children != null && children !== false && children !== ''
  return createPortal(
    <div
      className={[styles.region, className].filter(Boolean).join(' ')}
      role={live ? (tone === 'error' ? 'alert' : 'status') : undefined}
      aria-live={live ? (tone === 'error' ? 'assertive' : 'polite') : undefined}
      aria-hidden={live ? undefined : true}
      data-testid="toast-region"
    >
      {shown && (
        <div key={messageKey} className={`${styles.toast} ${action ? styles.withAction : ''} glass-strong`} data-tone={tone}>
          <span className={styles.text}>{children}</span>
          {action && (
            <button type="button" className={styles.action} onClick={action.onClick}>
              {action.label}
            </button>
          )}
        </div>
      )}
    </div>,
    document.body,
  )
}
