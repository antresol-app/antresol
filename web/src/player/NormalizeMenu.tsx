// «Выравнивание громкости» of music: Авто / По альбому / По треку / Выкл,
// remembered on this device (audio/normalize.ts).
import { useSyncExternalStore } from 'react'
import { Icon } from '../components/Icon'
import { MenuItem, Menu } from '../ui/Menu'
import { audio, normalizeSupported } from './audio/engine'
import { loadNormalize, NORMALIZE_MODES, subscribeNormalize, type NormalizeMode } from './audio/normalize'
import { useT, type Key } from '../i18n'
import styles from './SpeedMenu.module.css'

export const NORMALIZE_LABEL: Record<NormalizeMode, Key> = {
  auto: 'player.normalize.auto',
  album: 'player.normalize.album',
  track: 'player.normalize.track',
  off: 'player.normalize.off',
}

const NORMALIZE_DETAIL: Partial<Record<NormalizeMode, Key>> = {
  auto: 'player.normalize.autoDetail',
}

export interface NormalizeMenuProps {
  tone?: 'player' | 'page'
  placement?: 'bottom-start' | 'bottom-end' | 'top-end' | 'top-start'
}

export function NormalizeMenu({ tone = 'player', placement = 'top-end' }: NormalizeMenuProps) {
  const t = useT()
  const mode = useSyncExternalStore(subscribeNormalize, loadNormalize, () => 'auto' as NormalizeMode)
  // iPhone and iPad: Safari ignores an element's volume — no pretending
  const supported = normalizeSupported()
  const label = supported ? t(NORMALIZE_LABEL[mode]) : t('player.normalize.unsupported')
  return (
    <Menu
      label={t('player.normalize.title')}
      placement={placement}
      className={styles.menu}
      trigger={(p) => (
        <button
          {...p}
          type="button"
          className={`${styles.trigger} ${styles[tone]}`}
          aria-label={t('player.normalize.aria', { label })}
          title={t('player.normalize.title')}
        >
          <Icon name="sliders" size={18} />
        </button>
      )}
    >
      {(close) => (
        <>
          {!supported && <MenuItem label={t('player.normalize.unsupported')} disabled onSelect={() => {}} />}
          {NORMALIZE_MODES.map((m) => {
            const detail = NORMALIZE_DETAIL[m]
            return (
              <MenuItem
                key={m}
                label={t(NORMALIZE_LABEL[m])}
                detail={supported && detail ? t(detail) : undefined}
                checked={supported ? m === mode : undefined}
                disabled={!supported}
                onSelect={() => {
                  audio.setNormalize(m)
                  close()
                }}
              />
            )
          })}
        </>
      )}
    </Menu>
  )
}
