// A programme's details (the guide's block, tapped): the still, the
// channel with its star, the title and episode, when it is on, what it is
// and the description; «Смотреть» while it is on air.
import type { ChannelProgram } from '../../api/channels'
import { locale, useT } from '../../i18n'
import { Badge } from '../../ui/Badge'
import { IconButton } from '../../ui/IconButton'
import { Sheet } from '../../ui/Sheet'
import { ChannelArt } from './ChannelArt'
import { isOnAir, type Numbered } from './model'
import { clock } from './useChannels'
import styles from './Guide.module.css'

/** «Сегодня, 21:00–22:40» (tomorrow, yesterday; another day by its date). */
export function useWhen() {
  const t = useT()
  return (p: { start: string; end: string }, now: number) => {
    const range = t('tv.time', { start: clock(p.start), end: clock(p.end) })
    const day = (ms: number) => {
      const d = new Date(ms)
      return new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime()
    }
    const diff = Math.round((day(Date.parse(p.start)) - day(now)) / 86_400_000)
    if (diff === 0) return t('tv.today', { time: range })
    if (diff === 1) return t('tv.tomorrow', { time: range })
    if (diff === -1) return t('tv.yesterday', { time: range })
    return `${new Date(p.start).toLocaleDateString(locale(), { day: 'numeric', month: 'long' })}, ${range}`
  }
}

export function ProgramSheet({
  program: p,
  channel: c,
  now,
  onWatch,
  onFavorite,
  onClose,
}: {
  program: ChannelProgram
  channel: Numbered
  now: number
  onWatch: () => void
  onFavorite: (on: boolean) => void
  onClose: () => void
}) {
  const t = useT()
  const when = useWhen()
  const live = isOnAir(p, now)
  const fav = Boolean(c.favorite)
  const facts = [p.year, ...(p.genres ?? []), p.rating].filter(Boolean).join(' · ')
  return (
    <Sheet
      title={t('tv.channelNumber', { number: c.number, name: c.name })}
      role="dialog"
      size="wide"
      dismissOnOutside
      closeButton
      onCancel={onClose}
      actions={live ? [{ label: t('tv.watchNow'), kind: 'primary', run: onWatch }] : []}
    >
      <div className={styles.sheet}>
        <ChannelArt program={p} width={440} eager className={styles.sheetArt} />
        <div className={styles.sheetHead}>
          <h3 className={styles.sheetTitle}>{p.title}</h3>
          <IconButton icon={fav ? 'starFill' : 'star'} size="sm" variant="plain" label={fav ? t('tv.favRemove') : t('tv.favAdd')} aria-pressed={fav} onClick={() => onFavorite(!fav)} />
        </div>
        {p.subtitle && <p className={styles.sheetSub}>{p.subtitle}</p>}
        <p className={styles.sheetWhen}>
          {when(p, now)}
          {live && <Badge tone="accent">{t('tv.onAirNow')}</Badge>}
        </p>
        {facts && <p className={styles.sheetFacts}>{facts}</p>}
        {p.overview && <p className={styles.sheetOverview}>{p.overview}</p>}
      </div>
    </Sheet>
  )
}
