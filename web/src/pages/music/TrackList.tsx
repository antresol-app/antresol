// The tracks of an album: number (a play glyph on hover, an animated
// indicator on the track that is playing), title, the artist when it differs
// from the album's, duration. «Диск N» headers when there are several discs.
// The `songs` variant (playlists, «Песни», an artist's top songs) shows a
// small cover instead of the number and always the artist. Every row has
// the song's «Ещё» menu. A playlist can add the songs its import has not
// brought as dimmed rows in their places: «Ещё не скачано» while on its way,
// otherwise why not («Пропущено», «не найдено в источниках», …). Where the
// server fetches nothing (an admin: GET /api/import/services `downloads`;
// another profile: no addon that adds titles, useCanAdd) it says «Нет в
// медиатеке» for what the library lacks.
import { Fragment, useEffect, useState, type ReactNode } from 'react'
import { Artwork } from '../../ui/Artwork'
import type { Song } from '../../api/types'
import { getImportServices } from '../../api/client'
import { Icon } from '../../components/Icon'
import { useCanAddState } from '../../addons/slots'
import { useSession } from '../../state/session'
import { useAudioSelect } from '../../player/audio/useAudio'
import { clock } from '../../player/timeline'
import { SongMenu } from './SongMenu'
import styles from './TrackList.module.css'
import { t as tr, useLang, useT } from '../../i18n'

export interface TrackListProps {
  tracks: Song[]
  /** Artist of the album: a track's own artist shows only when different. */
  albumArtist: string
  /** Index into `tracks`. */
  onPlay: (index: number) => void
  variant?: 'album' | 'songs'
  /** The «Ещё» menu of a row; default SongMenu, false for none. */
  menu?: ((t: Song, index: number) => ReactNode) | false
  /** Accessible name of the list (songs variant). */
  label?: string
  /** Second line in the songs variant: the artist (default), the album (an artist's own page) or «артист · альбом» (search). */
  subtitle?: 'artist' | 'album' | 'both'
  /** Songs variant: rows of songs not downloaded yet, by their place in the whole list (0-based). */
  pending?: { position: number; artist: string; title: string; state?: string; waitingFor?: string }[]
}

/**
 * What a pending row says: why the song is not here; «Ещё не скачано» only
 * while it is on its way. `local`: nothing can bring it (the server
 * downloads nothing, useServerDownloads), so every song the library lacks is
 * «Нет в медиатеке».
 */
export function pendingLabel(state: string | undefined, waitingFor: string | undefined, local: boolean): string {
  if (state === 'skipped') return tr('music.pending.skipped')
  if (state === 'failed') return tr('common.error')
  if (local) return tr('music.pending.notInLibrary')
  switch (state) {
    case 'waiting_download':
      return waitingFor ? tr('music.pending.waitingFor', { name: waitingFor }) : tr('music.pending.waiting')
    case 'not_found':
      return tr('music.pending.notFound')
    case 'waiting_space':
      return tr('common.download.waitingSpace')
    case 'searching':
      return tr('music.pending.searching')
    default:
      return tr('music.pending.notYet')
  }
}

/** Which track each pending row goes before (tracks.length: after the last). */
export function pendingSlots(pending: { position: number }[], tracks: number): number[] {
  // a row at place p has p − (pending rows before it) tracks above it
  return pending.map((p, k) => Math.max(0, Math.min(tracks, p.position - k)))
}

/**
 * Whether the server brings what the library lacks, asked only when `asked`
 * (a list with pending rows). An admin: the import's own answer (GET
 * /api/import/services `downloads`: false where no addon takes the albums).
 * Another profile cannot ask it (the route is the admins'): whether an addon
 * adds titles (useCanAdd). Not known yet (loading, or the answer failed):
 * true, so a pending row is not «Нет в медиатеке» first.
 */
function useServerDownloads(asked: boolean): boolean {
  const { user } = useSession()
  const canAdd = useCanAddState()
  const admin = asked && Boolean(user?.isAdmin)
  const [answer, setAnswer] = useState<{ user: number; downloads: boolean } | null>(null)
  useEffect(() => {
    if (!admin || !user) return
    const ctl = new AbortController()
    getImportServices(ctl.signal).then(
      (r) => setAnswer({ user: user.id, downloads: r.downloads }),
      () => undefined,
    )
    return () => ctl.abort()
  }, [admin, user?.id])
  if (admin) return answer && answer.user === user?.id ? answer.downloads : true
  return canAdd ?? true
}

function PendingRow({ artist, title, state, waitingFor, downloads }: { artist: string; title: string; state?: string; waitingFor?: string; downloads: boolean }) {
  useLang() // pendingLabel speaks the interface language
  return (
    <li className={`${styles.item} ${styles.pending}`}>
      <span className={styles.row} aria-disabled="true">
        <span className={styles.num}>
          <span className={styles.thumb}>
            <Icon name={downloads ? 'download' : 'music'} size={16} />
          </span>
        </span>
        <span className={styles.text}>
          <span className={styles.title}>
            {artist} — {title}
          </span>
          <span className={styles.badge}>{pendingLabel(state, waitingFor, !downloads)}</span>
        </span>
      </span>
    </li>
  )
}

function Thumb({ src }: { src: string }) {
  return (
    <span className={styles.thumb}>
      <Artwork src={src} width={40} fallback={<Icon name="music" size={16} />} />
    </span>
  )
}

function Playing({ paused }: { paused: boolean }) {
  return (
    <span className={`${styles.bars} ${paused ? styles.paused : ''}`} aria-hidden>
      <span />
      <span />
      <span />
    </span>
  )
}

export function TrackList({ tracks, albumArtist, onPlay, variant = 'album', menu, label, subtitle = 'artist', pending }: TrackListProps) {
  // Only the current track and play state: no re-render on every timeupdate.
  const tx = useT()
  const nowId = useAudioSelect((s) => (s.kind === 'music' ? (s.queue[s.index]?.trackId ?? 0) : 0))
  const playing = useAudioSelect((s) => s.playing)
  const songs = variant === 'songs'
  const discs = songs ? [1] : [...new Set(tracks.map((t) => t.discNo || 1))]
  const multi = discs.length > 1
  const waiting = songs && pending?.length ? [...pending].sort((a, b) => a.position - b.position) : []
  const slots = pendingSlots(waiting, tracks.length)
  const downloads = useServerDownloads(waiting.length > 0)
  const pendingAt = (i: number) =>
    waiting.map((p, k) => (slots[k] === i ? <PendingRow key={`pending-${p.position}-${k}`} artist={p.artist} title={p.title} state={p.state} waitingFor={p.waitingFor} downloads={downloads} /> : null))
  const menuOf = (t: Song, i: number) =>
    menu === false ? null : <span className={styles.menu}>{menu ? menu(t, i) : <SongMenu song={t} hide={{ album: !songs }} />}</span>

  return (
    <div className={styles.list}>
      {discs.map((disc) => (
        <section key={disc} className={styles.disc} aria-label={multi ? tx('music.disc', { n: disc }) : undefined}>
          {multi && <h2 className={styles.discTitle}>{tx('music.disc', { n: disc })}</h2>}
          <ol className={`${styles.rows} ${songs ? styles.songs : ''}`} aria-label={label}>
            {tracks.map((t, i) => {
              if (!songs && (t.discNo || 1) !== disc) return null
              const current = nowId === t.id
              const inDisc = tracks.slice(0, i + 1).filter((x) => (x.discNo || 1) === disc).length
              const second =
                songs && subtitle === 'both'
                  ? [t.artist, t.album].filter(Boolean).join(' · ')
                  : songs && subtitle === 'album'
                    ? t.album
                    : t.artist && (songs || t.artist !== albumArtist)
                      ? t.artist
                      : ''
              return (
                <Fragment key={`${t.id}-${i}`}>
                  {pendingAt(i)}
                  <li className={styles.item}>
                    <button
                      type="button"
                      className={`${styles.row} ${current ? styles.current : ''}`}
                      aria-current={current ? 'true' : undefined}
                      onClick={() => onPlay(i)}
                    >
                      <span className="visually-hidden">{current ? tx('music.nowPlayingPrefix') : tx('music.playPrefix')}</span>
                      <span className={styles.num}>
                        {songs ? (
                          <>
                            <Thumb src={t.cover} />
                            {current ? (
                              <span className={styles.thumbNow}>
                                <Playing paused={!playing} />
                              </span>
                            ) : (
                              <span className={`${styles.hoverPlay} ${styles.thumbNow}`}>
                                <Icon name="play" size={14} />
                              </span>
                            )}
                          </>
                        ) : current ? (
                          <Playing paused={!playing} />
                        ) : (
                          <>
                            <span className={styles.no} aria-hidden>
                              {t.trackNo || inDisc}
                            </span>
                            <span className={styles.hoverPlay}>
                              <Icon name="play" size={14} />
                            </span>
                          </>
                        )}
                      </span>
                      <span className={styles.text}>
                        <span className={styles.title}>{t.title}</span>
                        {second && <span className={styles.artist}>{second}</span>}
                      </span>
                      <span className={styles.time}>{t.durationMs > 0 ? clock(t.durationMs / 1000) : ''}</span>
                    </button>
                    {menuOf(t, i)}
                  </li>
                </Fragment>
              )
            })}
            {pendingAt(tracks.length)}
          </ol>
        </section>
      ))}
    </div>
  )
}
