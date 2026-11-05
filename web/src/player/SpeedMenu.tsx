// «Скорость» of an audiobook: 0,75× … 3× in steps of 0,25, remembered for
// the next book (`vault.audiobook.rate`). The pitch stays the same.
import { useSyncExternalStore } from 'react'
import { Icon } from '../components/Icon'
import { MenuItem, Menu } from '../ui/Menu'
import { loadRate, RATES, setBookRate } from './audio/audiobook'
import { audio } from './audio/engine'
import styles from './SpeedMenu.module.css'
import { locale, useT } from '../i18n'

/** «1,25×» / "1.25×". */
export const rateLabel = (r: number) => `${new Intl.NumberFormat(locale(), { maximumFractionDigits: 2 }).format(r)}×`

export interface SpeedMenuProps {
  /** Look of the trigger: on the dark full player or on a page. */
  tone?: 'player' | 'page'
  placement?: 'bottom-start' | 'bottom-end' | 'top-end' | 'top-start'
}

/** The speed now: the engine's while a book plays, else the remembered one. */
function useRate(): number {
  return useSyncExternalStore(
    audio.subscribe,
    () => (audio.get().kind === 'audiobook' ? audio.get().rate : loadRate()),
    () => 1,
  )
}

export function SpeedMenu({ tone = 'page', placement = 'bottom-start' }: SpeedMenuProps) {
  const t = useT()
  const rate = useRate()
  const label = rateLabel(rate)
  return (
    <Menu
      label={t('player.speed.title')}
      placement={placement}
      className={styles.menu}
      trigger={(p) => (
        <button {...p} type="button" className={`${styles.trigger} ${styles[tone]}`} aria-label={t('player.speed.aria', { label })}>
          <Icon name="speed" size={18} />
          <span>{label}</span>
        </button>
      )}
    >
      {(close) =>
        RATES.map((r) => (
          <MenuItem
            key={r}
            label={rateLabel(r)}
            checked={r === rate}
            onSelect={() => {
              setBookRate(r)
              close()
            }}
          />
        ))
      }
    </Menu>
  )
}
