// One channel of the list, as a TV's channel list: its number, the
// programme's still, the channel's name, what is on (one line) and a thin
// progress bar — nothing more (the time left and what follows are in the
// details pane, the guide and the player). The star at the trailing edge
// (shown on hover and focus with a mouse, always on touch and when starred)
// and the row's context menu star the channel. The row opens the player.
import { useState, type MouseEvent } from 'react'
import { Link, useNavigate } from 'react-router'
import type { Channel } from '../../api/channels'
import { Icon } from '../../components/Icon'
import { useT } from '../../i18n'
import { IconButton } from '../../ui/IconButton'
import { MenuItem, PointMenu } from '../../ui/Menu'
import { ProgressBar } from '../../ui/ProgressBar'
import { ChannelArt } from './ChannelArt'
import { ChannelLogo } from './ChannelLogo'
import { progressOf } from './model'
import styles from './Tv.module.css'

export interface ChannelRowProps {
  channel: Channel
  now: number
  /** The player's address (keeps the list it was opened from). */
  to: string
  last?: boolean
  selected?: boolean
  eager?: boolean
  onFavorite: (on: boolean) => void
  /** Some channel of the list has a logo: keep its slot in this row too. */
  logoSlot?: boolean
  /** The pointer or the focus is on the row (the wide screen's preview follows it). */
  onPreview?: () => void
}

export function ChannelRow({ channel: c, now, to, last, selected, eager, logoSlot, onFavorite, onPreview }: ChannelRowProps) {
  const t = useT()
  const navigate = useNavigate()
  const onWatch = () => navigate(to)
  const [menu, setMenu] = useState<{ x: number; y: number } | null>(null)
  const p = c.now && Date.parse(c.now.end) > now ? c.now : c.next && Date.parse(c.next.start) <= now ? c.next : c.now
  const title = p ? [p.title, p.subtitle].filter(Boolean).join(' · ') : t('tv.nothing')
  const pct = p ? Math.round(progressOf(p, now) * 100) : 0
  const fav = Boolean(c.favorite)
  const onContext = (e: MouseEvent) => {
    e.preventDefault()
    setMenu({ x: e.clientX, y: e.clientY })
  }
  return (
    <li className={styles.row} data-last={last || undefined} data-selected={selected || undefined} data-fav={fav || undefined} onPointerEnter={onPreview} onFocus={onPreview} onContextMenu={onContext}>
      <Link to={to} className={styles.rowLink} data-logos={logoSlot || undefined} aria-label={`${t('tv.channelNumber', { number: c.number, name: c.name })}. ${title}`}>
        <span className={styles.rowNum}>{c.number}</span>
        <ChannelArt program={p} width={112} sizes="(max-width: 699px) 80px, 112px" eager={eager} className={styles.rowArt} />
        {logoSlot && <ChannelLogo url={c.logoUrl} size="sm" reserve />}
        <span className={styles.rowText}>
          <span className={styles.rowChannel}>
            <span className={styles.rowChannelName}>{c.name}</span>
          </span>
          <span className={styles.rowTitle}>{title}</span>
          {p && <ProgressBar value={pct / 100} tone="label" className={styles.rowBar} />}
        </span>
      </Link>
      <span className={styles.rowStar}>
        <IconButton
          icon={fav ? 'starFill' : 'star'}
          variant="plain"
          size="sm"
          className={styles.starButton}
          label={fav ? t('tv.favRemove') : t('tv.favAdd')}
          aria-pressed={fav}
          onClick={() => onFavorite(!fav)}
        />
      </span>
      {menu && (
        <PointMenu x={menu.x} y={menu.y} label={c.name} onClose={() => setMenu(null)}>
          <MenuItem icon={<Icon name="play" size={16} />} label={t('tv.watchNow')} onSelect={() => (setMenu(null), onWatch())} />
          <MenuItem
            icon={<Icon name={fav ? 'starFill' : 'star'} size={16} />}
            label={fav ? t('tv.favRemove') : t('tv.favAdd')}
            onSelect={() => {
              setMenu(null)
              onFavorite(!fav)
            }}
          />
        </PointMenu>
      )}
    </li>
  )
}
