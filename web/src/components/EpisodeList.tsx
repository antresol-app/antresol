// Seasons and episodes of a series: a season switcher and a horizontal row of
// 16:9 episode tiles with progress. Choosing an episode starts playing it.
import { useEffect, useRef, useState } from 'react'
import { imageSrcSet, imageUrl } from '../api/client'
import type { Episode, FamilyMember, Season } from '../api/types'
import { humanMinutes } from '../player/timeline'
import { t as tr, useT } from '../i18n'
import { TileFaces, tileFacesLabel } from './FamilyProgress'
import { Icon } from './Icon'
import { Rail } from './Rail'
import { SegmentedControl } from '../ui/SegmentedControl'
import { IconButton } from '../ui/IconButton'
import styles from './EpisodeList.module.css'

export interface EpisodeListProps {
  seasons: Season[]
  /** Season shown first (the one with the next episode). */
  initialSeason?: number
  nextId?: number
  /** The series' own artwork: an episode that only borrows it gets a neutral tile instead. */
  seriesArt?: { poster?: string; backdrop?: string }
  onPlay: (e: Episode) => void
  /** «На это устройство» for one episode; no button without it. */
  onDownload?: (e: Episode) => void
  /** «Семья»: who of the other profiles is on which episode (their faces on its tile). */
  family?: FamilyMember[]
}

/** «С1 Э4», or «Спецвыпуск 2» for season 0. */
export const episodeCode = (e: Pick<Episode, 'seasonNo' | 'episodeNo'>) =>
  e.seasonNo === 0 ? tr('comp.episode.special', { n: e.episodeNo }) : tr('comp.episode.code', { season: e.seasonNo, episode: e.episodeNo })

export const seasonLabel = (s: Pick<Season, 'number'>) => (s.number === 0 ? tr('comp.episode.specials') : tr('comp.episode.season', { n: s.number }))

export function episodeTitle(e: Episode): string {
  const name = e.title.trim()
  return name && !/^(серия|episode)\s*\d+$/i.test(name) ? name : tr('comp.episode.number', { n: e.episodeNo })
}

function stillOf(ep: Episode, series?: { poster?: string; backdrop?: string }): string | undefined {
  // TMDB stills are stored as the episode's poster; the API fills missing art
  // from the series, and the same series picture on every tile says nothing.
  if (ep.poster && ep.poster !== series?.poster) return ep.poster
  if (ep.backdrop && ep.backdrop !== series?.backdrop) return ep.backdrop
  return undefined
}

function EpisodeTile({
  ep,
  isNext,
  seriesArt,
  family = [],
  onPlay,
}: {
  ep: Episode
  isNext: boolean
  seriesArt?: { poster?: string; backdrop?: string }
  family?: FamilyMember[]
  onPlay: () => void
}) {
  const t = useT()
  const still = stillOf(ep, seriesArt)
  const src = imageUrl(still, 640)
  const [failed, setFailed] = useState(false)
  const pct = Math.round(Math.min(Math.max(ep.progress || 0, 0), 1) * 100)
  const title = episodeTitle(ep)
  const dur = ep.durationMs ? humanMinutes(ep.durationMs / 60000) : ''
  return (
    <button
      type="button"
      className={styles.tile}
      onClick={onPlay}
      aria-label={`${t('comp.episode.aria', { n: ep.episodeNo })}${title !== t('comp.episode.number', { n: ep.episodeNo }) ? `, ${title}` : ''}${ep.watched ? t('comp.episode.ariaWatched') : ''}${tileFacesLabel(family)}`}
    >
      <div className={styles.media}>
        {src && !failed ? (
          <img
            src={src}
            srcSet={imageSrcSet(still, 320, 960)}
            sizes="(max-width: 699px) 80vw, 30vw"
            alt=""
            loading="lazy"
            decoding="async"
            draggable={false}
            onError={() => setFailed(true)}
          />
        ) : (
          <span className={styles.fallback} aria-hidden>
            <span className={styles.fallbackNo}>{ep.episodeNo}</span>
          </span>
        )}
        <span className={styles.playGlyph} aria-hidden>
          <Icon name="play" size={22} />
        </span>
        {ep.watched && (
          <span className={styles.watched} aria-hidden>
            <Icon name="check" size={14} strokeWidth={2.6} />
          </span>
        )}
        <TileFaces members={family} className={styles.faces} />
        {pct > 0 && pct < 100 && (
          <div className={styles.progress} aria-hidden>
            <div style={{ width: `${pct}%` }} />
          </div>
        )}
      </div>
      <div className={styles.text}>
        <span className={styles.eyebrow}>
          {t(ep.seasonNo === 0 ? 'comp.episode.special' : 'comp.episode.number', { n: ep.episodeNo })}
          {isNext && <span className={styles.next}>{t('comp.episode.next')}</span>}
        </span>
        <span className={styles.title}>{title}</span>
        {dur && <span className={styles.runtime}>{dur}</span>}
        {ep.overview && <span className={styles.overview}>{ep.overview}</span>}
      </div>
    </button>
  )
}

export function EpisodeList({ seasons, initialSeason, nextId, seriesArt, onPlay, onDownload, family }: EpisodeListProps) {
  const t = useT()
  const sorted = [...seasons].sort((a, b) => (a.number === 0 ? 1 : b.number === 0 ? -1 : a.number - b.number))
  const first = sorted.find((s) => s.number === initialSeason) ?? sorted[0]
  const [season, setSeason] = useState(first ? String(first.id) : '')
  const rowRef = useRef<HTMLUListElement>(null)

  const current = sorted.find((s) => String(s.id) === season) ?? first

  // Bring the next episode into view inside the row.
  useEffect(() => {
    const el = rowRef.current?.querySelector<HTMLElement>('[data-next="true"]')
    if (el && rowRef.current) rowRef.current.scrollLeft = Math.max(0, el.offsetLeft - rowRef.current.offsetLeft - 40)
  }, [season])

  if (!current) return null
  return (
    <section className={styles.section} aria-label={t('comp.episode.episodes')}>
      <div className={styles.header}>
        {sorted.length > 1 ? (
          <SegmentedControl
            label={t('common.kind.season')}
            segments={sorted.map((s) => ({ value: String(s.id), label: seasonLabel(s) }))}
            value={season}
            onChange={setSeason}
          />
        ) : (
          <h2 className={styles.heading}>{seasonLabel(current)}</h2>
        )}
        <span className={styles.count}>{episodesCount(current.episodes.length)}</span>
      </div>
      <Rail listRef={rowRef} label={seasonLabel(current)}>
        {current.episodes.map((e) => (
          <li key={e.id} className={styles.cell} data-next={e.id === nextId || undefined}>
            <EpisodeTile
              ep={e}
              isNext={e.id === nextId}
              seriesArt={seriesArt}
              family={family?.filter((m) => m.itemId === e.id && !m.finished)}
              onPlay={() => onPlay(e)}
            />
            {onDownload && (
              <IconButton
                icon="download"
                size="xs"
                material="clear"
                className={styles.download}
                label={t('comp.episode.toDevice', { n: e.episodeNo })}
                tooltip={t('comp.episode.toDeviceTitle')}
                onClick={() => onDownload(e)}
              />
            )}
          </li>
        ))}
      </Rail>
    </section>
  )
}

export function episodesCount(n: number): string {
  return tr('comp.episode.count', { count: n })
}
