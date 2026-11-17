// What a card of a title on its way into the library draws over its artwork
// (GET /api/library/pending, the addons' pending cards): the round download
// button's look — a glass disc with the progress ring — in the middle of a
// card of its own, or small in the corner of a library card that is
// getting more (a new season).
import type { PendingState } from '../api/types'
import { useT, type TFunction } from '../i18n'
import { ProgressRing, percent } from '../ui/StatusRing'
import { Icon } from './Icon'
import styles from './PendingOverlay.module.css'

/** «Загружается 42 %», «Пауза · 42 %», «Ждёт места»…; an active one's hint (the addon's line) wins. */
export function pendingLabel(t: TFunction, p: PendingState): string {
  const n = percent(p.done, p.size)
  switch (p.status) {
    case 'active':
      // one without headway says why instead of a «0 %» that never moves
      return p.hint || (p.size > 0 ? t('comp.pending.active', { n }) : t('comp.pending.starting'))
    case 'paused':
      return t('comp.pending.paused', { n })
    case 'waiting':
      return t('comp.pending.waiting')
    case 'organizing':
      return t('comp.pending.organizing')
    default:
      return t('comp.pending.error')
  }
}

/**
 * The ring over a pending card. `corner`: the small one on a library card,
 * where it is the only word on the download; else the big one of a card of
 * its own, whose subtitle already says the same (hidden from screen readers
 * there, so the status is not read twice).
 */
export function PendingOverlay({ pending, corner = false }: { pending: PendingState; corner?: boolean }) {
  const t = useT()
  const d = corner ? 30 : 52
  const starting = pending.status === 'organizing' || (pending.status === 'active' && !(pending.size > 0))
  const mode = starting ? 'starting' : pending.status === 'active' ? 'active' : 'paused'
  const icon = pending.status === 'error' ? 'warning' : pending.status === 'paused' || pending.status === 'waiting' ? 'pause' : 'arrowDown'
  return (
    <span className={`${styles.overlay} ${corner ? styles.corner : styles.center}`} aria-hidden={corner ? undefined : true}>
      <span className={styles.wrap} style={{ width: d, height: d }}>
        <span className={`${styles.disc} glass ${pending.status === 'error' ? styles.error : ''}`}>
          <Icon name={icon} size={corner ? 14 : 20} strokeWidth={2.2} />
        </span>
        {pending.status !== 'error' && (
          <ProgressRing diameter={d} mode={mode} pct={percent(pending.done, pending.size)} label={pendingLabel(t, pending)} />
        )}
      </span>
    </span>
  )
}
