// A row of channels (the «ТВ» page's groups, «Сейчас в эфире» on Главная):
// the Home shelves' look with channel cards.
import { useId } from 'react'
import { Link } from 'react-router'
import type { Channel } from '../../api/channels'
import { Icon } from '../../components/Icon'
import { Rail } from '../../components/Rail'
import { ChannelCard } from './ChannelCard'
import shelf from '../../components/Shelf.module.css'

export function ChannelShelf({ title, channels, now, priority, seeAllTo }: { title: string; channels: Channel[]; now: number; priority?: boolean; seeAllTo?: string }) {
  const id = useId()
  return (
    <section className={[shelf.shelf, shelf.wide].join(' ')} aria-labelledby={id}>
      <div className={shelf.header}>
        <h2 id={id} className={shelf.title}>
          {seeAllTo ? (
            <Link to={seeAllTo} className={shelf.titleLink}>
              {title}
              <Icon name="chevronRight" size={18} strokeWidth={2.4} />
            </Link>
          ) : (
            title
          )}
        </h2>
      </div>
      <Rail labelledBy={id}>
        {channels.map((c, i) => (
          <li key={c.id} className={shelf.cell}>
            <ChannelCard channel={c} now={now} priority={priority && i < 4} />
          </li>
        ))}
      </Rail>
    </section>
  )
}

