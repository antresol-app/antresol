// iOS-style segmented control: a filled track with a sliding white-on-grey
// thumb. Radio-group semantics; ←/→ move the selection.
// `row`: a page's filter under its title (Поиск). Centred in its container;
// when the segments do not fit (phones) the whole control scrolls sideways,
// out to the window's edges (--segmented-bleed, the page's gutter), and the
// edge with more segments past it fades.
import { useEffect, useLayoutEffect, useRef, useState, type KeyboardEvent, type ReactNode, type RefObject } from 'react'
import { useSlidingThumb } from './slidingThumb'
import styles from './SegmentedControl.module.css'

export interface Segment<T extends string> {
  value: T
  label: string
  /** Shorter label for phones. */
  short?: string
  /** A glyph in place of the text (Files' view switch); `label` is its name and tooltip. */
  icon?: ReactNode
}

export interface SegmentedControlProps<T extends string> {
  label: string
  segments: Segment<T>[]
  value: T
  onChange: (v: T) => void
  /** A centred filter row that scrolls with edge fades when it does not fit. */
  row?: boolean
  /** small: the small control height (a toolbar's view switch beside 36 pt buttons). */
  size?: 'regular' | 'small'
  /** Show every segment's `short` label at every width (a narrow place: a sheet); the full label is its name and tooltip. */
  shortLabels?: boolean
}

/** Which edges of a sideways scroller have content past them. */
function useEdgeFades(ref: RefObject<HTMLDivElement | null>, on: boolean) {
  const [fade, setFade] = useState({ start: false, end: false })
  const measure = () => {
    const el = ref.current
    if (!el) return
    const over = el.scrollWidth - el.clientWidth
    // a pixel of rounding is not «more»
    const next = { start: over > 1 && el.scrollLeft > 1, end: over > 1 && el.scrollLeft < over - 1 }
    setFade((f) => (f.start === next.start && f.end === next.end ? f : next))
  }
  useLayoutEffect(() => {
    const el = ref.current
    if (!on || !el) return
    measure()
    if (typeof ResizeObserver === 'undefined') return
    const ro = new ResizeObserver(measure)
    ro.observe(el)
    if (el.firstElementChild) ro.observe(el.firstElementChild)
    return () => ro.disconnect()
  }, [on])
  return { fade, measure }
}

export function SegmentedControl<T extends string>({ label, segments, value, onChange, row = false, size = 'regular', shortLabels = false }: SegmentedControlProps<T>) {
  const refs = useRef<(HTMLButtonElement | null)[]>([])
  const trackRef = useRef<HTMLDivElement>(null)
  const rowRef = useRef<HTMLDivElement>(null)
  const { fade, measure } = useEdgeFades(rowRef, row)
  const i = Math.max(
    0,
    segments.findIndex((s) => s.value === value),
  )

  // A track that scrolls sideways (phones) keeps the chosen segment in view,
  // also when it comes from the URL; the page itself never scrolls.
  useEffect(() => {
    const el = refs.current[i]
    const scroller = row ? rowRef.current : el?.parentElement
    if (!el || !scroller || scroller.scrollWidth <= scroller.clientWidth) return
    // the track's border and padding (4px) stay clear of the segment; in a
    // row, the fade too
    const pad = row ? scroller.clientWidth / 6 : 4
    const t = scroller.getBoundingClientRect()
    const r = el.getBoundingClientRect()
    if (r.left < t.left + pad) scroller.scrollLeft -= t.left + pad - r.left
    else if (r.right > t.right - pad) scroller.scrollLeft += r.right - t.right + pad
  }, [i, row])

  const thumb = useSlidingThumb(trackRef, '[aria-checked="true"]', `${i}:${segments.length}`)

  const onKey = (e: KeyboardEvent) => {
    const d = e.key === 'ArrowRight' ? 1 : e.key === 'ArrowLeft' ? -1 : 0
    if (!d) return
    e.preventDefault()
    // the arrows belong to the control, not to a list around it
    e.stopPropagation()
    const n = (i + d + segments.length) % segments.length
    onChange(segments[n]!.value)
    refs.current[n]?.focus()
  }

  const control = (
    <div ref={trackRef} className={[styles.track, row ? styles.inRow : '', size === 'small' ? styles.small : ''].filter(Boolean).join(' ')} role="radiogroup" aria-label={label} onKeyDown={onKey}>
      {thumb && <span className={styles.thumb} style={thumb.style} data-slide={thumb.slide || undefined} aria-hidden />}
      {segments.map((s, k) => {
        const on = k === i
        return (
          <button
            key={s.value}
            ref={(el) => {
              refs.current[k] = el
            }}
            type="button"
            role="radio"
            aria-checked={on}
            tabIndex={on ? 0 : -1}
            className={s.icon ? `${styles.segment} ${styles.iconSegment}` : styles.segment}
            aria-label={s.icon || (shortLabels && s.short) ? s.label : undefined}
            title={s.icon || (shortLabels && s.short) ? s.label : undefined}
            onClick={() => onChange(s.value)}
          >
            {s.icon ? (
              <span className={styles.label}>{s.icon}</span>
            ) : shortLabels && s.short ? (
              <span className={styles.label} aria-hidden>
                {s.short}
              </span>
            ) : s.short ? (
              <>
                <span className={`${styles.label} ${styles.long}`}>{s.label}</span>
                <span className={`${styles.label} ${styles.short}`} aria-hidden>
                  {s.short}
                </span>
              </>
            ) : (
              <span className={styles.label}>{s.label}</span>
            )}
          </button>
        )
      })}
    </div>
  )
  if (!row) return control
  return (
    <div
      ref={rowRef}
      className={styles.row}
      data-fade-start={fade.start || undefined}
      data-fade-end={fade.end || undefined}
      onScroll={measure}
    >
      {control}
    </div>
  )
}
