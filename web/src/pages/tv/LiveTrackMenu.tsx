// «Звук и субтитры» in the live player: the film player's two-column menu
// (TrackMenu's look) with the programme's own voices and subtitle
// languages, labelled by the server («Русский — LostFilm», «Оригинал (fr)»).
import type { ChannelTrack } from '../../api/channels'
import { Icon } from '../../components/Icon'
import { useT } from '../../i18n'
import { Menu, MenuHeading, MenuItem } from '../../ui/Menu'
import styles from './LivePlayer.module.css'

export interface LiveTrackMenuProps {
  audio: ChannelTrack[] | undefined
  subtitles: ChannelTrack[] | undefined
  /** The audio entry playing. */
  audioSlot: string | undefined
  /** The subtitle slot shown; null: off. */
  subSlot: string | null
  onAudio: (slot: string) => void
  onSubtitles: (slot: string | null) => void
  onOpenChange?: (open: boolean) => void
}

export function LiveTrackMenu({ audio, subtitles, audioSlot, subSlot, onAudio, onSubtitles, onOpenChange }: LiveTrackMenuProps) {
  const t = useT()
  const unknown = audio === undefined && subtitles === undefined
  return (
    <Menu
      label={t('tv.tracks')}
      placement="top-end"
      className={[styles.trackMenu, audio && audio.length > 0 ? styles.trackMenuTwo : ''].join(' ')}
      onOpenChange={onOpenChange}
      trigger={(p) => (
        <button {...p} type="button" className={styles.tool} aria-label={t('tv.tracks')} title={t('tv.tracks')}>
          <Icon name="captions" size={24} />
        </button>
      )}
    >
      {(close) =>
        unknown ? (
          <MenuItem label={t('tv.tracksUnknown')} disabled />
        ) : (
          <>
            {audio && audio.length > 0 && (
              <div role="group" aria-label={t('common.audio')} className={styles.trackGroup}>
                <MenuHeading>{t('common.audio')}</MenuHeading>
                {audio.length === 1 && <MenuItem label={audio[0]?.label ?? ''} checked />}
                {audio.length > 1 &&
                  audio.map((a) => (
                    <MenuItem
                      key={a.slot}
                      label={a.label}
                      checked={a.slot === audioSlot}
                      onSelect={() => {
                        close()
                        onAudio(a.slot)
                      }}
                    />
                  ))}
              </div>
            )}
            <div role="group" aria-label={t('common.subtitles')} className={styles.trackGroup}>
              <MenuHeading>{t('common.subtitles')}</MenuHeading>
              <MenuItem
                label={t('player.tracks.subtitlesOff')}
                checked={subSlot === null}
                onSelect={() => {
                  close()
                  onSubtitles(null)
                }}
              />
              {(subtitles ?? []).map((s) => (
                <MenuItem
                  key={s.slot}
                  label={s.label}
                  checked={s.slot === subSlot}
                  onSelect={() => {
                    close()
                    onSubtitles(s.slot)
                  }}
                />
              ))}
            </div>
          </>
        )
      }
    </Menu>
  )
}
