// Lyrics of the current track. Synced LRC: the active line is large and
// bright, the view follows it (not while the user scrolls by hand), a click
// on a line seeks there. Plain text: paragraphs. None: «Текста нет».
// Synced words that run ahead of or behind the song are corrected under
// them with «Раньше» / «Позже» (0,25 s a press, for this track and every
// profile): applied at once, saved a moment after the last press.
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { ApiError, getLyrics, setLyricsOffset, errorText } from '../api/client'
import { Icon } from '../components/Icon'
import { useReducedMotion } from '../state/useMediaQuery'
import { activeLine, clampOffset, OFFSET_STEP_MS, offsetSeconds, parseLrc, shiftLines, type LrcLine } from './lrc'
import styles from './LyricsView.module.css'
import tool from './SpeedMenu.module.css'
import { locale, useT } from '../i18n'

export interface LyricsViewProps {
  trackId?: number
  /** false: known to have none, no request. */
  hasLyrics?: boolean
  positionSec: number
  onSeek: (sec: number) => void
  /** «Раньше» / «Позже» under synced words (adult profiles, not kids). */
  canAdjust?: boolean
}

type State =
  | { status: 'loading' }
  | { status: 'none' }
  | { status: 'error'; message: string }
  | { status: 'synced'; lines: LrcLine[]; offsetMs: number }
  | { status: 'plain'; paragraphs: string[] }

/** After a manual scroll the view stops following the song this long. */
const MANUAL_MS = 3000
/** The offset is saved this long after the last press. */
export const OFFSET_SAVE_MS = 800

export function LyricsView({ trackId, hasLyrics, positionSec, onSeek, canAdjust = false }: LyricsViewProps) {
  const [state, setState] = useState<State>({ status: 'loading' })
  const t = useT()
  const reduce = useReducedMotion()
  const boxRef = useRef<HTMLDivElement>(null)
  const manualUntil = useRef(0)

  useEffect(() => {
    if (!trackId || hasLyrics === false) {
      setState({ status: 'none' })
      return
    }
    const ac = new AbortController()
    setState({ status: 'loading' })
    getLyrics(trackId, ac.signal)
      .then((l) => {
        const lines = l.synced ? parseLrc(l.lyrics) : null
        if (lines) setState({ status: 'synced', lines, offsetMs: clampOffset(l.offsetMs ?? 0) })
        else {
          const paragraphs = l.lyrics
            .replace(/\r/g, '')
            .split(/\n\s*\n/)
            .map((p) => p.trim())
            .filter(Boolean)
          setState(paragraphs.length ? { status: 'plain', paragraphs } : { status: 'none' })
        }
      })
      .catch((e: unknown) => {
        if (ac.signal.aborted) return
        if (e instanceof ApiError && e.status === 404) setState({ status: 'none' })
        else setState({ status: 'error', message: errorText(e, t('player.lyrics.loadFailed')) })
      })
    return () => ac.abort()
  }, [trackId, hasLyrics])

  const parsed = state.status === 'synced' ? state.lines : null
  const offsetMs = state.status === 'synced' ? state.offsetMs : 0
  const lines = useMemo(() => (parsed ? shiftLines(parsed, offsetMs) : null), [parsed, offsetMs])
  const active = useMemo(() => (lines ? activeLine(lines, positionSec) : -1), [lines, positionSec])
  const setOffset = useCallback((ms: number) => setState((s) => (s.status === 'synced' ? { ...s, offsetMs: ms } : s)), [])

  useEffect(() => {
    if (active < 0 || Date.now() < manualUntil.current) return
    const el = boxRef.current?.querySelector<HTMLElement>(`[data-line="${active}"]`)
    el?.scrollIntoView?.({ block: 'center', behavior: reduce ? 'auto' : 'smooth' })
  }, [active, reduce])

  const manual = () => {
    manualUntil.current = Date.now() + MANUAL_MS
  }

  if (state.status === 'loading') return <div className={styles.box} aria-busy="true" aria-label={t('player.lyrics.loading')} />
  if (state.status === 'none' || state.status === 'error')
    return (
      <div className={`${styles.box} ${styles.empty}`}>
        <p>{state.status === 'none' ? t('player.lyrics.none') : state.message}</p>
      </div>
    )
  if (state.status === 'plain')
    return (
      <div className={`${styles.box} ${styles.plain}`} tabIndex={0} aria-label={t('player.lyrics.aria')}>
        {state.paragraphs.map((p, i) => (
          <p key={i}>{p}</p>
        ))}
      </div>
    )
  return (
    <div className={styles.syncedWrap}>
      <div
        ref={boxRef}
        className={`${styles.box} ${styles.synced}`}
        aria-label={t('player.lyrics.aria')}
        onWheel={manual}
        onTouchMove={manual}
      >
        <ol className={styles.lines}>
          {(lines ?? []).map((l, i) => (
            <li key={i}>
              <button
                type="button"
                data-line={i}
                className={`${styles.line} ${i === active ? styles.active : ''} ${i < active ? styles.past : ''}`}
                aria-current={i === active ? 'true' : undefined}
                onClick={() => {
                  manualUntil.current = 0
                  onSeek(l.timeSec)
                }}
              >
                {l.line || '♪'}
              </button>
            </li>
          ))}
        </ol>
      </div>
      {canAdjust && trackId ? <OffsetControl key={trackId} trackId={trackId} offsetMs={offsetMs} onChange={setOffset} /> : null}
    </div>
  )
}

interface OffsetControlProps {
  trackId: number
  offsetMs: number
  /** Shows a value (stable: a useCallback). */
  onChange: (ms: number) => void
}

/**
 * «Раньше» / «Позже» and the correction now («Текст: +0,50 с», hidden at
 * zero, with a reset). A press shows at once; the value is sent
 * OFFSET_SAVE_MS after the last one, or when the view goes away with a
 * change still waiting. A failed save puts back what the server has.
 */
function OffsetControl({ trackId, offsetMs, onChange }: OffsetControlProps) {
  const t = useT()
  const saved = useRef(offsetMs)
  const pending = useRef<{ ms: number; timer: ReturnType<typeof setTimeout> } | null>(null)
  // false once the view is gone: an answer that comes later (after a skip
  // the parent holds the next track's words) must not touch it
  const alive = useRef(true)

  const send = useCallback(
    (ms: number) => {
      setLyricsOffset(trackId, ms)
        .then((v) => {
          if (!alive.current) return
          saved.current = v
          if (!pending.current && v !== ms) onChange(v)
        })
        .catch(() => {
          if (alive.current && !pending.current) onChange(saved.current)
        })
    },
    [trackId, onChange],
  )

  // a change still waiting goes out when the view closes
  useEffect(() => {
    alive.current = true
    return () => {
      alive.current = false
      const p = pending.current
      if (!p) return
      clearTimeout(p.timer)
      pending.current = null
      if (p.ms !== saved.current) send(p.ms)
    }
  }, [send])

  const move = (ms: number) => {
    const next = clampOffset(ms)
    if (next === offsetMs) return
    onChange(next)
    if (pending.current) clearTimeout(pending.current.timer)
    const timer = setTimeout(() => {
      pending.current = null
      if (next !== saved.current) send(next)
    }, OFFSET_SAVE_MS)
    pending.current = { ms: next, timer }
  }

  const loc = locale()
  const step = offsetSeconds(OFFSET_STEP_MS, loc).replace(/^\+/, '')
  return (
    <div className={styles.offset} role="group" aria-label={t('player.lyrics.offset.group')}>
      <button
        type="button"
        className={`${tool.trigger} ${tool.player}`}
        aria-label={t('player.lyrics.offset.earlierAria', { n: step })}
        onClick={() => move(offsetMs - OFFSET_STEP_MS)}
      >
        <Icon name="chevronLeft" size={16} />
        <span>{t('player.lyrics.offset.earlier')}</span>
      </button>
      <span className={styles.offsetValue} aria-live="polite">
        {offsetMs !== 0 ? t('player.lyrics.offset.value', { value: offsetSeconds(offsetMs, loc) }) : ''}
      </span>
      <button
        type="button"
        className={`${tool.trigger} ${tool.player}`}
        aria-label={t('player.lyrics.offset.laterAria', { n: step })}
        onClick={() => move(offsetMs + OFFSET_STEP_MS)}
      >
        <span>{t('player.lyrics.offset.later')}</span>
        <Icon name="chevronRight" size={16} />
      </button>
      {offsetMs !== 0 && (
        <button
          type="button"
          className={`${tool.trigger} ${tool.player}`}
          aria-label={t('player.lyrics.offset.reset')}
          title={t('player.lyrics.offset.reset')}
          onClick={() => move(0)}
        >
          <Icon name="restart" size={16} />
        </button>
      )}
    </div>
  )
}
