// The player's timeline: elapsed time on the left, remaining on the right, a
// thin track that thickens on hover, a hover/drag time bubble (with the
// scrub preview of that time once the server has made them). Seeks only
// when the drag ends, so a transcoded stream restarts once, not on every move.
import { useEffect, useRef, useState, type PointerEvent, type ReactNode } from 'react'
import { clock } from './timeline'
import { preloadSheets, tileAt, tileStyle, type ScrubPreview } from './trickplay'
import styles from './Scrubber.module.css'
import { useT } from '../i18n'

export interface ScrubberProps {
  /** Seconds on the whole timeline. */
  time: number
  duration: number
  /** End of what is already loaded, seconds. */
  buffered: number
  onSeek: (sec: number) => void
  onScrubbing?: (active: boolean) => void
  /** Drawn just above the track, after the slider in the tab order («Семья»: FamilyMarks). */
  marks?: ReactNode
  /** The file's scrub previews (useTrickplay); none: the time alone. */
  preview?: ScrubPreview
}

/** The preview's width on screen, px. */
const PREVIEW_WIDTH = 192

export function Scrubber({ time, duration, buffered, onSeek, onScrubbing, marks, preview }: ScrubberProps) {
  const t = useT()
  const trackRef = useRef<HTMLDivElement>(null)
  const [drag, setDrag] = useState<number | null>(null)
  const [hover, setHover] = useState<number | null>(null)

  const at = (clientX: number) => {
    const r = trackRef.current?.getBoundingClientRect()
    if (!r || r.width === 0 || duration <= 0) return 0
    return Math.min(Math.max((clientX - r.left) / r.width, 0), 1) * duration
  }

  const shown = drag ?? time
  const pct = duration > 0 ? Math.min(shown / duration, 1) * 100 : 0
  const bufPct = duration > 0 ? Math.min(buffered / duration, 1) * 100 : 0
  const bubble = drag ?? hover
  const tile = bubble !== null && preview ? tileAt(preview.index, bubble) : null
  // the sheet of the time playing and the next one load before a scrub needs them
  const playingSheet = preview ? (tileAt(preview.index, time)?.sheet ?? 0) : -1
  useEffect(() => {
    if (preview && playingSheet >= 0) preloadSheets(preview, playingSheet)
  }, [preview, playingSheet])

  const down = (e: PointerEvent<HTMLDivElement>) => {
    if (duration <= 0 || e.button !== 0) return
    e.currentTarget.setPointerCapture(e.pointerId)
    setDrag(at(e.clientX))
    onScrubbing?.(true)
  }
  const move = (e: PointerEvent<HTMLDivElement>) => {
    if (drag !== null) setDrag(at(e.clientX))
    else if (e.pointerType === 'mouse') setHover(at(e.clientX))
  }
  const up = (e: PointerEvent<HTMLDivElement>) => {
    if (drag === null) return
    const t = at(e.clientX)
    setDrag(null)
    onScrubbing?.(false)
    onSeek(t)
  }

  return (
    <div className={styles.root}>
      <div
        ref={trackRef}
        className={[styles.hit, drag !== null ? styles.active : ''].join(' ')}
        role="slider"
        tabIndex={0}
        aria-label={t('player.position')}
        aria-valuemin={0}
        aria-valuemax={Math.round(duration)}
        aria-valuenow={Math.round(shown)}
        aria-valuetext={t('player.positionOf', { time: clock(shown), total: clock(duration) })}
        onPointerDown={down}
        onPointerMove={move}
        onPointerUp={up}
        onPointerCancel={() => {
          setDrag(null)
          onScrubbing?.(false)
        }}
        onPointerLeave={() => setHover(null)}
        onKeyDown={(e) => {
          // ←/→ are the player's ±10 s; Home/End jump to the ends.
          if (e.key === 'Home') onSeek(0)
          else if (e.key === 'End') onSeek(Math.max(0, duration - 1))
          else return
          e.preventDefault()
        }}
      >
        <div className={styles.track}>
          <div className={styles.buffered} style={{ width: `${bufPct}%` }} />
          <div className={styles.played} style={{ width: `${pct}%` }} />
        </div>
        <div className={styles.knob} style={{ left: `${pct}%` }} aria-hidden />
        {bubble !== null && duration > 0 && preview && tile ? (
          // kept inside the timeline at its ends
          <div
            className={`${styles.preview} glass-strong`}
            style={{ left: `clamp(${PREVIEW_WIDTH / 2 + 4}px, ${(bubble / duration) * 100}%, calc(100% - ${PREVIEW_WIDTH / 2 + 4}px))` }}
            aria-hidden
            data-testid="scrub-preview"
          >
            <div className={styles.frame} style={tileStyle(preview.index, tile, preview.sheetUrl(tile.sheet), PREVIEW_WIDTH)} />
            <span className={styles.previewTime}>{clock(bubble)}</span>
          </div>
        ) : (
          bubble !== null &&
          duration > 0 && (
            <div className={`${styles.bubble} glass-strong`} style={{ left: `${(bubble / duration) * 100}%` }} aria-hidden>
              {clock(bubble)}
            </div>
          )
        )}
      </div>
      {marks && <div className={styles.marks}>{marks}</div>}
      <div className={styles.times} aria-hidden>
        <span>{clock(shown)}</span>
        <span>−{clock(Math.max(duration - shown, 0))}</span>
      </div>
    </div>
  )
}
