// «Сейчас играет»: the full player over everything, opened from the mini
// player. A blurred, darkened copy of the cover fills the background; the
// big cover shrinks while paused, as in Apple Music. Desktop: cover and
// controls on the left, lyrics or the queue on the right; phone: one column
// where lyrics or the queue take the cover's place. Closes with «Свернуть»,
// Esc, or a swipe down on a phone. An audiobook shows its chapter instead
// of the artist, «−15 с» / «+30 с» around «Играть» with chapter jumps
// outside them, and the speed and the sleep timer at the bottom.
import { useEffect, useRef, useState, type CSSProperties, type KeyboardEvent as ReactKeyboardEvent, type PointerEvent } from 'react'
import { createPortal } from 'react-dom'
import { Link } from 'react-router'
import { usePresence } from '../ui/presence'
import { imageUrl } from '../api/client'
import { Icon } from '../components/Icon'
import { FamilyMarks, ReturnPill } from '../components/FamilyMarks'
import { useFamily } from '../components/FamilyProgress'
import { useSession } from '../state/session'
import { useIsCompact, useReducedMotion } from '../state/useMediaQuery'
import { bookPositionMs, chapterAt, seekAbsolute } from './audio/audiobook'
import { audio, BOOK_BACK_SEC, BOOK_FORWARD_SEC, engineMode, type QueueItem, type RepeatMode } from './audio/engine'
import { useAudio } from './audio/useAudio'
import { AudiobookBookmarks } from './AudiobookBookmarks'
import { LyricsView } from './LyricsView'
import { QueueList } from './QueueList'
import { hasPlace, useReturnPoint, type FamilyPlace } from './familyMarks'
import { Scrubber } from './Scrubber'
import { clock, locate } from './timeline'
import { NormalizeMenu } from './NormalizeMenu'
import { SleepMenu } from './SleepMenu'
import { SpeedMenu } from './SpeedMenu'
import styles from './NowPlaying.module.css'
import { useT, type Key } from '../i18n'

export interface NowPlayingProps {
  open: boolean
  onClose: () => void
}

type Panel = 'lyrics' | 'queue' | null

export const REPEAT_LABEL: Record<RepeatMode, Key> = {
  off: 'player.repeat.off',
  all: 'player.repeat.all',
  one: 'player.repeat.one',
}

type AirPlayElement = HTMLAudioElement & { webkitShowPlaybackTargetPicker?: () => void }
type AvailabilityEvent = Event & { availability?: 'available' | 'not-available' }

/**
 * Safari: true while an AirPlay target is around (the element's
 * `webkitplaybacktargetavailabilitychanged`). Elsewhere always false.
 */
function useAirPlay(trackKey: string): boolean {
  const [available, setAvailable] = useState(false)
  useEffect(() => {
    if (!('WebKitPlaybackTargetAvailabilityEvent' in window)) return
    const el = audio.element()
    if (!el) return
    const on = (e: Event) => setAvailable((e as AvailabilityEvent).availability === 'available')
    el.addEventListener('webkitplaybacktargetavailabilitychanged', on)
    return () => el.removeEventListener('webkitplaybacktargetavailabilitychanged', on)
  }, [trackKey]) // the dual engine swaps elements between tracks
  return available
}

/** Seconds of the current track already loaded (direct streams only). */
function bufferedSec(): number {
  const el = audio.element()
  if (!el || !Number.isFinite(el.duration)) return 0
  const b = el.buffered
  for (let i = 0; i < b.length; i++) if (b.start(i) <= el.currentTime + 0.5 && b.end(i) >= el.currentTime) return b.end(i)
  return 0
}

/** How long it takes to slide away; a drag this far (pt) or a flick this fast (pt per ms) closes it. */
const CLOSE_MS = 240
const DISMISS_DRAG = 120
const DISMISS_FLICK = 0.7

export function NowPlaying({ open, onClose }: NowPlayingProps) {
  const s = useAudio()
  const it = s.queue[s.index]
  // The queue ended (stop, last track removed): nothing to show.
  useEffect(() => {
    if (open && !it) onClose()
  }, [open, it, onClose])
  // it slides down when closed: the last item stays on it meanwhile
  const last = useRef(it)
  if (it) last.current = it
  const { mounted, leaving } = usePresence(open && !!it, CLOSE_MS)
  const shown = it ?? last.current
  return createPortal(mounted && shown ? <Player key="now-playing" item={shown} onClose={onClose} leaving={leaving} /> : null, document.body)
}

function Cover({ src, className, book }: { src: string; className?: string; book?: boolean }) {
  const url = imageUrl(src, 960)
  const [failed, setFailed] = useState('')
  return (
    <span className={`${styles.cover} ${className ?? ''}`}>
      {url && failed !== url ? (
        <img src={url} alt="" draggable={false} onError={() => setFailed(url)} />
      ) : (
        <span className={styles.coverFallback}>
          <Icon name={book ? 'headphones' : 'music'} size={48} strokeWidth={1.3} />
        </span>
      )}
    </span>
  )
}

function Player({ item: it, onClose, leaving }: { item: QueueItem; onClose: () => void; leaving: boolean }) {
  const t = useT()
  const s = useAudio()
  const compact = useIsCompact()
  const reduce = useReducedMotion()
  const [panelChoice, setPanel] = useState<Panel>(null)
  const book = s.kind === 'audiobook'
  const panel: Panel = book ? null : panelChoice // a book has no lyrics or queue
  const ch = book ? chapterAt(it, s.positionSec) : null
  const rootRef = useRef<HTMLDivElement>(null)
  const closeRef = useRef<HTMLButtonElement>(null)
  // Phone: dragged down by the top bar or the cover (NowPlaying.module.css follows it)
  const [dragY, setDragY] = useState(0)
  const [dragging, setDragging] = useState(false)
  const bg = imageUrl(it.cover, 480)
  const volume = engineMode() === 'dual' // iOS ignores the element volume
  const swap = compact && panel !== null
  const airPlay = useAirPlay(it.key)

  // Focus moves in, the page under it stops scrolling; Esc closes.
  useEffect(() => {
    const restore = document.activeElement as HTMLElement | null
    closeRef.current?.focus()
    const overflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && !e.defaultPrevented) {
        e.preventDefault()
        onClose()
      }
    }
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('keydown', onKey)
      document.body.style.overflow = overflow
      restore?.focus?.()
    }
  }, [onClose])

  const onKeyDown = (e: ReactKeyboardEvent) => {
    const t = e.target as HTMLElement
    if (e.key === ' ' && !t.closest('button, a, input, [role="slider"]')) {
      e.preventDefault()
      audio.toggle()
    } else if (e.key === 'Tab') {
      // keep focus inside the dialog
      const els = Array.from(rootRef.current?.querySelectorAll<HTMLElement>('a[href], button:not(:disabled), input, [tabindex="0"]') ?? [])
      if (!els.length) return
      const i = els.indexOf(document.activeElement as HTMLElement)
      if (e.shiftKey && i <= 0) {
        e.preventDefault()
        els[els.length - 1]!.focus()
      } else if (!e.shiftKey && i === els.length - 1) {
        e.preventDefault()
        els[0]!.focus()
      }
    }
  }

  // Phone: a swipe down from the top bar or the cover closes it.
  // the window listeners of a drag under way (removed when it ends or the player unmounts)
  const endDrag = useRef<(() => void) | null>(null)
  useEffect(() => () => endDrag.current?.(), [])
  const startDrag = (e: PointerEvent) => {
    if (!compact || reduce || e.button !== 0) return
    endDrag.current?.()
    const d = { y0: e.clientY, t: e.timeStamp, vy: 0, dy: 0 }
    const move = (ev: globalThis.PointerEvent) => {
      const dy = Math.max(0, ev.clientY - d.y0)
      const dt = ev.timeStamp - d.t
      if (dt > 0) d.vy = (dy - d.dy) / dt
      d.dy = dy
      d.t = ev.timeStamp
      if (dy > 0) setDragging(true)
      setDragY(dy)
    }
    const detach = () => {
      window.removeEventListener('pointermove', move)
      window.removeEventListener('pointerup', up)
      window.removeEventListener('pointercancel', up)
      endDrag.current = null
    }
    const up = () => {
      detach()
      setDragging(false)
      setDragY(0)
      // far enough, or a flick: it slides away from where it is
      if (d.dy > DISMISS_DRAG || d.vy > DISMISS_FLICK) onClose()
    }
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', up)
    window.addEventListener('pointercancel', up)
    endDrag.current = detach
  }

  const { user } = useSession()
  const toggle = (p: Exclude<Panel, null>) => setPanel((cur) => (cur === p ? null : p))
  const panelView =
    panel === 'lyrics' ? (
      <LyricsView
        trackId={it.trackId}
        hasLyrics={it.hasLyrics}
        positionSec={s.positionSec}
        onSeek={(t) => audio.seek(t)}
        canAdjust={!!user && !user.isKid}
      />
    ) : panel === 'queue' ? (
      <QueueList hideNow={swap} />
    ) : null

  const chapters = it.chapters ?? []
  /** «Предыдущая глава»: the start of this one, or the one before within 3 s of it. */
  const previousChapter = () => {
    if (!ch) return audio.previous()
    const into = ch.absMs - ch.chapter.startMs
    seekAbsolute(into > 3000 || ch.index === 0 ? ch.chapter.startMs : chapters[ch.index - 1]!.startMs)
  }
  const nextChapter = () => {
    if (!ch) return audio.next()
    const n = chapters[ch.index + 1]
    if (n) seekAbsolute(n.startMs)
  }
  // «Семья»: the other profiles' places in this file of the book (fetched when the player opens)
  const bookId = book ? (s.bookId ?? 0) : 0
  const family = useFamily(bookId, book && !user?.isKid)
  const startMs = it.bookStartMs ?? 0
  const fileMs = s.durationSec * 1000
  const places: FamilyPlace[] = family
    .filter((m) => hasPlace(m, bookId) && m.positionMs >= startMs && m.positionMs < startMs + fileMs)
    .map((m) => ({ member: m, at: (m.positionMs - startMs) / 1000, label: clock((m.positionMs - startMs) / 1000) }))
  const ret = useReturnPoint<number>()
  /** «12:40» of a book position, in its own file (what the timeline shows). */
  const fileClock = (ms: number) => clock(locate(s.queue.map((q) => ({ durationMs: q.durationSec * 1000 })), ms / 1000).offset)
  const jumpToPlace = (pl: FamilyPlace) => {
    // a second jump keeps the way back to where this profile was before
    // the first, even once the pill is gone and the place is still held
    const mine = ret.back?.to ?? audio.bookHold()?.pos ?? bookPositionMs()
    const to = pl.member.positionMs
    audio.holdBook({ pos: mine, from: to })
    ret.offer(mine, fileClock(mine))
    seekAbsolute(to)
  }
  const returnBack = () => {
    if (!ret.back) return
    audio.holdBook(null)
    seekAbsolute(ret.back.to)
    ret.clear()
  }

  const back = book ? BOOK_BACK_SEC : 10
  const forward = book ? BOOK_FORWARD_SEC : 10

  const artist = book ? (
    <span className={styles.artist}>{ch?.chapter.title ?? it.artist}</span>
  ) : it.artistId && it.artistId > 0 ? (
    <Link to={`/music/artist/${it.artistId}`} className={styles.artist} onClick={onClose}>
      {it.artist}
    </Link>
  ) : (
    <span className={styles.artist}>{it.artist}</span>
  )

  return (
    <div
      ref={rootRef}
      role="dialog"
      aria-modal="true"
      aria-label={t('player.nowPlaying')}
      className={styles.root}
      onKeyDown={onKeyDown}
      data-leaving={leaving || undefined}
      // sliding away: nothing in it takes a click, focus or a screen reader any more
      inert={leaving || undefined}
      data-dragging={dragging || undefined}
      style={dragY && !leaving ? { transform: `translateY(${dragY}px)` } : undefined}
    >
      <div className={styles.bg} aria-hidden>
        {bg && <img src={bg} alt="" draggable={false} />}
      </div>
      <div className={styles.shade} aria-hidden />

      <div className={styles.top} onPointerDown={startDrag}>
        <button ref={closeRef} type="button" className={styles.close} aria-label={t('common.collapse')} onClick={onClose}>
          <Icon name="chevronDown" size={24} />
        </button>
        {compact && <span className={styles.grabber} aria-hidden />}
      </div>

      <div className={`${styles.body} ${panel && !compact ? styles.split : ''}`}>
        <div className={`${styles.deck} ${swap ? styles.deckSwap : ''}`}>
          {!swap && (
            <div className={styles.artBox} onPointerDown={startDrag}>
              <div className={styles.art} data-paused={!s.playing || undefined}>
                <Cover src={it.cover} book={book} />
              </div>
            </div>
          )}

          <div className={styles.meta}>
            {swap && <Cover src={it.cover} className={styles.small} book={book} />}
            <div className={styles.metaText}>
              <h2 className={styles.title}>{it.title}</h2>
              {artist}
            </div>
            {it.lossless && <span className={styles.badge}>Lossless</span>}
          </div>

          {swap && <div className={styles.panelSwap}>{panelView}</div>}

          <div
            className={styles.scrub}
            onKeyDown={(e) => {
              if (e.key === 'ArrowLeft' || e.key === 'ArrowRight') {
                e.preventDefault()
                ret.clear()
                audio.skip(e.key === 'ArrowLeft' ? -back : forward)
              }
            }}
          >
            <Scrubber
              time={s.positionSec}
              duration={s.durationSec}
              buffered={bufferedSec()}
              onSeek={(t) => {
                ret.clear()
                audio.seek(t)
              }}
              marks={places.length > 0 && <FamilyMarks places={places} total={s.durationSec} onJump={jumpToPlace} />}
            />
            {ret.back && <ReturnPill className={styles.returnPill} label={ret.back.label} onReturn={returnBack} onDismiss={ret.clear} />}
          </div>

          {book ? (
            <div className={styles.controls}>
              <button type="button" className={styles.mode} aria-label={t('player.previousChapter')} onClick={previousChapter}>
                <Icon name="previous" size={22} />
              </button>
              <button type="button" className={styles.skip} aria-label={t('player.skipBack', { n: BOOK_BACK_SEC })} onClick={() => (ret.clear(), audio.skip(-BOOK_BACK_SEC))}>
                <Icon name="back15" size={38} strokeWidth={1.7} />
              </button>
              <button
                type="button"
                className={styles.play}
                aria-label={s.playing ? t('common.pause') : t('player.playAction')}
                aria-busy={s.playing && s.buffering ? true : undefined}
                onClick={() => audio.toggle()}
              >
                <Icon name={s.playing ? 'pause' : 'play'} size={44} />
              </button>
              <button type="button" className={styles.skip} aria-label={t('player.skipForward', { n: BOOK_FORWARD_SEC })} onClick={() => (ret.clear(), audio.skip(BOOK_FORWARD_SEC))}>
                <Icon name="forward30" size={38} strokeWidth={1.7} />
              </button>
              <button
                type="button"
                className={styles.mode}
                aria-label={t('player.nextChapter')}
                disabled={!!ch && ch.index >= chapters.length - 1}
                onClick={nextChapter}
              >
                <Icon name="next" size={22} />
              </button>
            </div>
          ) : (
            <div className={styles.controls}>
              <button
                type="button"
                className={`${styles.mode} ${s.shuffle ? styles.on : ''}`}
                aria-label={t('player.shuffle')}
                aria-pressed={s.shuffle}
                onClick={() => audio.setShuffle(!s.shuffle)}
              >
                <Icon name="shuffle" size={20} />
              </button>
              <button type="button" className={styles.skip} aria-label={t('player.previousTrack')} onClick={() => audio.previous()}>
                <Icon name="previous" size={34} />
              </button>
              <button
                type="button"
                className={styles.play}
                aria-label={s.playing ? t('common.pause') : t('player.playAction')}
                aria-busy={s.playing && s.buffering ? true : undefined}
                onClick={() => audio.toggle()}
              >
                <Icon name={s.playing ? 'pause' : 'play'} size={44} />
              </button>
              <button type="button" className={styles.skip} aria-label={t('player.nextTrack')} onClick={() => audio.next()}>
                <Icon name="next" size={34} />
              </button>
              <button
                type="button"
                className={`${styles.mode} ${s.repeat !== 'off' ? styles.on : ''}`}
                aria-label={t(REPEAT_LABEL[s.repeat])}
                onClick={() => audio.cycleRepeat()}
              >
                <Icon name={s.repeat === 'one' ? 'repeatOne' : 'repeat'} size={20} />
              </button>
            </div>
          )}

          {volume && !swap && (
            <label className={styles.volume}>
              <Icon name="speakerMute" size={16} />
              <input
                type="range"
                min={0}
                max={1}
                step={0.01}
                value={s.volume}
                aria-label={t('player.volume')}
                style={{ '--fill': `${Math.round(s.volume * 100)}%` } as CSSProperties}
                onChange={(e) => audio.setVolume(Number(e.target.value))}
              />
              <Icon name="speaker" size={18} />
            </label>
          )}

          {book ? (
            <div className={styles.bottom}>
              <SpeedMenu tone="player" placement="top-start" />
              {s.bookId ? <AudiobookBookmarks bookId={s.bookId} className={styles.tool} /> : null}
              {airPlay && (
                <button type="button" className={styles.tool} aria-label="AirPlay" onClick={() => (audio.element() as AirPlayElement | null)?.webkitShowPlaybackTargetPicker?.()}>
                  <Icon name="airplay" size={20} />
                </button>
              )}
              <SleepMenu tone="player" placement="top-end" />
            </div>
          ) : (
            <div className={styles.bottom}>
              <button
                type="button"
                className={`${styles.tool} ${panel === 'lyrics' ? styles.on : ''}`}
                aria-label={t('player.lyrics')}
                aria-pressed={panel === 'lyrics'}
                onClick={() => toggle('lyrics')}
              >
                <Icon name="quote" size={20} />
              </button>
              <NormalizeMenu tone="player" placement="top-start" />
              {airPlay && (
                <button
                  type="button"
                  className={styles.tool}
                  aria-label="AirPlay"
                  onClick={() => (audio.element() as AirPlayElement | null)?.webkitShowPlaybackTargetPicker?.()}
                >
                  <Icon name="airplay" size={20} />
                </button>
              )}
              <button
                type="button"
                className={`${styles.tool} ${panel === 'queue' ? styles.on : ''}`}
                aria-label={t('player.queue')}
                aria-pressed={panel === 'queue'}
                onClick={() => toggle('queue')}
              >
                <Icon name="queue" size={20} />
              </button>
            </div>
          )}
        </div>

        {!compact && panel && <div className={styles.panel}>{panelView}</div>}
      </div>
    </div>
  )
}
