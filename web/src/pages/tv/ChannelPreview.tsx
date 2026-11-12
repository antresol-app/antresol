// The wide screen's details pane beside the list (TiviMate): the channel
// the pointer or the focus is on — the programme's still, what it is, how
// far it is, its description, «Смотреть», the star, and what follows.
import { useEffect, useState } from 'react'
import { getChannelGuide, type Channel, type ChannelProgram } from '../../api/channels'
import { useT } from '../../i18n'
import { ButtonLink } from '../../ui/Button'
import { IconButton } from '../../ui/IconButton'
import { ProgressBar } from '../../ui/ProgressBar'
import { ChannelArt } from './ChannelArt'
import { isOnAir, minutesLeft, progressOf } from './model'
import { clock } from './useChannels'
import styles from './Tv.module.css'

export function ChannelPreview({ channel: c, now, to, onFavorite }: { channel: Channel; now: number; to: string; onFavorite: (on: boolean) => void }) {
  const t = useT()
  const [later, setLater] = useState<ChannelProgram[] | null>(null)
  // what follows: read after the pointer rests on the channel a moment
  useEffect(() => {
    setLater(null)
    const ac = new AbortController()
    const id = window.setTimeout(() => {
      getChannelGuide(c.id, 6, ac.signal).then((g) => setLater(g.programs), () => undefined)
    }, 250)
    return () => {
      window.clearTimeout(id)
      ac.abort()
    }
  }, [c.id])
  const p = c.now && isOnAir(c.now, now) ? c.now : c.next && isOnAir(c.next, now) ? c.next : c.now
  const upcoming = (later ?? (c.next ? [c.next] : [])).filter((x) => Date.parse(x.start) >= (p ? Date.parse(p.end) - 1000 : now)).slice(0, 4)
  const fav = Boolean(c.favorite)
  const facts = p ? [p.year, ...(p.genres ?? []).slice(0, 2), p.rating].filter(Boolean).join(' · ') : ''
  return (
    <aside className={styles.preview} aria-label={t('tv.channelNumber', { number: c.number, name: c.name })}>
      <ChannelArt program={p} width={440} eager className={styles.previewArt} />
      <div className={styles.previewBody}>
        <p className={styles.previewChannel}>
          <span className={styles.previewNum}>{c.number}</span>
          {c.name}
        </p>
        <h2 className={styles.previewTitle}>{p ? p.title : t('tv.nothing')}</h2>
        {p?.subtitle && <p className={styles.previewSub}>{p.subtitle}</p>}
        {facts && <p className={styles.previewFacts}>{facts}</p>}
        {p && (
          <div className={styles.previewTime}>
            <span>{clock(p.start)}</span>
            <ProgressBar value={progressOf(p, now)} tone="label" className={styles.previewBar} />
            <span>{clock(p.end)}</span>
          </div>
        )}
        {p && <p className={styles.previewLeft}>{t('tv.left', { count: minutesLeft(p, now) })}</p>}
        <div className={styles.previewActions}>
          <ButtonLink to={to} icon="play">
            {t('tv.watchNow')}
          </ButtonLink>
          <IconButton icon={fav ? 'starFill' : 'star'} label={fav ? t('tv.favRemove') : t('tv.favAdd')} aria-pressed={fav} onClick={() => onFavorite(!fav)} />
        </div>
        {p?.overview && <p className={styles.previewOverview}>{p.overview}</p>}
        {upcoming.length > 0 && (
          <section className={styles.previewNext}>
            <h3 className={styles.previewNextTitle}>{t('tv.next')}</h3>
            <ul>
              {upcoming.map((x) => (
                <li key={x.id ?? x.start}>
                  <span className={styles.previewNextTime}>{clock(x.start)}</span>
                  <span className={styles.previewNextName}>{[x.title, x.subtitle].filter(Boolean).join(' · ')}</span>
                </li>
              ))}
            </ul>
          </section>
        )}
      </div>
    </aside>
  )
}
