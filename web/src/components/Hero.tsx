// Full-bleed featured carousel at the top of Главная (Apple TV style).
// Advances every 8 s; the active page dot fills up as a timer. Pauses on hover,
// keyboard focus and when the tab is hidden; never auto-advances with
// reduced motion (WCAG 2.2.2). Slides crossfade.
import { useCallback, useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router'
import { AnimatePresence, motion } from 'motion/react'
import { imageSrcSet, imageUrl } from '../api/client'
import type { Card } from '../api/types'
import { useReducedMotion } from '../state/useMediaQuery'
import { useT, type Key } from '../i18n'
import { Button } from '../ui/Button'
import { useTrimmedLogo } from './useTrimmedImage'
import styles from './Hero.module.css'

export const HERO_INTERVAL_MS = 8000

const KIND_LABEL: Record<string, Key> = {
  movie: 'common.kind.movie',
  series: 'common.kind.series',
  season: 'common.kind.season',
  episode: 'common.kind.episode',
}

export interface HeroProps {
  items: Card[]
  /** Small caps line above the title. */
  eyebrow?: string
  onPlay?: (c: Card) => void
}

function metaLine(c: Card) {
  return [c.year ? String(c.year) : '', c.subtitle].filter(Boolean).join(' · ')
}

export function Hero({ items, eyebrow, onPlay }: HeroProps) {
  const t = useT()
  const navigate = useNavigate()
  const reduce = useReducedMotion()
  const [index, setIndex] = useState(0)
  const [hover, setHover] = useState(false)
  const [focusWithin, setFocusWithin] = useState(false)
  const [hidden, setHidden] = useState(() => typeof document !== 'undefined' && document.hidden)
  const [logoFailed, setLogoFailed] = useState<Record<number, boolean>>({})
  // ← / → replace the slide and its buttons: the same button of the new
  // slide takes the keyboard focus back (it would fall to <body>).
  const refocus = useRef<string | null>(null)
  const focusTarget = (action: string) => (el: HTMLButtonElement | null) => {
    if (el && refocus.current === action) {
      refocus.current = null
      el.focus({ preventScroll: true })
    }
  }

  const count = items.length
  const current = items[Math.min(index, count - 1)]
  const paused = hover || focusWithin || hidden || reduce || count < 2

  useEffect(() => {
    const on = () => setHidden(document.hidden)
    document.addEventListener('visibilitychange', on)
    return () => document.removeEventListener('visibilitychange', on)
  }, [])

  useEffect(() => {
    if (index >= count) setIndex(0)
  }, [count, index])

  const next = useCallback(() => setIndex((i) => (i + 1) % Math.max(count, 1)), [count])

  // Preload the next backdrop so the crossfade never shows a blank frame.
  useEffect(() => {
    const n = items[(index + 1) % Math.max(count, 1)]
    const src = imageUrl(n?.backdrop, 1280)
    if (src) {
      // the same candidate the <img> will take
      const img = new Image()
      img.sizes = '100vw'
      img.srcset = imageSrcSet(n?.backdrop, 640, 1920) ?? ''
      img.src = src
    }
  }, [index, items, count])

  const logoSrc = current && !logoFailed[current.id] ? imageUrl(current.logo) : undefined
  const { url: trimmedLogo, dark: darkLogo } = useTrimmedLogo(logoSrc)

  if (!current) return null

  const backdropRef = current.backdrop || current.poster
  const backdrop = imageUrl(backdropRef, 1280)
  const kindKey = KIND_LABEL[current.kind]
  const kind = kindKey ? t(kindKey) : ''
  const meta = metaLine(current)
  const play = () => (onPlay ? onPlay(current) : navigate(`/item/${current.id}?play=1`))

  return (
    <section
      className={styles.hero}
      aria-roledescription={t('comp.hero.carousel')}
      aria-label={t('comp.hero.featured')}
      onPointerEnter={(e) => e.pointerType === 'mouse' && setHover(true)}
      onPointerLeave={() => setHover(false)}
      onFocus={() => setFocusWithin(true)}
      onBlur={(e) => {
        if (!e.currentTarget.contains(e.relatedTarget as Node)) setFocusWithin(false)
      }}
      onKeyDown={(e) => {
        if (count < 2 || (e.key !== 'ArrowRight' && e.key !== 'ArrowLeft')) return
        refocus.current = (document.activeElement as HTMLElement | null)?.dataset?.heroAction ?? null
        if (e.key === 'ArrowRight') setIndex((i) => (i + 1) % count)
        if (e.key === 'ArrowLeft') setIndex((i) => (i - 1 + count) % count)
      }}
    >
      <AnimatePresence initial={false} mode="sync">
        <motion.div
          key={current.id}
          className={styles.backdrop}
          initial={{ opacity: 0, scale: reduce ? 1 : 1.03 }}
          animate={{ opacity: 1, scale: 1 }}
          exit={{ opacity: 0 }}
          transition={{ duration: reduce ? 0 : 1.1, ease: [0.22, 1, 0.36, 1] }}
        >
          {backdrop && <img src={backdrop} srcSet={imageSrcSet(backdropRef, 640, 1920)} sizes="100vw" alt="" decoding="async" fetchPriority="high" draggable={false} />}
        </motion.div>
      </AnimatePresence>
      <div className={styles.scrim} aria-hidden />

      <div className={styles.content} aria-live={paused ? 'polite' : 'off'}>
        <AnimatePresence mode="wait" initial={false}>
          <motion.div
            key={current.id}
            className={styles.copy}
            initial={{ opacity: 0, y: reduce ? 0 : 12 }}
            animate={{ opacity: 1, y: 0 }}
            exit={{ opacity: 0, y: reduce ? 0 : -6 }}
            transition={{ duration: reduce ? 0 : 0.45, ease: [0.22, 1, 0.36, 1] }}
            role="group"
            aria-roledescription={t('comp.hero.slide')}
            aria-label={t('comp.hero.slideLabel', { n: index + 1, count, title: current.title })}
          >
            <p className={styles.eyebrow}>
              {eyebrow ?? t('comp.hero.eyebrow')}
              {kind ? ` · ${kind}` : ''}
            </p>
            {logoSrc ? (
              <h2 className={styles.logoTitle}>
                <img
                  src={trimmedLogo ?? logoSrc}
                  data-ready={trimmedLogo ? true : undefined}
                  data-dark={darkLogo || undefined}
                  alt={current.title}
                  className={styles.logo}
                  onError={() => setLogoFailed((m) => ({ ...m, [current.id]: true }))}
                />
              </h2>
            ) : (
              <h2 className={styles.title}>{current.title}</h2>
            )}
            {meta && <p className={styles.meta}>{meta}</p>}
            <div className={styles.actions}>
              <Button size="large" icon="play" onClick={play} data-hero-action="play" ref={focusTarget('play')}>
                {current.progress > 0 ? t('common.resume') : t('common.watch')}
              </Button>
              <Button size="large" variant="secondary" icon="info" onClick={() => navigate(`/item/${current.id}`)} data-hero-action="info" ref={focusTarget('info')}>
                {t('comp.hero.details')}
              </Button>
            </div>
          </motion.div>
        </AnimatePresence>
      </div>

      {count > 1 && (
        <div className={styles.dots} role="group" aria-label={t('comp.hero.pickSlide')}>
          {items.map((c, i) => (
            <button
              key={c.id}
              type="button"
              className={[styles.dot, i === index ? styles.dotActive : ''].join(' ')}
              aria-label={`${i + 1}: ${c.title}`}
              aria-current={i === index ? 'true' : undefined}
              onClick={() => setIndex(i)}
            >
              {/* The button is a 44pt-tall target; the pip is what you see. */}
              <span className={styles.pip}>
                {i === index && (
                  <span
                    key={`${index}-${c.id}`}
                    className={styles.dotFill}
                    style={{ animationDuration: `${HERO_INTERVAL_MS}ms`, animationPlayState: paused ? 'paused' : 'running' }}
                    onAnimationEnd={next}
                    data-paused={paused || undefined}
                  />
                )}
              </span>
            </button>
          ))}
        </div>
      )}
    </section>
  )
}
