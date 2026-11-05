// «Семья» on a timeline: a face per other profile above the track at its
// place (a thin white ring, a short stem down to the track), its name and
// place on hover or focus («Мама · 34:12»), a click or Enter jumps there.
// The faces are buttons after the slider in the tab order. Nothing is drawn
// when nobody else is somewhere in this item. ReturnPill is «↩ Вернуться к
// 12:40» after such a jump.
import { useLayoutEffect, useRef, useState, type CSSProperties } from 'react'
import { useT } from '../i18n'
import { MARK_SIZE, placeMarks, type FamilyPlace } from '../player/familyMarks'
import { Avatar } from './Avatar'
import { Icon } from './Icon'
import styles from './FamilyMarks.module.css'

export interface FamilyMarksProps {
  places: FamilyPlace[]
  /** The timeline's length, in the places' units. */
  total: number
  onJump: (p: FamilyPlace) => void
  /** A manga's slider runs right to left. */
  dir?: 'ltr' | 'rtl'
  className?: string
  /** Face size (px). */
  size?: number
}

/** The track's width (px), followed on resize (a default before layout, and in tests). */
function useWidth(active: boolean) {
  const ref = useRef<HTMLDivElement>(null)
  const [width, setWidth] = useState(600)
  useLayoutEffect(() => {
    const el = ref.current
    if (!el || !active) return
    const read = () => el.offsetWidth > 0 && setWidth(el.offsetWidth)
    read()
    if (typeof ResizeObserver === 'undefined') return
    const ro = new ResizeObserver(read)
    ro.observe(el)
    return () => ro.disconnect()
  }, [active])
  return [ref, width] as const
}

export function FamilyMarks({ places, total, onJump, dir = 'ltr', className, size = MARK_SIZE }: FamilyMarksProps) {
  const t = useT()
  const [ref, width] = useWidth(places.length > 0 && total > 0)
  const marks = placeMarks(places, total, width, size)
  if (marks.length === 0) return null
  return (
    <div ref={ref} className={[styles.lane, className].filter(Boolean).join(' ')} role="group" aria-label={t('comp.family.title')} data-family-marks>
      {marks.map((m) => {
        const pct = dir === 'rtl' ? 100 - m.pct : m.pct
        const text = `${m.member.name} · ${m.label}`
        const style = { '--pct': `${pct}%`, '--off': `${m.offset}px`, '--size': `${size}px` } as CSSProperties
        return (
          <button
            key={m.member.userId}
            type="button"
            className={styles.mark}
            style={style}
            aria-label={t('comp.family.markAria', { name: m.member.name, place: m.label })}
            data-pct={Math.round(pct * 10) / 10}
            onClick={(e) => {
              e.stopPropagation()
              onJump(m)
            }}
            onPointerDown={(e) => e.stopPropagation()}
          >
            <Avatar name={m.member.name} avatar={m.member.avatar} size={size} className={styles.face} />
            <span className={styles.stem} aria-hidden />
            <span className={`${styles.tip} glass-strong`} aria-hidden>
              {text}
            </span>
          </button>
        )
      })}
    </div>
  )
}

export interface ReturnPillProps {
  /** «12:40», «стр. 34», «42 %». */
  label: string
  onReturn: () => void
  onDismiss: () => void
  className?: string
}

/** «↩ Вернуться к 12:40» and a small ✕. */
export function ReturnPill({ label, onReturn, onDismiss, className }: ReturnPillProps) {
  const t = useT()
  return (
    <div className={[styles.pill, 'glass-strong', className].filter(Boolean).join(' ')} role="group" aria-label={t('comp.family.returnGroup')} data-return-pill>
      <button
        type="button"
        className={styles.pillMain}
        onClick={(e) => {
          e.stopPropagation()
          onReturn()
        }}
      >
        <Icon name="returnArrow" size={16} strokeWidth={2.2} />
        <span>{t('comp.family.returnTo', { place: label })}</span>
      </button>
      <button
        type="button"
        className={styles.pillClose}
        aria-label={t('comp.family.returnDismiss')}
        onClick={(e) => {
          e.stopPropagation()
          onDismiss()
        }}
      >
        <Icon name="close" size={12} strokeWidth={2.6} />
      </button>
    </div>
  )
}
