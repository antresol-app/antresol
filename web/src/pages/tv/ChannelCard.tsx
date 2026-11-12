// A channel on Главная («Сейчас в эфире»): the programme on air as the
// picture (its still, or the neutral placeholder) with its live progress,
// the channel's number and name, the programme's title.
import { Link } from 'react-router'
import type { Channel } from '../../api/channels'
import { useT } from '../../i18n'
import { ChannelArt } from './ChannelArt'
import { progressOf } from './model'
import card from '../../components/Card.module.css'
import styles from './Tv.module.css'

export function ChannelCard({ channel, now, priority = false }: { channel: Channel; now: number; priority?: boolean }) {
  const t = useT()
  const p = channel.now
  const pct = p ? Math.round(progressOf(p, now) * 100) : 0
  const sub = p ? [p.title, p.subtitle].filter(Boolean).join(' · ') : t('tv.nothing')
  return (
    <Link to={`/tv/${channel.id}`} className={[card.card, card.wide].join(' ')} aria-label={t('tv.watch', { name: channel.name }) + (p ? `. ${sub}` : '')}>
      <div className={card.media}>
        <ChannelArt program={p} width={480} sizes="(max-width: 699px) 80vw, (max-width: 1099px) 34vw, 26vw" eager={priority} className={styles.cardArt} />
        {p && (
          <div className={card.progress} role="progressbar" aria-label={t('tv.onAirNow')} aria-valuemin={0} aria-valuemax={100} aria-valuenow={pct}>
            <div className={card.progressFill} style={{ width: `${pct}%` }} />
          </div>
        )}
      </div>
      <div className={card.meta}>
        <span className={card.title}>{t('tv.channelNumber', { number: channel.number, name: channel.name })}</span>
        <span className={card.subtitle}>{sub}</span>
      </div>
    </Link>
  )
}
