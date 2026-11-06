// The skip pills over the video: «Пропустить пересказ / вступление» while
// playback is inside one (8 s on their own, then with the controls),
// «Следующая серия» at the credits (it fills over 10 s and plays the next
// episode, unless a scene follows the credits; «Смотреть титры» dismisses
// it), «Пропустить титры» at the credits of a last episode or a film. With
// «Пропускать сам» a recap or intro is skipped at once, with a short note
// that can take it back. The credits are never skipped by themselves.
import { useEffect, useRef, useState, type MutableRefObject, type ReactNode } from 'react'
import type { PlaySegment } from '../api/types'
import { MenuHeading, MenuItem, Menu } from '../ui/Menu'
import { Icon } from '../components/Icon'
import { useT } from '../i18n'
import { countdown, creditsToEnd, pillVisible, postCreditsScene, segmentAt, SKIPPED_NOTE_MS, PILL_ALONE_MS } from './skipSegments'
import styles from './SkipControls.module.css'

export interface AutoSkip {
  recap: boolean
  intro: boolean
}

export interface SkipControlsProps {
  /** usableSegments of the current part. */
  segments: PlaySegment[]
  /** The current part: a new one starts afresh. */
  part: number
  /** Seconds into the part (the segments' clock). */
  time: number
  /** The part's duration, seconds (0: unknown). */
  durationSec: number
  playing: boolean
  controlsShown: boolean
  /** There is a next episode (the last part of this one plays). */
  hasNext: boolean
  /** «Смотреть титры» was chosen (until the next part or file). */
  creditsDismissed: boolean
  onDismissCredits: () => void
  autoSkip: AutoSkip
  /** Seeks to t seconds into the part. */
  onSeek: (t: number) => void
  /** «Следующая серия». */
  onNext: () => void
  /** The credits run to the end and are skipped: as if the file ended. */
  onEnd: () => void
  /** Set to the action of the pill on screen (Enter/S); null when none shows. */
  actionRef: MutableRefObject<(() => void) | null>
}

export function SkipControls({
  segments,
  part,
  time,
  durationSec,
  playing,
  controlsShown,
  hasNext,
  creditsDismissed,
  onDismissCredits,
  autoSkip,
  onSeek,
  onNext,
  onEnd,
  actionRef,
}: SkipControlsProps) {
  const t = useT()
  const active = segmentAt(segments, time)
  const key = active ? `${part}:${active.kind}:${active.startSec}` : ''
  const [entered, setEntered] = useState<{ key: string; ms: number; at: number } | null>(null)
  const [, rerender] = useState(0)
  const [note, setNote] = useState<{ kind: string; back: number } | null>(null)
  /** Segments skipped by themselves once (or taken back): never again in this file. */
  const autoDone = useRef(new Set<string>())
  const nexted = useRef(false)

  useEffect(
    () => () => {
      actionRef.current = null
    },
    [actionRef],
  )
  useEffect(() => {
    if (!key) setEntered(null)
    else if (entered?.key !== key) setEntered({ key, ms: Date.now(), at: time })
    // only a change of segment counts
  }, [key])
  // the pill hides 8 s after the segment starts: a render at that moment
  useEffect(() => {
    if (!entered) return
    const id = window.setTimeout(() => rerender((n) => n + 1), Math.max(0, entered.ms + PILL_ALONE_MS - Date.now()) + 20)
    return () => window.clearTimeout(id)
  }, [entered])

  // «Пропускать сам»: once per segment, while it plays
  useEffect(() => {
    if (!active || !playing || autoDone.current.has(key)) return
    if ((active.kind === 'recap' && autoSkip.recap) || (active.kind === 'intro' && autoSkip.intro)) {
      autoDone.current.add(key)
      setNote({ kind: active.kind, back: active.startSec })
      onSeek(active.endSec)
    }
  }, [key, playing, autoSkip.recap, autoSkip.intro])
  useEffect(() => {
    if (!note) return
    const id = window.setTimeout(() => setNote(null), SKIPPED_NOTE_MS)
    return () => window.clearTimeout(id)
  }, [note])

  const credits = active?.kind === 'credits' ? active : undefined
  const offerNext = Boolean(credits && hasNext && !creditsDismissed)
  const autoplay = offerNext && credits !== undefined && !postCreditsScene(credits, durationSec)
  const fill = autoplay && entered?.key === key ? countdown(entered.at, time) : 0
  const goNext = () => {
    if (nexted.current) return
    nexted.current = true
    onNext()
  }
  useEffect(() => {
    if (autoplay && fill >= 1) goNext()
  }, [autoplay, fill >= 1])

  const alone = entered?.key === key && pillVisible(entered.ms, Date.now(), controlsShown)
  let action: (() => void) | null = null
  let pill: ReactNode = null
  if (active && (active.kind === 'recap' || active.kind === 'intro') && alone) {
    const label = active.kind === 'recap' ? t('player.skip.recap') : t('player.skip.intro')
    const end = active.endSec
    action = () => onSeek(end)
    pill = (
      <button type="button" className={`${styles.pill} glass`} onClick={action} title={t('player.skip.keyHint', { label })}>
        {label}
        <Icon name="next" size={16} />
      </button>
    )
  } else if (credits && offerNext) {
    const label = t('player.skip.next')
    action = goNext
    pill = (
      <div className={styles.row}>
        <button type="button" className={`${styles.pill} glass`} onClick={onDismissCredits}>
          {t('player.skip.watchCredits')}
        </button>
        <button
          type="button"
          className={`${styles.pill} ${styles.primary} glass-strong`}
          onClick={goNext}
          title={t('player.skip.keyHint', { label })}
          style={{ ['--fill' as string]: `${Math.round(fill * 1000) / 10}%` }}
          data-countdown={autoplay || undefined}
        >
          <span className={styles.fill} aria-hidden />
          <span className={styles.label}>
            <Icon name="play" size={14} />
            {label}
          </span>
        </button>
      </div>
    )
  } else if (credits && !hasNext && alone) {
    const label = t('player.skip.credits')
    const end = credits.endSec
    action = () => (creditsToEnd(credits, durationSec) ? onEnd() : onSeek(end))
    pill = (
      <button type="button" className={`${styles.pill} glass`} onClick={action} title={t('player.skip.keyHint', { label })}>
        {label}
        <Icon name="next" size={16} />
      </button>
    )
  }
  // the note of an automatic skip stays beside the next pill (the pill takes the keys)
  let noteBox: ReactNode = null
  if (note) {
    const undo = () => {
      onSeek(note.back)
      setNote(null)
    }
    action ??= undo
    noteBox = (
      <div className={`${styles.note} glass`} role="status">
        <span>{note.kind === 'recap' ? t('player.skip.recapSkipped') : t('player.skip.introSkipped')}</span>
        <button type="button" className={styles.noteUndo} onClick={undo}>
          {t('player.skip.undo')}
        </button>
      </div>
    )
  }
  actionRef.current = action
  return noteBox || pill ? (
    <div className={styles.box}>
      {noteBox}
      {pill}
    </div>
  ) : null
}

export interface SkipMenuProps {
  /** The kinds this file has (the menu shows for those). */
  kinds: { recap: boolean; intro: boolean }
  value: AutoSkip
  onChange: (v: AutoSkip) => void
  onOpenChange?: (open: boolean) => void
}

/** «Пропускать сам»: пересказ / вступление, per device. */
export function SkipMenu({ kinds, value, onChange, onOpenChange }: SkipMenuProps) {
  const t = useT()
  if (!kinds.recap && !kinds.intro) return null
  const label = t('player.skip.auto')
  return (
    <Menu
      label={label}
      placement="top-end"
      className={styles.menu}
      onOpenChange={onOpenChange}
      trigger={(p) => (
        <button {...p} type="button" className={styles.trigger} aria-label={label} title={label}>
          <Icon name="next" size={22} />
        </button>
      )}
    >
      {() => (
        <div role="group" aria-label={label}>
          <MenuHeading>{label}</MenuHeading>
          <MenuItem label={t('player.skip.autoRecap')} checked={value.recap} onSelect={() => onChange({ ...value, recap: !value.recap })} />
          <MenuItem label={t('player.skip.autoIntro')} checked={value.intro} onSelect={() => onChange({ ...value, intro: !value.intro })} />
        </div>
      )}
    </Menu>
  )
}
