// «Статистика»: the disk, how much is in the library, who watched how much
// in the last 30 days, what was watched most, and the addons' blocks (statsBlocks).
import { APP_NAME } from '../../brand'
import { useEffect, useState } from 'react'
import { Link } from 'react-router'
import { getStats, imageUrl, errorText } from '../../api/client'
import type { Stats as Data } from '../../api/types'
import { Avatar } from '../../components/Avatar'
import { itemHref } from '../../components/Card'
import { DiskMeter, segmentsOf, type DiskSegment } from '../../components/DiskMeter'
import { Card } from '../../ui/Card'
import { DiskBreakdownSheet } from './DiskBreakdown'
import { Icon } from '../../components/Icon'
import { useSession } from '../../state/session'
import { AddonBoundary, useAddonSlot } from '../../addons/slots'
import { getLanguage, t, useT } from '../../i18n'
import { Spinner } from '../../ui/Spinner'
import m from './Manage.module.css'
import styles from './Stats.module.css'

const decimal = (n: number | string) => (getLanguage() === 'ru' ? String(n).replace('.', ',') : String(n))
const hours = (h: number) => {
  if (h < 1) return t('stats.minutes', { n: Math.round(h * 60) })
  const r = Math.round(h * 10) / 10
  return t('stats.hours', { n: decimal(r) })
}

export function Stats() {
  const [data, setData] = useState<Data | null>(null)
  const [error, setError] = useState('')
  const { user } = useSession()
  const blocks = useAddonSlot('statsBlocks')
  // the disk category whose breakdown is open
  const [picked, setPicked] = useState<DiskSegment | null>(null)
  const t = useT()
  useEffect(() => {
    document.title = `${t('stats.title')} — ${APP_NAME}`
    const ac = new AbortController()
    getStats(ac.signal).then(setData, (e: unknown) => !ac.signal.aborted && setError(errorText(e, t('common.loadFailed'))))
    return () => ac.abort()
  }, [t])

  const maxHours = Math.max(0.01, ...(data?.watchedByUser.map((u) => u.hours30d) ?? [0]))
  return (
    <section aria-labelledby="stats-title">
      <header className={m.header}>
        <div>
          <h2 id="stats-title" className={m.title}>
            {t('stats.title')}
          </h2>
          <p className={m.subtitle}>{t('stats.subtitleLibrary')}</p>
        </div>
      </header>
      {error && (
        <div className={m.problem} role="alert">
          {error}
        </div>
      )}
      {!data && !error && (
        <div className={m.spinnerRow} role="status">
          <Spinner track className={m.spinner} /> {t('stats.loading')}
        </div>
      )}
      {data && (
        <>
          <div className={styles.tiles}>
            <Card className={styles.tile}>
              <span className={styles.tileValue}>{data.counts.movies}</span>
              <span className={styles.tileLabel}>{t('stats.tile.movies', { count: data.counts.movies })}</span>
            </Card>
            <Card className={styles.tile}>
              <span className={styles.tileValue}>{data.counts.series}</span>
              <span className={styles.tileLabel}>
                {t('stats.tile.series', { count: data.counts.series })} · {t('stats.episodes', { count: data.counts.episodes })}
              </span>
            </Card>
          </div>

          <p className={styles.libraryLine}>{libraryLine(data.counts)}</p>
          {data.transcoding && (
            <p className={styles.libraryLine}>
              {data.transcoding.mode === 'none' ? t('stats.transcoding.cpu') : t('stats.transcoding.gpu', { name: data.transcoding.name })}
            </p>
          )}

          {blocks.map((b, i) => {
            const Block = b.element
            return (
              <AddonBoundary key={`${b.addonId}:${i}`} addonId={b.addonId} fallback={null}>
                <Block />
              </AddonBoundary>
            )
          })}

          {data.disk.total > 0 && (
            <Card>
              <DiskMeter
                title={t('stats.mediaDisk')}
                total={data.disk.total}
                free={data.disk.free}
                segments={segmentsOf(data.disk.categories)}
                // a category opens into what it is made of (not on a library-only server)
                onPick={user?.libraryOnly ? undefined : setPicked}
                pickHint={user?.libraryOnly ? undefined : t('stats.disk.details')}
              />
            </Card>
          )}
          {!user?.libraryOnly && picked && <DiskBreakdownSheet segment={picked} onClose={() => setPicked(null)} />}

          <div className={styles.columns}>
            <div>
              <h3 className={m.groupTitle}>{t('stats.byUser')}</h3>
              {data.watchedByUser.length === 0 ? (
                <p className={m.groupFoot}>{t('stats.byUser.empty')}</p>
              ) : (
                <ul className={m.group}>
                  {data.watchedByUser.map((u) => (
                    <li key={u.user} className={`${m.row} ${styles.userRow}`}>
                      <Avatar name={u.user} avatar={u.avatar} photo={u.photo} size={30} />
                      <span className={styles.userName}>{u.user}</span>
                      <span className={styles.barTrack} aria-hidden>
                        <span className={styles.bar} style={{ width: `${(u.hours30d / maxHours) * 100}%` }} />
                      </span>
                      <span className={styles.hours}>{hours(u.hours30d)}</span>
                    </li>
                  ))}
                </ul>
              )}
            </div>
            <div>
              <h3 className={m.groupTitle}>{t('stats.top')}</h3>
              {data.topItems30d.length === 0 ? (
                <p className={m.groupFoot}>{t('stats.top.empty')}</p>
              ) : (
                <ol className={m.group}>
                  {data.topItems30d.map((it, i) => (
                    <li key={it.id} className={`${m.row} ${styles.topRow}`}>
                      <span className={styles.rank}>{i + 1}</span>
                      <span className={styles.topPoster}>
                        {it.poster ? <img src={imageUrl(it.poster, 160)} alt="" loading="lazy" /> : <Icon name={it.kind === 'series' ? 'tv' : it.kind === 'audiobook' ? 'headphones' : 'film'} size={16} />}
                      </span>
                      <Link to={itemHref(it.kind, it.id)} className={styles.topTitle}>
                        {it.title}
                      </Link>
                      <span className={styles.hours}>
                        {t('stats.viewers', { count: it.viewers })} · {hours(it.hours)}
                      </span>
                    </li>
                  ))}
                </ol>
              )}
            </div>
          </div>
        </>
      )}
    </section>
  )
}

/** «Музыка: 12 альбомов, 140 песен · Книги: 30 · Аудиокниги: 4 · Манга и комиксы: 2 серии». */
export function libraryLine(c: Partial<Data['counts']>): string {
  return [
    t('stats.line.music', { albums: t('stats.albums', { count: c.albums ?? 0 }), songs: t('stats.songs', { count: c.tracks ?? 0 }) }),
    t('stats.line.books', { n: c.books ?? 0 }),
    t('stats.line.audiobooks', { n: c.audiobooks ?? 0 }),
    t('stats.line.comics', { series: t('stats.comicSeries', { count: c.comics ?? 0 }) }),
  ].join(' · ')
}
