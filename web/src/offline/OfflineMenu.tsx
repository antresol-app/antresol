// «На это устройство» → «Сохранить офлайн»: the menu items and the status
// line under a page's buttons («Загружено», «Сохраняется… 40 %», errors).
import { Link } from 'react-router'
import { Icon } from '../components/Icon'
import { MenuItem } from '../ui/Menu'
import { t as tr, useT } from '../i18n'
import {
  cancelSaving,
  clearSaveError,
  offlineSupported,
  removeOffline,
  startSaving,
  useOfflineStatus,
  type OfflineTarget,
  type SaveStatus,
} from './store'
import styles from './OfflineMenu.module.css'

export const percent = (s: SaveStatus) => (s.state === 'saving' && s.total > 0 ? Math.floor((s.done / s.total) * 100) : 0)

const detailOf = (target: OfflineTarget) => tr(target.kind === 'album' || target.kind === 'audiobook' ? 'offline.listenOffline' : 'offline.readOffline')

/** Menu rows for a Popover; `close` closes it. */
export function OfflineMenuItems({ target, close }: { target: OfflineTarget; close: (refocus?: boolean) => void }) {
  const t = useT()
  const s = useOfflineStatus(target)
  if (!offlineSupported()) return <MenuItem label={t('offline.save')} detail={t('offline.needsHttps')} icon={<Icon name="download" size={18} />} disabled />
  if (s.state === 'saving') {
    return (
      <>
        <MenuItem label={t('offline.saving', { percent: percent(s) })} icon={<Icon name="download" size={18} />} disabled />
        <MenuItem
          label={t('offline.cancelSaving')}
          icon={<Icon name="close" size={18} />}
          onSelect={() => {
            cancelSaving(target)
            close(false)
          }}
        />
      </>
    )
  }
  if (s.state === 'saved') {
    return (
      <>
        <MenuItem label={t('offline.saved')} icon={<Icon name="checkCircle" size={18} />} disabled />
        <MenuItem
          label={t('offline.remove')}
          icon={<Icon name="trash" size={18} />}
          onSelect={() => {
            void removeOffline(target)
            close(false)
          }}
        />
      </>
    )
  }
  return (
    <MenuItem
      label={t('offline.save')}
      detail={detailOf(target)}
      icon={<Icon name="download" size={18} />}
      onSelect={() => {
        startSaving(target)
        close(false)
      }}
    />
  )
}

/** Under a page's buttons: «Загружено» (link to the downloads), the progress or an error. */
export function OfflineStatus({ target }: { target: OfflineTarget }) {
  const t = useT()
  const s = useOfflineStatus(target)
  if (s.state === 'saved') {
    return (
      <Link to="/offline" className={styles.badge} aria-label={t('offline.downloadedAria')}>
        <Icon name="checkCircle" size={15} strokeWidth={2.2} />
        {t('offline.downloaded')}
      </Link>
    )
  }
  if (s.state === 'saving') {
    const p = percent(s)
    return (
      <div className={styles.saving} role="status">
        <span className={styles.label}>{t('offline.saving', { percent: p })}</span>
        <span className={styles.bar} aria-hidden>
          <span style={{ width: `${p}%` }} />
        </span>
        <button type="button" className={styles.cancel} onClick={() => cancelSaving(target)}>
          {t('common.cancelAction')}
        </button>
      </div>
    )
  }
  if (s.state === 'error') {
    return (
      <p className={styles.error} role="alert">
        <Icon name="warning" size={15} />
        {s.message}
        <button type="button" className={styles.cancel} onClick={() => clearSaveError(target)} aria-label={t('offline.hide')}>
          <Icon name="close" size={14} />
        </button>
      </p>
    )
  }
  return null
}
