// «Таймер сна»: 15 / 30 / 45 min, 1 h or «До конца главы»; «Выключить».
// While it runs the button shows the time left («12:40»).
import { Icon } from '../components/Icon'
import { MenuItem, MenuSeparator, Menu } from '../ui/Menu'
import { armSleep, cancelSleep, currentSleep, useSleepRemaining } from './audio/sleep'
import { clock } from './timeline'
import { useT, type Key } from '../i18n'
import styles from './SpeedMenu.module.css'

export const SLEEP_MINUTES: [minutes: number, label: Key][] = [
  [15, 'player.sleep.15'],
  [30, 'player.sleep.30'],
  [45, 'player.sleep.45'],
  [60, 'player.sleep.60'],
]

export interface SleepMenuProps {
  tone?: 'player' | 'page'
  placement?: 'bottom-start' | 'bottom-end' | 'top-end' | 'top-start'
}

export function SleepMenu({ tone = 'page', placement = 'bottom-start' }: SleepMenuProps) {
  const t = useT()
  const left = useSleepRemaining()
  const on = left !== null
  const chapter = currentSleep()?.kind === 'chapter'
  const text = on ? clock(left) : ''
  return (
    <Menu
      label={t('player.sleep.title')}
      placement={placement}
      className={styles.menu}
      trigger={(p) => (
        <button
          {...p}
          type="button"
          className={`${styles.trigger} ${styles[tone]} ${on ? styles.on : ''}`}
          aria-label={on ? t('player.sleep.left', { text }) : t('player.sleep.title')}
        >
          <Icon name="moon" size={18} />
          {on && <span>{text}</span>}
        </button>
      )}
    >
      {(close) => (
        <>
          {SLEEP_MINUTES.map(([m, label]) => (
            <MenuItem
              key={m}
              label={t(label)}
              onSelect={() => {
                armSleep({ kind: 'minutes', endsAt: Date.now() + m * 60_000 })
                close()
              }}
            />
          ))}
          <MenuItem
            label={t('player.sleep.endOfChapter')}
            detail={chapter && on ? t('player.sleep.on') : undefined}
            onSelect={() => {
              armSleep({ kind: 'chapter' })
              close()
            }}
          />
          <MenuSeparator />
          <MenuItem
            label={t('player.sleep.off')}
            disabled={!on}
            onSelect={() => {
              cancelSleep()
              close()
            }}
          />
        </>
      )}
    </Menu>
  )
}
