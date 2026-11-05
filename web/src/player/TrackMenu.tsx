// «Аудио и субтитры»: one glass menu with two columns, like Apple TV.
// Image-based subtitles (PGS/VobSub) are listed but cannot be chosen yet.
// Under the subtitles: «Субтитры раньше / позже» (the menu stays open).
import { useId } from 'react'
import type { SubTrack, Track } from '../api/types'
import { Icon } from '../components/Icon'
import { MenuHeading, MenuItem, MenuSeparator, Menu } from '../ui/Menu'
import { audioLabel, audioMenuOrder, subtitleLabel } from './timeline'
import { offsetSeconds, OFFSET_STEP_MS } from './lrc'
import styles from './TrackMenu.module.css'
import { locale, useT } from '../i18n'

export interface TrackMenuProps {
  audio: Track[]
  audioIndex: number
  subtitles: SubTrack[]
  /** Selected subtitle stream index, or -1 for off. */
  subIndex: number
  onAudio: (index: number) => void
  onSubtitle: (index: number) => void
  onOpenChange?: (open: boolean) => void
  /** Admin: «Найти субтитры…» (OpenSubtitles search for this file). */
  onFindSubtitles?: () => void
  /** Sources build, adults: «Найти озвучку…», the audio column's last row. */
  onFindVoiceOvers?: () => void
  /** «Субтитры раньше / позже» now, ms (positive: later). */
  subOffsetMs?: number
  /** Moves the subtitles (shown while a subtitle track is on); the menu stays open. */
  onSubOffset?: (ms: number) => void
}

export function TrackMenu({ audio, audioIndex, subtitles, subIndex, onAudio, onSubtitle, onOpenChange, onFindSubtitles, onFindVoiceOvers, subOffsetMs = 0, onSubOffset }: TrackMenuProps) {
  const t = useT()
  const loc = locale()
  const offsetId = useId()
  const step = offsetSeconds(OFFSET_STEP_MS, loc).replace(/^\+/, '')
  const findVoices = !__LIBRARY_EDITION__ && onFindVoiceOvers
  // one track and «Найти озвучку…»: the column shows to offer it
  const showAudio = audio.length > 1 || Boolean(findVoices)
  return (
    <Menu
      label={t('player.tracks.title')}
      placement="top-end"
      className={[styles.menu, showAudio ? styles.two : ''].join(' ')}
      onOpenChange={onOpenChange}
      trigger={(p) => (
        <button {...p} type="button" className={styles.trigger} aria-label={t('player.tracks.title')} title={t('player.tracks.title')}>
          <Icon name="captions" size={24} />
        </button>
      )}
    >
      {(close) => (
        <>
          {showAudio && (
            <div role="group" aria-label={t('common.audio')} className={styles.group}>
              <MenuHeading>{t('common.audio')}</MenuHeading>
              {audioMenuOrder(audio).map(({ tr, i }) => {
                const l = audioLabel(tr, i)
                return (
                  <MenuItem
                    key={tr.index}
                    label={l.title}
                    detail={l.detail}
                    badge={l.cleaned && { text: l.cleaned.badge, title: l.cleaned.hint }}
                    ariaLabel={l.cleaned?.aria}
                    checked={tr.index === audioIndex}
                    onSelect={() => {
                      close()
                      if (tr.index !== audioIndex) onAudio(tr.index)
                    }}
                  />
                )
              })}
              {findVoices && (
                <>
                  <MenuSeparator />
                  <MenuItem
                    icon={<Icon name="search" size={16} />}
                    label={t('vo.find')}
                    onSelect={() => {
                      close()
                      findVoices()
                    }}
                  />
                </>
              )}
            </div>
          )}
          <div role="group" aria-label={t('common.subtitles')} className={styles.group}>
            <MenuHeading>{t('common.subtitles')}</MenuHeading>
            <MenuItem
              label={t('player.tracks.subtitlesOff')}
              checked={subIndex < 0}
              onSelect={() => {
                close()
                onSubtitle(-1)
              }}
            />
            {subtitles.map((s, i) => {
              const l = subtitleLabel(s, i)
              return (
                <MenuItem
                  key={s.index}
                  label={l.title}
                  detail={l.detail}
                  checked={s.index === subIndex}
                  disabled={s.burnIn || !s.url}
                  onSelect={() => {
                    close()
                    onSubtitle(s.index)
                  }}
                />
              )
            })}
            {onSubOffset && subIndex >= 0 && (
              <div role="group" aria-labelledby={offsetId} className={styles.offset}>
                <MenuSeparator />
                <MenuHeading>
                  <span id={offsetId}>{t('player.tracks.offset')}</span>
                </MenuHeading>
                <p className={styles.offsetValue} aria-live="polite">
                  {subOffsetMs !== 0 ? t('player.tracks.offsetValue', { value: offsetSeconds(subOffsetMs, loc) }) : t('player.tracks.offsetNone')}
                </p>
                <div className={styles.offsetRow}>
                  <MenuItem
                    icon={<Icon name="chevronLeft" size={16} />}
                    label={t('player.tracks.earlier')}
                    ariaLabel={t('player.tracks.earlierAria', { n: step })}
                    onSelect={() => onSubOffset(subOffsetMs - OFFSET_STEP_MS)}
                  />
                  <MenuItem
                    icon={<Icon name="chevronRight" size={16} />}
                    label={t('player.tracks.later')}
                    ariaLabel={t('player.tracks.laterAria', { n: step })}
                    onSelect={() => onSubOffset(subOffsetMs + OFFSET_STEP_MS)}
                  />
                </div>
                {/* kept at 0 (disabled), so focus never falls out of the menu after a reset */}
                <MenuItem icon={<Icon name="restart" size={16} />} label={t('player.tracks.offsetReset')} disabled={subOffsetMs === 0} onSelect={() => onSubOffset(0)} />
              </div>
            )}
            {onFindSubtitles && (
              <MenuItem
                icon={<Icon name="search" size={16} />}
                label={t('player.tracks.findSubtitles')}
                onSelect={() => {
                  close(false)
                  onFindSubtitles()
                }}
              />
            )}
          </div>
        </>
      )}
    </Menu>
  )
}
