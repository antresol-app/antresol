// The «Ещё» menu of a song: queue it, add it to a playlist, like it,
// download the file, go to its artist or album. Callers can add their own
// items (a playlist adds «Убрать из плейлиста» and moving).
import { useEffect, useRef, useState, type ReactNode } from 'react'
import { Toast } from '../../ui/Toast'
import { useNavigate } from 'react-router'
import { ApiError, downloadUrl, star, unstar } from '../../api/client'
import type { Playlist, Song } from '../../api/types'
import { Icon } from '../../components/Icon'
import { IconButton } from '../../ui/IconButton'
import { MenuItem, MenuSeparator, Menu } from '../../ui/Menu'
import { audio } from '../../player/audio/engine'
import { songItems } from '../../player/audio/queue'
import { AddToPlaylistSheet } from './AddToPlaylistSheet'
import { useT } from '../../i18n'
import { useSession } from '../../state/session'
import { useScreenLimits } from '../../state/screenTime'

export interface SongMenuProps {
  song: Song
  /** Leaves out «Перейти к альбому» / «Перейти к артисту» where we already are. */
  hide?: { album?: boolean; artist?: boolean }
  /** Extra items at the end (each gets `close`). */
  extra?: (close: () => void) => ReactNode
  onStarChange?: (starred: boolean) => void
  /** The song went into a playlist (the open playlist reloads when it is that one). */
  onAdded?: (p: Playlist) => void
  className?: string
}

export function SongMenu({ song, hide, extra, onStarChange, onAdded, className }: SongMenuProps) {
  const t = useT()
  // a kid whose music is limited does not download («Экранное время»)
  const noDownload = useScreenLimits(Boolean(useSession().user?.isKid)).audio
  const navigate = useNavigate()
  const [starred, setStarred] = useState(song.starred)
  const [adding, setAdding] = useState(false)
  // a short toast: an error (alert) or «Добавлено в «X»» (status)
  const [toast, setToast] = useState<{ text: string; error: boolean } | null>(null)
  const errTimer = useRef<ReturnType<typeof setTimeout> | null>(null)

  // follow a reload of the list
  useEffect(() => setStarred(song.starred), [song.starred])
  useEffect(() => () => void (errTimer.current && clearTimeout(errTimer.current)), [])

  const show = (text: string, error: boolean) => {
    setToast({ text, error })
    if (errTimer.current) clearTimeout(errTimer.current)
    errTimer.current = setTimeout(() => setToast(null), error ? 4000 : 2500)
  }
  const showError = (text: string) => show(text, true)

  const toggleStar = () => {
    const next = !starred
    setStarred(next)
    ;(next ? star(song.id) : unstar(song.id))
      .then(() => onStarChange?.(next))
      .catch((e: unknown) => {
        setStarred(!next)
        showError(e instanceof ApiError && e.status !== 0 ? e.message : t('music.song.starFailed'))
      })
  }

  return (
    <>
      <Menu
        label={t('music.song.actions', { title: song.title })}
        placement="bottom-end"
        trigger={(p) => (
          <IconButton {...p} size="sm" variant="plain" icon="ellipsis" className={className} label={t('common.more')} />
        )}
      >
        {(close) => (
          <>
            <MenuItem
              label={t('music.song.playNext')}
              icon={<Icon name="queue" size={17} />}
              onSelect={() => {
                close(false)
                audio.playNext(songItems([song]))
              }}
            />
            <MenuItem
              label={t('music.song.addToQueue')}
              icon={<Icon name="list" size={17} />}
              onSelect={() => {
                close(false)
                audio.addToQueue(songItems([song]))
              }}
            />
            <MenuItem
              label={t('music.song.addToPlaylist')}
              icon={<Icon name="plus" size={17} />}
              onSelect={() => {
                close(false)
                setAdding(true)
              }}
            />
            <MenuSeparator />
            <MenuItem
              label={starred ? t('music.song.unfavorite') : t('music.song.favorite')}
              icon={<Icon name={starred ? 'heartFill' : 'heart'} size={17} />}
              onSelect={() => {
                close(false)
                toggleStar()
              }}
            />
            {song.fileId > 0 && !noDownload && (
              <MenuItem
                // a CUE slice lives inside the album image: that file is what downloads
                label={song.cue ? t('music.song.downloadAlbumFile') : t('music.song.download')}
                detail={song.cue ? t('music.song.cueDetail') : t('music.song.downloadDetail')}
                icon={<Icon name="download" size={17} />}
                href={downloadUrl(song.fileId)}
                download
                onSelect={() => close(false)}
              />
            )}
            {((!hide?.artist && song.artistId > 0) || (!hide?.album && song.albumId > 0)) && <MenuSeparator />}
            {!hide?.artist && song.artistId > 0 && (
              <MenuItem
                label={t('music.song.goToArtist')}
                icon={<Icon name="person" size={17} />}
                onSelect={() => {
                  close(false)
                  navigate(`/music/artist/${song.artistId}`)
                }}
              />
            )}
            {!hide?.album && song.albumId > 0 && (
              <MenuItem
                label={t('music.song.goToAlbum')}
                icon={<Icon name="music" size={17} />}
                onSelect={() => {
                  close(false)
                  navigate(`/music/album/${song.albumId}`)
                }}
              />
            )}
            {extra && (
              <>
                <MenuSeparator />
                {extra(() => close(false))}
              </>
            )}
          </>
        )}
      </Menu>
      {adding && (
        <AddToPlaylistSheet
          trackIds={[song.id]}
          onClose={() => setAdding(false)}
          onDone={(p) => {
            show(t('music.song.addedTo', { name: p.name }), false)
            onAdded?.(p)
          }}
        />
      )}
      <Toast messageKey={toast?.text} tone={toast?.error ? 'error' : 'default'}>
        {toast?.text}
      </Toast>
    </>
  )
}
