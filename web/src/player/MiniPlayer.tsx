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
import { useAudio, useAudioNotice } from './audio/useAudio'
import { useT } from '../i18n'

export interface MiniPlayerProps {
  /** Opens «Сейчас играет»; without it the bar is not clickable. */
  onExpand?: () => void
}

export function MiniPlayer({ onExpand }: MiniPlayerProps) {
  const t = useT()
  const s = useAudio()
  const notice = useAudioNotice()
  const it = s.queue[s.index]
  // Always mounted, so screen readers announce a new text (a live region
  // inserted together with its text often is not read).
  const live = (
    <div className="visually-hidden" role="status" aria-live="polite">
      {notice ?? ''}
    </div>
  )
  if (!it) return live
  const book = s.kind === 'audiobook'
  const second = (book && chapterAt(it, s.positionSec)?.chapter.title) || it.artist

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
        progress={s.durationSec > 0 ? s.positionSec / s.durationSec : 0}
      >
        {book ? (
          <PlayerBarButton icon="back15" label={t('player.skipBack', { n: BOOK_BACK_SEC })} onClick={() => audio.skip(-BOOK_BACK_SEC)} />
        ) : (
          <PlayerBarButton icon="previous" label={t('player.previousTrack')} onClick={() => audio.previous()} />
        )}
        <PlayerBarButton
          icon={s.playing ? 'pause' : 'play'}
          label={s.playing ? t('common.pause') : t('player.playAction')}
          busy={s.playing && s.buffering}
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
