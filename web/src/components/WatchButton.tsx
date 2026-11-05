// «Смотреть» as one white capsule: the main part runs the current way of
// watching, the ▾ segment opens a menu of the other ways (radios) and extra
// actions. With a single way and no extras it is just the plain button.
import { useT } from '../i18n'
import { Button } from '../ui/Button'
import { MenuItem, MenuSeparator, Menu } from '../ui/Menu'
import styles from './WatchButton.module.css'

export interface WatchWay {
  id: string
  label: string
  run: () => void
}

export interface WatchButtonProps {
  /** «Смотреть», or the resume / episode label from ActionBar. */
  label: string
  /** Menu order. Must not be empty: the main part always runs one of them. */
  ways: WatchWay[]
  /** Id of the way the main part runs. */
  current: string
  /** Called after a menu choice ran, so the caller can remember it. */
  onChoose?: (id: string) => void
  /** Plain menu items under a separator. */
  extra?: { label: string; run: () => void }[]
  size?: 'large' | 'small'
  /** Which edge of the capsule the menu lines up with: start on item pages (the row starts at the left), end on cards at the right. */
  placement?: 'bottom-start' | 'bottom-end'
  loading?: boolean
  disabled?: boolean
}

export function WatchButton({ label, ways, current, onChoose, extra = [], size = 'large', placement = 'bottom-start', loading, disabled }: WatchButtonProps) {
  const t = useT()
  const way = ways.find((w) => w.id === current) ?? ways[0]!
  const hasMenu = ways.length > 1 || extra.length > 0

  if (!hasMenu) {
    return (
      <Button size={size} icon="play" loading={loading} disabled={disabled} onClick={way.run}>
        {label}
      </Button>
    )
  }

  return (
    <div className={styles.group}>
      <Button className={styles.main} size={size} icon="play" loading={loading} disabled={disabled} onClick={way.run}>
        {label}
      </Button>
      <Menu
        label={t('comp.action.otherWays')}
        placement={placement}
        trigger={(p) => (
          <Button
            {...p}
            className={styles.more}
            size={size}
            icon="chevronDown"
            aria-label={t('comp.action.otherWays')}
            disabled={disabled || loading}
          />
        )}
      >
        {(close) => (
          <>
            {ways.length > 1 &&
              ways.map((w) => (
                <MenuItem
                  key={w.id}
                  label={w.label}
                  checked={w.id === way.id}
                  onSelect={() => {
                    close()
                    w.run()
                    onChoose?.(w.id)
                  }}
                />
              ))}
            {ways.length > 1 && extra.length > 0 && <MenuSeparator />}
            {extra.map((x) => (
              <MenuItem
                key={x.label}
                label={x.label}
                onSelect={() => {
                  close()
                  x.run()
                }}
              />
            ))}
          </>
        )}
      </Menu>
    </div>
  )
}
