// The row of actions on an item page: the main «Смотреть»/«Продолжить с …»
// button (▾ with other ways of watching, `moreWays`), the round download
// button (to this device), «Хочу посмотреть». A title that is not on the
// server brings its own «Смотреть ▾» and download button (`primary`,
// `download`). Less frequent actions (start over, another version, mark as
// watched) live in the «Ещё» menu.
import { useEffect, useState, type ReactNode } from 'react'
import { addToWatchlist, downloadUrl, removeFromWatchlist, setWatched, errorText } from '../api/client'
import type { Episode, ItemDetail, MediaFile } from '../api/types'
import { clock, durationSec, FINISHED, humanMinutes, isMultiPart, langBadge, partNumber, playbackParts, resumeSec } from '../player/timeline'
import { ActionTiles } from '../ui/ActionTiles'
import { MaterialContext } from '../ui/Button'
import { IconButton } from '../ui/IconButton'
import { ProgressBar } from '../ui/ProgressBar'
import { DeleteItem, DeleteItemSheet, type Deleted } from './DeleteItem'
import { DownloadButton } from './DownloadButton'
import { episodeCode } from './EpisodeList'
import { Icon } from './Icon'
import { MenuItem, MenuSeparator, Menu } from '../ui/Menu'
import { WatchButton, type WatchWay } from './WatchButton'
import { discardProgress } from '../offline/store'
import { useIsCompact } from '../state/useMediaQuery'
import styles from './ActionBar.module.css'
import { getLanguage, t, useT } from '../i18n'

export interface PlayOptions {
  /** Start from the beginning instead of the saved position. */
  fromStart?: boolean
  /** Another version of the movie. */
  fileId?: number
}

export interface ActionBarProps {
  item: ItemDetail
  onPlay: (opts?: PlayOptions) => void
  /** Called after something changed on the server (watched state). */
  onChanged?: () => void
  /** Ways after «С сервера» in the ▾ menu (the sources build's «Онлайн»). */
  moreWays?: WatchWay[]
  /** Replaces the built-in «Смотреть» (a title not on the server: «Смотреть ▾»). */
  primary?: ReactNode
  /** Rendered right after `primary`; replaces the library's download button (to this device). */
  download?: ReactNode
  /** Round buttons before «Хочу посмотреть» («Добавить сезон…», «Следить»): phone tiles too. */
  toggles?: ReactNode
  /** false: the page has its own «Хочу посмотреть» (the mark, tracking); default true. */
  watchlistButton?: boolean
  /** The admin's «Удалить»: the red round button at the end of the row on a
   *  wide screen; on a phone the last item of «Ещё» (as in Apple's apps, a
   *  destructive action lives in the menu, and the tiles keep their room). */
  deletion?: { itemId: number; title: string; onDeleted: (d: Deleted) => void }
  /** Admin: «Найти субтитры…» in «Ещё» (OpenSubtitles search for the file). */
  onFindSubtitles?: () => void
  /** Sources build, adults: «Найти озвучку…» in «Ещё» (VoiceOverSheet). */
  onFindVoiceOvers?: () => void
}

const units = ['common.unit.B', 'common.unit.KB', 'common.unit.MB', 'common.unit.GB', 'common.unit.TB'] as const
/** «2,4 ГБ» / "2.4 GB" in the interface language. */
export function formatSize(bytes: number): string {
  let v = bytes
  let u = 0
  while (v >= 1024 && u < units.length - 1) {
    v /= 1024
    u++
  }
  const n = v >= 100 || u === 0 ? Math.round(v).toString() : v.toFixed(1).replace('.', getLanguage() === 'ru' ? ',' : '.')
  return `${n} ${t(units[u]!)}`
}


/** The episode «Смотреть» starts on a series or season page. */
export function episodeToPlay(item: ItemDetail): Episode | undefined {
  if (item.next) return item.next
  const all = item.seasons.flatMap((s) => s.episodes)
  const regular = all.filter((e) => e.seasonNo > 0)
  return regular.find((e) => !e.watched) ?? regular[0] ?? all[0]
}

function totalSeconds(item: ItemDetail): number {
  const parts = playbackParts(item.files)
  const files = parts.reduce((a, f) => a + durationSec(f), 0)
  return files > 0 ? files : (item.runtime ?? 0) * 60
}

/** Languages of the tracks, once each: «RU, EN». */
function langs(tracks: { lang: string }[]): string {
  return [...new Set(tracks.map((x) => langBadge(x.lang)).filter(Boolean))].join(', ')
}

/**
 * A download row's badges from the file's real tracks: «2160p · RU, EN · Субтитры: EN · 4,0 ГБ».
 * `quality: false` leaves the resolution out when the row's label already says it.
 */
function fileBadges(f: MediaFile, quality = true): string {
  const subs = langs(f.subs)
  return [quality && f.resolution, langs(f.audio), subs && t('comp.action.subs', { langs: subs }), formatSize(f.size)].filter(Boolean).join(' · ')
}

export function ActionBar({ item, onPlay, onChanged, moreWays = [], primary, download, toggles, watchlistButton = true, deletion, onFindSubtitles, onFindVoiceOvers }: ActionBarProps) {
  const t = useT()
  const [inList, setInList] = useState(item.inWatchlist)
  const [busy, setBusy] = useState(false)
  const [problem, setProblem] = useState('')
  const [deleting, setDeleting] = useState(false)
  const compact = useIsCompact()
  useEffect(() => setInList(item.inWatchlist), [item.inWatchlist])
  const hasMore = item.actions.play || item.files.length > 0
  const deleteInMenu = !!deletion && compact && hasMore

  const isShow = item.kind === 'series' || item.kind === 'season'
  const ep = isShow ? episodeToPlay(item) : undefined
  const total = isShow ? (ep?.durationMs ?? 0) / 1000 : totalSeconds(item)
  const progress = isShow ? (ep?.progress ?? 0) : item.progress
  const resume = resumeSec(progress, total)
  const watched = isShow
    ? item.seasons.length > 0 && item.seasons.every((s) => s.episodes.every((e) => e.watched))
    : item.progress >= FINISHED

  let playLabel = t('comp.action.watch')
  if (isShow && ep) playLabel = t(resume > 0 ? 'comp.action.resumeEpisode' : 'comp.action.watchEpisode', { code: episodeCode(ep) })
  else if (resume > 0) playLabel = t('comp.action.resumeFrom', { time: clock(resume) })

  const multiPart = isMultiPart(item.files)
  const versions = multiPart ? [] : item.files
  const remaining = resume > 0 && total > 0 ? humanMinutes((total - resume) / 60) : ''

  // To this device. The browser owns the download, so there is no ring: the
  // page cannot see its progress (and reading a 20 GB file into it is no option).
  const files = item.actions.toDevice ? item.files : []
  let toDevice: ReactNode = null
  if (files.length === 1) toDevice = <DownloadButton state={{ kind: 'idle' }} href={downloadUrl(files[0]!.id)} />
  else if (files.length > 1) {
    const parts = playbackParts(files).length > 1
    const rows = parts ? [...files].sort((a, b) => partNumber(a.name) - partNumber(b.name)) : files
    toDevice = (
      <Menu
        label={t('comp.action.toDeviceMenu')}
        placement="bottom-start"
        trigger={(p) => <DownloadButton state={{ kind: 'idle' }} triggerProps={p} />}
      >
        {(close) =>
          rows.map((f) => (
            <MenuItem
              key={f.id}
              label={
                parts ? t('comp.action.part', { n: partNumber(f.name) }) : f.resolution ? t('comp.action.qualityN', { q: f.resolution }) : f.name
              }
              detail={fileBadges(f, parts || !f.resolution)}
              href={downloadUrl(f.id)}
              download
              onSelect={() => close()}
            />
          ))
        }
      </Menu>
    )
  }

  const lead =
    primary ??
    (item.actions.play && (
      <WatchButton
        label={playLabel}
        // a library item always plays the server's file first: no remembered way here
        ways={[{ id: 'server', label: t('comp.way.server'), run: () => onPlay() }, ...moreWays]}
        current="server"
      />
    ))
  const dl = download ?? toDevice

  const progressRow = resume > 0 && total > 0 && (
    <div className={styles.progressRow}>
      <ProgressBar className={styles.progress} value={resume / total} tone="label" label={t('comp.action.watched')} />
      <span>{t('comp.action.remaining', { time: remaining })}</span>
    </div>
  )

  const toggleList = async () => {
    const next = !inList
    setInList(next)
    setProblem('')
    try {
      await (next ? addToWatchlist(item.id) : removeFromWatchlist(item.id))
    } catch (e) {
      setInList(!next)
      setProblem(errorText(e, t('comp.action.failed')))
    }
  }

  const toggleWatched = async () => {
    setBusy(true)
    setProblem('')
    try {
      const markedAt = Date.now()
      await setWatched(item.id, !watched)
      discardProgress([item.id, ...(item.seasons ?? []).flatMap((s) => s.episodes.map((e) => e.id))], markedAt)
      onChanged?.()
    } catch (e) {
      setProblem(errorText(e, t('comp.action.markFailed')))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className={styles.wrap}>
      {/* the title's action row floats over its artwork: round and secondary buttons are clear glass */}
      <MaterialContext.Provider value="clear">
      <div className={styles.bar}>
        {(lead || dl) && (
          // one line on phones: «Смотреть ▾» takes the width, the round button sits beside it
          <div className={styles.lead}>
            {lead && (
              // the watch progress hangs under «Продолжить», centred on it
              <div className={styles.play}>
                {lead}
                {progressRow}
              </div>
            )}
            {dl}
          </div>
        )}
        {/* Secondary actions: a row of equal tiles on phones. */}
        <ActionTiles>
          {toggles}
          {item.id > 0 && watchlistButton && (
            <IconButton
              size="lg"
              icon="watchlist"
              toggled={inList}
              label={t('comp.action.watchlist')}
              tile
              tooltip={inList ? t('comp.action.watchlistOn') : t('comp.action.watchlistAdd')}
              onClick={() => void toggleList()}
            />
          )}
          {hasMore && (
            <Menu
              label={t('comp.action.more')}
              placement="bottom-end"
              trigger={(p) => (
                <IconButton {...p} size="lg" icon="ellipsis" label={t('comp.action.more')} tile />
              )}
            >
              {(close) => (
                <>
                  {resume > 0 && (
                    <MenuItem
                      icon={<Icon name="restart" size={18} />}
                      label={t('comp.action.fromStart')}
                      onSelect={() => {
                        close(false)
                        onPlay({ fromStart: true })
                      }}
                    />
                  )}
                  {versions.length > 1 &&
                    versions.map((f) => (
                      <MenuItem
                        key={f.id}
                        icon={<Icon name="play" size={16} />}
                        label={f.resolution ? t('comp.action.watchIn', { q: f.resolution }) : t('comp.action.watchInOther')}
                        detail={fileBadges(f, false)} // «Смотреть в 2160p» already names the quality
                        onSelect={() => {
                          close(false)
                          onPlay({ fileId: f.id })
                        }}
                      />
                    ))}
                  {(resume > 0 || versions.length > 1) && <MenuSeparator />}
                  <MenuItem
                    icon={<Icon name="checkCircle" size={18} />}
                    label={watched ? t('comp.action.markUnwatched') : t('comp.action.markWatched')}
                    disabled={busy}
                    onSelect={() => {
                      close()
                      void toggleWatched()
                    }}
                  />
                  {onFindSubtitles && (
                    <MenuItem
                      icon={<Icon name="captions" size={18} />}
                      label={t('comp.action.findSubtitles')}
                      onSelect={() => {
                        close(false)
                        onFindSubtitles()
                      }}
                    />
                  )}
                  {!__LIBRARY_EDITION__ && onFindVoiceOvers && (
                    <MenuItem
                      icon={<Icon name="speaker" size={18} />}
                      label={t('vo.find')}
                      onSelect={() => {
                        close(false)
                        onFindVoiceOvers()
                      }}
                    />
                  )}
                  {deleteInMenu && (
                    <>
                      <MenuSeparator />
                      <MenuItem
                        destructive
                        icon={<Icon name="trash" size={18} />}
                        label={t('comp.delete.menu')}
                        onSelect={() => {
                          close(false)
                          setDeleting(true)
                        }}
                      />
                    </>
                  )}
                </>
              )}
            </Menu>
          )}
          {deletion && !deleteInMenu && <DeleteItem itemId={deletion.itemId} title={deletion.title} size="lg" tileLabel onDeleted={deletion.onDeleted} />}
        </ActionTiles>
        {deletion && deleting && (
          <DeleteItemSheet itemId={deletion.itemId} title={deletion.title} onClose={() => setDeleting(false)} onDeleted={deletion.onDeleted} />
        )}
      </div>
      </MaterialContext.Provider>
      {problem && (
        <p className={styles.problem} role="alert">
          {problem}
        </p>
      )}
      {!lead && progressRow}
    </div>
  )
}
