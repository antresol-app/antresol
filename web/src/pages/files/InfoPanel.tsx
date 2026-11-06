// «Свойства»: Finder's preview pane / Get Info. Picture, name, kind and size,
// then dates and location, the library item and download the file belongs to,
// and for media the codecs, resolution, duration and tracks.
import { IconButton } from '../../ui/IconButton'
import type { ReactNode } from 'react'
import { Link } from 'react-router'
import type { FileEntry, FileProbe, FileStat } from '../../api/types'
import { formatDuration, formatLongDate, formatSize, kindLabel, objects } from './model'
import { Glyph, itemGlyph } from './glyphs'
import { itemHref } from '../../components/Card'
import { Thumb } from './Thumb'
import { useStat } from './useDir'
import styles from './InfoPanel.module.css'
import { locale, t, useT, type Key } from '../../i18n'

const ITEM_KIND: Record<string, Key> = {
  movie: 'common.kind.movie',
  series: 'common.kind.series',
  season: 'common.kind.season',
  episode: 'common.kind.episode',
  album: 'common.kind.album',
  track: 'files.item.track',
  book: 'common.kind.book',
  audiobook: 'common.kind.audiobook',
  comic: 'common.kind.manga',
  comic_volume: 'files.item.volume',
}

const LANGS: Record<string, string> = {
  rus: 'ru', ru: 'ru', eng: 'en', en: 'en', ukr: 'uk', jpn: 'ja', fre: 'fr', fra: 'fr', ger: 'de', deu: 'de',
  spa: 'es', ita: 'it', chi: 'zh', zho: 'zh', kor: 'ko', pol: 'pl', por: 'pt', tur: 'tr', heb: 'he', ara: 'ar',
}
const names = new Map<string, Intl.DisplayNames>()
export function langName(code: string): string {
  if (!code || code === 'und') return ''
  const c = LANGS[code.toLowerCase()] ?? code
  try {
    const l = locale()
    let dn = names.get(l)
    if (!dn) names.set(l, (dn = new Intl.DisplayNames([l], { type: 'language' })))
    const n = dn.of(c)
    return n ? n[0]!.toUpperCase() + n.slice(1) : code
  } catch {
    return code
  }
}

const CODECS: Record<string, string> = {
  h264: 'H.264', hevc: 'HEVC', h265: 'HEVC', av1: 'AV1', vp9: 'VP9', mpeg4: 'MPEG-4', mpeg2video: 'MPEG-2',
  aac: 'AAC', ac3: 'Dolby Digital', eac3: 'Dolby Digital Plus', dts: 'DTS', truehd: 'Dolby TrueHD', flac: 'FLAC',
  mp3: 'MP3', opus: 'Opus', vorbis: 'Vorbis', pcm_s16le: 'PCM', alac: 'ALAC',
  subrip: 'SRT', srt: 'SRT', ass: 'ASS', ssa: 'SSA', webvtt: 'WebVTT', hdmv_pgs_subtitle: 'PGS', dvd_subtitle: 'VobSub',
}
export const codecName = (c: string) => (c.toLowerCase() === 'mov_text' ? t('files.kind.text') : (CODECS[c.toLowerCase()] ?? c.toUpperCase()))
const channels = (n: number) => (n === 1 ? t('files.ch.mono') : n === 2 ? t('files.ch.stereo') : n === 6 ? '5.1' : n === 8 ? '7.1' : n ? t('files.ch.n', { n }) : '')

const TIERS: [long: number, short: number, tag: string][] = [
  [3200, 2000, '4K'],
  [2300, 1400, '1440p'],
  [1800, 1000, '1080p'],
  [1200, 700, '720p'],
]

// The tier by both sides (as the server's): a 1920×800 scope film is 1080p.
export function resolution(w: number, h: number) {
  const long = Math.max(w || h, h)
  const short = Math.min(w || h, h)
  const tier = TIERS.find(([l, s]) => long >= l || short >= s)
  const tag = !h ? '' : tier ? tier[2] : `${h}p`
  return `${w}×${h}${tag ? ` (${tag})` : ''}`
}

function Rows({ children }: { children: ReactNode }) {
  return <dl className={styles.rows}>{children}</dl>
}
function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className={styles.row}>
      <dt>{label}</dt>
      <dd>{children}</dd>
    </div>
  )
}
function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className={styles.section} aria-label={title}>
      <h3 className={styles.sectionTitle}>{title}</h3>
      {children}
    </section>
  )
}

function Media({ probe }: { probe: FileProbe }) {
  const t = useT()
  return (
    <>
      <Section title={t('files.info.media')}>
        <Rows>
          {probe.duration > 0 && <Row label={t('files.info.duration')}>{formatDuration(probe.duration)}</Row>}
          {probe.video && (
            <>
              <Row label={t('files.info.resolution')}>{resolution(probe.video.width, probe.video.height)}</Row>
              <Row label={t('files.info.video')}>
                {codecName(probe.video.codec)}
                {probe.video.hdr ? ' · HDR' : ''}
              </Row>
            </>
          )}
          {probe.container && <Row label={t('files.info.container')}>{probe.container.split(',')[0]!.toUpperCase()}</Row>}
        </Rows>
      </Section>
      {probe.audio.length > 0 && (
        <Section title={t('files.info.audioTracks', { n: probe.audio.length })}>
          <ul className={styles.tracks}>
            {probe.audio.map((a, i) => (
              <li key={i}>
                <span>{a.title || langName(a.lang) || t('files.info.track', { n: i + 1 })}</span>
                <span className={styles.trackMeta}>{[codecName(a.codec), channels(a.channels)].filter(Boolean).join(' · ')}</span>
              </li>
            ))}
          </ul>
        </Section>
      )}
      {probe.subs.length > 0 && (
        <Section title={t('files.info.subtitles', { n: probe.subs.length })}>
          <ul className={styles.tracks}>
            {probe.subs.map((s, i) => (
              <li key={i}>
                <span>{s.title || langName(s.lang) || t('files.info.subtitle', { n: i + 1 })}</span>
                <span className={styles.trackMeta}>{[codecName(s.codec), s.forced ? t('files.info.forced') : ''].filter(Boolean).join(' · ')}</span>
              </li>
            ))}
          </ul>
        </Section>
      )}
    </>
  )
}

function sizeOf(e: FileEntry, stat?: FileStat, gaveUp?: boolean): ReactNode {
  if (!e.isDir) return formatSize(e.size)
  if (stat?.dirSize !== undefined) return formatSize(stat.dirSize)
  if (gaveUp) return <span className={styles.pending}>{t('files.info.sizeFailed')}</span>
  if (stat?.dirSizePending)
    return (
      <span className={styles.pending}>
        <span className={styles.spinner} aria-hidden /> {t('files.info.counting')}
      </span>
    )
  return '—'
}

export interface InfoContentProps {
  root: string
  entry: FileEntry
  /** «Фильмы › Интерстеллар (2014)» */
  where: string
  /** Column view preview: picture and a short list only. */
  compact?: boolean
  refresh?: number
}

export function InfoContent({ root, entry, where, compact, refresh }: InfoContentProps) {
  const { stat, error, gaveUp } = useStat(root, entry.path, refresh)
  const t = useT()
  const e = stat ?? entry
  const item = e.item
  return (
    <div className={`${styles.content} ${compact ? styles.compact : ''}`}>
      <div className={styles.preview}>
        <Thumb entry={e} size={compact ? 150 : 176} eager />
      </div>
      <h2 className={styles.name}>{e.name}</h2>
      <p className={styles.kind}>
        {kindLabel(e)} · {e.isDir ? (e.childCount !== undefined ? objects(e.childCount) : '') : formatSize(e.size)}
      </p>

      {item && (
        <Link to={itemHref(item.kind, item.id)} className={styles.itemLink}>
          <Glyph name={itemGlyph(item.kind)} size={18} />
          <span className={styles.itemText}>
            <span className={styles.itemLabel}>{t('files.info.inLibrary', { kind: t(ITEM_KIND[item.kind] ?? 'files.item.object') })}</span>
            <span className={styles.itemTitle}>{item.title}</span>
          </span>
          <Glyph name="chevronRight" size={14} strokeWidth={2.2} className={styles.itemChevron} />
        </Link>
      )}

      {error && (
        <p className={styles.statError} role="alert">
          {error}
        </p>
      )}
      <Section title={t('files.info.general')}>
        <Rows>
          <Row label={t('files.col.kind')}>{kindLabel(e)}</Row>
          <Row label={t('files.col.size')}>{sizeOf(e, stat, gaveUp)}</Row>
          {e.isDir && e.childCount !== undefined && <Row label={t('files.info.contains')}>{objects(e.childCount)}</Row>}
          <Row label={t('files.col.modified')}>{formatLongDate(e.mtime)}</Row>
          {!compact && <Row label={t('files.info.where')}>{where}</Row>}
          {!__LIBRARY_EDITION__ && e.torrent && (
            <Row label={t('files.info.torrent')}>
              {e.torrent.seeding ? (
                <span className={styles.seeding}>
                  {t('files.info.seeding')} <Glyph name="checkCircle" size={14} label={t('files.info.yes')} />
                </span>
              ) : (
                t('files.info.downloading')
              )}
            </Row>
          )}
        </Rows>
      </Section>
      {stat?.probe && !compact && <Media probe={stat.probe} />}
      {stat?.probe && compact && stat.probe.duration > 0 && (
        <div className={styles.compactMedia}>
        <Rows>
          <Row label={t('files.info.duration')}>{formatDuration(stat.probe.duration)}</Row>
          {stat.probe.video && <Row label={t('files.info.resolution')}>{resolution(stat.probe.video.width, stat.probe.video.height)}</Row>}
        </Rows>
        </div>
      )}
    </div>
  )
}

export interface InfoPanelProps {
  root: string | null
  selected: FileEntry[]
  /** The folder being shown, when nothing is selected. */
  folder: FileEntry | null
  whereOf: (e: FileEntry) => string
  onClose: () => void
  refresh?: number
}

export function InfoPanel({ root, selected, folder, whereOf, onClose, refresh }: InfoPanelProps) {
  const one = selected.length === 1 ? selected[0]! : selected.length === 0 ? folder : null
  const t = useT()
  return (
    <aside className={styles.panel} aria-label={t('files.info')}>
      <header className={styles.head}>
        <span className={styles.headTitle}>{t('files.info')}</span>
        <IconButton size="xs" variant="plain" icon="close" label={t('files.info.close')} onClick={onClose} />
      </header>
      {root && one ? (
        <InfoContent key={one.path} root={root} entry={one} where={whereOf(one)} refresh={refresh} />
      ) : selected.length > 1 ? (
        <div className={styles.many}>
          <div className={styles.stack} aria-hidden>
            {selected.slice(0, 3).map((e, i) => (
              <span key={e.path} style={{ transform: `rotate(${(i - 1) * 6}deg)` }}>
                <Thumb entry={e} size={96} />
              </span>
            ))}
          </div>
          <h2 className={styles.name}>{t('files.info.selected', { objects: objects(selected.length) })}</h2>
          <p className={styles.kind}>
            {formatSize(selected.reduce((a, e) => a + (e.isDir ? 0 : e.size), 0))}
            {selected.some((e) => e.isDir) ? t('files.info.noFolders') : ''}
          </p>
        </div>
      ) : (
        <p className={styles.nothing}>{t('files.info.nothing')}</p>
      )}
    </aside>
  )
}
