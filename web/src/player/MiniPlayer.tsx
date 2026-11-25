// Persistent mini player: the design system's PlayerBar above the content
// (above the tab bar on phones) while the audio queue is not empty. The
// audio itself lives in the engine (audio/engine.ts), so the bar can unmount
// and remount freely without interrupting the sound. An audiobook shows its
// chapter under the title and «−15 с» / «+30 с» instead of the track buttons.
import { imageUrl } from '../api/client'
import { PlayerBar, PlayerBarButton } from '../ui/PlayerBar'
import { Toast } from '../ui/Toast'
import { chapterAt } from './audio/audiobook'
import { audio, BOOK_BACK_SEC, BOOK_FORWARD_SEC } from './audio/engine'
import { useAudioItem, useAudioNotice, useAudioSelect } from './audio/useAudio'
import { useT } from '../i18n'

export interface MiniPlayerProps {
  /** Opens «Сейчас играет»; without it the bar is not clickable. */
  onExpand?: () => void
}

/** The bar's progress moves in this many steps. */
const PROGRESS_STEPS = 400

export function MiniPlayer({ onExpand }: MiniPlayerProps) {
  const t = useT()
  const notice = useAudioNotice()
  // piece by piece: the bar must not render with every tick of the position, only when what it shows changes
  const it = useAudioItem()
  const book = useAudioSelect((s) => s.kind === 'audiobook')
  const chapter = useAudioSelect((s) => {
    const cur = s.queue[s.index]
    return s.kind === 'audiobook' && cur ? chapterAt(cur, s.positionSec)?.chapter.title : undefined
  })
  const output = useAudioSelect((s) => s.output)
  const playing = useAudioSelect((s) => s.playing)
  const buffering = useAudioSelect((s) => s.buffering)
  // in steps of a quarter of a percent: finer than the bar is wide
  const steps = useAudioSelect((s) => (s.durationSec > 0 ? Math.round((s.positionSec / s.durationSec) * PROGRESS_STEPS) : 0))
  // Always mounted, so screen readers announce a new text (a live region
  // inserted together with its text often is not read).
  const live = (
    <div className="visually-hidden" role="status" aria-live="polite">
      {notice ?? ''}
    </div>
  )
  if (!it) return live
  const heard = chapter || it.artist
  // a remote output plays it: where, before the artist («Living room · M83»)
  const second = output ? `${output} · ${heard}` : heard

  return (
    <>
      {live}
      {/* the words are read by the region above */}
      <Toast live={false} messageKey={notice ?? undefined}>
        {notice}
      </Toast>
      <PlayerBar
        label={t('player.nowPlaying')}
        title={it.title}
        subtitle={second}
        artwork={imageUrl(it.cover, 160)}
        fallbackIcon={book ? 'headphones' : 'music'}
        onOpen={onExpand}
        openLabel={t('player.mini.openAria', { title: it.title, second })}
        progress={steps / PROGRESS_STEPS}
      >
        {book ? (
          <PlayerBarButton icon="back15" label={t('player.skipBack', { n: BOOK_BACK_SEC })} onClick={() => audio.skip(-BOOK_BACK_SEC)} />
        ) : (
          <PlayerBarButton icon="previous" label={t('player.previousTrack')} onClick={() => audio.previous()} />
        )}
        <PlayerBarButton
          icon={playing ? 'pause' : 'play'}
          label={playing ? t('common.pause') : t('player.playAction')}
          busy={playing && buffering}
          prominent
          onClick={() => audio.toggle()}
        />
        {book ? (
          <PlayerBarButton icon="forward30" label={t('player.skipForward', { n: BOOK_FORWARD_SEC })} onClick={() => audio.skip(BOOK_FORWARD_SEC)} />
        ) : (
          <PlayerBarButton icon="next" label={t('player.nextTrack')} onClick={() => audio.next()} />
        )}
      </PlayerBar>
    </>
  )
}
