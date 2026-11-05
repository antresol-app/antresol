// Manga and comics: a volume's pages from /api/comics/{fileId}/page/{n}.
// «Постранично»: the whole page fits the screen (by height on a desktop, by
// width on a phone), one page or a spread of two; keys and taps follow the
// direction (right to left for manga, see comicNav.ts); pinch or a double
// tap zooms. «Лента»: one long vertical ribbon (manhwa, webtoons). The three
// pages after the screen are loaded ahead. The bottom bar has the page
// slider («12 / 180», running right to left for manga) and, on the last
// page, «Следующий том ›». The position is saved as `page:N`.
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type PointerEvent as ReactPointerEvent, type WheelEvent } from 'react'
import { useNavigate } from 'react-router'
import { ApiError, comicPageUrl, getComicPages } from '../api/client'
import type { BookDetail } from '../api/types'
import { Button } from '../ui/Button'
import { Icon } from '../components/Icon'
import { MenuHeading, MenuItem, MenuSeparator, Menu } from '../ui/Menu'
import { useIsCompact } from '../state/useMediaQuery'
import { nextPageKey, tapZone, type ComicDir } from './comicNav'
import { GONE_TEXT, GONE_TITLE } from './comicText'
import { CHROME_ATTR, chromeKey, inChrome, useAutoHide, useFocusInside } from './ReaderChrome'
import { useReaderFamily } from './useReaderFamily'
import { useReadingProgress, type ReadingPosition } from './useReadingProgress'
import { FamilyMarks, ReturnPill } from '../components/FamilyMarks'
import { pageOfMember, type FamilyPlace } from '../player/familyMarks'
import styles from './ComicReader.module.css'
import { useT } from '../i18n'

export interface ComicReaderProps {
  book: BookDetail
  onExit: () => void
}

export type ComicView = 'paged' | 'ribbon'

/** width / height of a page from /pages, or undefined when unknown. */
export const sizeRatio = (s: [number, number] | undefined) => (s && s[0] > 0 && s[1] > 0 ? s[0] / s[1] : undefined)

/** Pages loaded ahead of the screen. */
export const PRELOAD = 3
const MAX_ZOOM = 4
const SWIPE_PX = 50

// --- remembered choices (per series; private mode just forgets) ---

const dirKey = (seriesId: number) => `vault.comic.dir.${seriesId}`
const viewKey = (seriesId: number) => `vault.comic.view.${seriesId}`
const SPREAD_KEY = 'vault.comic.spread'

function read(key: string): string | null {
  try {
    return localStorage.getItem(key)
  } catch {
    return null
  }
}
function write(key: string, value: string) {
  try {
    localStorage.setItem(key, value)
  } catch {
    /* not remembered */
  }
}

/** The direction chosen for the series, else the volume's own (manga: right to left). */
export function initialDir(book: BookDetail): ComicDir {
  const saved = read(dirKey(book.seriesId || book.card.id))
  if (saved === 'rtl' || saved === 'ltr') return saved
  return book.direction === 'ltr' ? 'ltr' : 'rtl'
}

/** `page:N` → N − 1 (0-based), or 0. */
export function pageIndex(locator: string | undefined, count: number): number {
  const m = /^page:(\d+)$/.exec(locator ?? '')
  if (!m || count <= 0) return 0
  return Math.min(Math.max(Number(m[1]) - 1, 0), count - 1)
}

/** The screens of a volume: single pages, or the cover alone and then pairs. */
export function screensOf(count: number, spread: boolean): number[][] {
  const out: number[][] = []
  if (!spread) {
    for (let i = 0; i < count; i++) out.push([i])
    return out
  }
  if (count > 0) out.push([0])
  for (let i = 1; i < count; i += 2) out.push(i + 1 < count ? [i, i + 1] : [i])
  return out
}

/** Keys the ribbon scrolls with itself. */
const RIBBON_KEYS = ['ArrowDown', 'ArrowUp', 'PageDown', 'PageUp', ' ', 'Home', 'End']

type Status = { status: 'loading' } | { status: 'error'; message: string; gone?: boolean } | { status: 'ready'; count: number; sizes: [number, number][] }

/** A double tap: the second tap within this many ms. Single taps on touch wait for it. */
const DOUBLE_TAP_MS = 280
/** Preloads start once the screen has stayed this long (slider scrubbing). */
const PRELOAD_DELAY_MS = 200
type Zoom = { s: number; x: number; y: number }
const NO_ZOOM: Zoom = { s: 1, x: 0, y: 0 }

export function ComicReader({ book, onExit }: ComicReaderProps) {
  const t = useT()
  const fileId = book.readFileId
  const seriesId = book.seriesId || book.card.id
  const compact = useIsCompact()
  const navigate = useNavigate()
  const [status, setStatus] = useState<Status>({ status: 'loading' })
  const [attempt, setAttempt] = useState(0)
  const [dir, setDir] = useState<ComicDir>(() => initialDir(book))
  const [view, setView] = useState<ComicView>(() => (read(viewKey(seriesId)) === 'ribbon' ? 'ribbon' : 'paged'))
  const [spreadPref, setSpreadPref] = useState(() => read(SPREAD_KEY) === 'two')
  const [page, setPage] = useState(0)
  const [menuOpen, setMenuOpen] = useState(false)
  const [zoom, setZoom] = useState<Zoom>(NO_ZOOM)
  const [failed, setFailed] = useState<Set<number>>(new Set())
  // focus in the panels keeps them up (Tab reaches them while hidden)
  const [focused, focusProps] = useFocusInside()
  const chrome = useAutoHide(menuOpen || focused)
  const chromeRef = useRef(chrome)
  chromeRef.current = chrome
  const count = status.status === 'ready' ? status.count : 0
  const spread = spreadPref && !compact && view === 'paged'
  const screens = useMemo(() => screensOf(count, spread), [count, spread])
  const screenIndex = Math.max(
    0,
    screens.findIndex((s) => s.includes(page)),
  )
  const shown = screens[screenIndex] ?? [page]
  const lastShown = shown[shown.length - 1] ?? page
  const atEnd = count > 0 && lastShown >= count - 1
  // opening a volume is not reading it: the position is saved once the reader moved
  const [opened, setOpened] = useState<number | null>(null)
  const [moved, setMoved] = useState(false)
  useEffect(() => {
    if (opened !== null && page !== opened) setMoved(true)
  }, [page, opened])
  const pos: ReadingPosition | null = count > 0 && moved ? { locator: `page:${page + 1}`, fraction: (lastShown + 1) / count } : null
  const initial = book.progress?.locator ? { locator: book.progress.locator, fraction: book.progress.fraction } : null
  const progress = useReadingProgress(book.card.id, pos, initial)
  const flush = progress.flush
  const hold = progress.hold
  const family = useReaderFamily<number>(book.card.id)

  const exit = useCallback(() => void flush(false).finally(onExit), [flush, onExit])
  const exitRef = useRef(exit)
  exitRef.current = exit
  const nextVolume = useCallback(() => {
    void flush(false).finally(() => navigate(`/read/${book.nextId}`, { replace: true }))
  }, [flush, navigate, book.nextId])

  // page count; the position from the server
  useEffect(() => {
    if (!fileId) {
      setStatus({ status: 'error', message: t('reader.comic.noFile') })
      return
    }
    const ac = new AbortController()
    setStatus({ status: 'loading' })
    getComicPages(fileId, ac.signal)
      .then((p) => {
        const at = pageIndex(book.progress?.finished ? '' : book.progress?.locator, p.count)
        setPage(at)
        setOpened(at)
        setStatus(p.count > 0 ? { status: 'ready', count: p.count, sizes: p.sizes ?? [] } : { status: 'error', message: t('reader.comic.noPages') })
      })
      .catch((e: unknown) => {
        if (ac.signal.aborted) return
        // removed, or hidden for this profile: «Повторить» cannot help
        if (e instanceof ApiError && e.status === 404) setStatus({ status: 'error', message: t(GONE_TEXT), gone: true })
        else setStatus({ status: 'error', message: e instanceof ApiError ? e.message : t('reader.comic.openFailed') })
      })
    return () => ac.abort()
    // the saved position is read once per open
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [fileId, attempt])

  const go = useCallback(
    (delta: number) => {
      if (delta === 0 || screens.length === 0) return
      const next = Math.min(Math.max(screenIndex + delta, 0), screens.length - 1)
      if (next === screenIndex) return
      hold(false) // read on from a jump to another profile's page
      setPage(screens[next]![0]!)
      setZoom(NO_ZOOM)
      chrome.hide()
    },
    [screens, screenIndex, chrome, hold],
  )

  // keys: paged ←/→ by direction, Space / PageDown / PageUp; the ribbon
  // scrolls with its own. Any other key shows the panels; Esc shows them,
  // then leaves.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.defaultPrevented || e.altKey || e.ctrlKey || e.metaKey || menuOpen) return
      const t = e.target as HTMLElement | null
      if (t?.closest('textarea, [role="menu"]')) return
      // the page slider takes its arrows; only Esc goes on from it
      if (t?.closest('input')) {
        if (e.key === 'Escape') chromeKey(e, chromeRef.current, exitRef.current)
        return
      }
      const own = view === 'paged' ? nextPageKey(e.key === ' ' && e.shiftKey ? 'PageUp' : e.key, dir) !== 0 : RIBBON_KEYS.includes(e.key)
      // Space on a control of the panels presses it
      if (!own || (e.key === ' ' && inChrome(e))) {
        if (!own) chromeKey(e, chromeRef.current, exitRef.current)
        return
      }
      if (view !== 'paged') return
      e.preventDefault()
      go(nextPageKey(e.key === ' ' && e.shiftKey ? 'PageUp' : e.key, dir))
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [view, dir, go, menuOpen])

  // the three pages after the screen, loaded ahead
  // (after the screen settles, so scrubbing the slider does not queue
  // hundreds of requests; a stale preload is dropped)
  useEffect(() => {
    if (!count || !fileId) return
    const imgs: HTMLImageElement[] = []
    const t = setTimeout(() => {
      for (let i = lastShown + 1; i <= Math.min(count - 1, lastShown + PRELOAD); i++) {
        const img = new Image()
        img.decoding = 'async'
        img.src = comicPageUrl(fileId, i)
        imgs.push(img)
      }
    }, PRELOAD_DELAY_MS)
    return () => {
      clearTimeout(t)
      for (const img of imgs) if (!img.complete) img.src = ''
    }
  }, [count, fileId, lastShown])

  const chooseDir = (d: ComicDir) => {
    setDir(d)
    write(dirKey(seriesId), d)
  }
  const chooseView = (v: ComicView) => {
    setView(v)
    setZoom(NO_ZOOM)
    write(viewKey(seriesId), v)
  }
  const chooseSpread = (two: boolean) => {
    setSpreadPref(two)
    write(SPREAD_KEY, two ? 'two' : 'one')
  }

  // --- paged: taps, swipes, pinch and double-tap zoom ---

  const stageRef = useRef<HTMLDivElement>(null)
  const zoomRef = useRef(zoom)
  zoomRef.current = zoom
  const gesture = useRef<{
    pointers: Map<number, { x: number; y: number }>
    start: { x: number; y: number; t: number; zoom: Zoom } | null
    pinch: { d: number; mid: { x: number; y: number }; zoom: Zoom } | null
    moved: boolean
    pinched: boolean
    lastTap: number
    /** A touch tap waiting to see whether it is the first of a double tap. */
    pending: ReturnType<typeof setTimeout> | null
  }>({
    pointers: new Map(),
    start: null,
    pinch: null,
    moved: false,
    pinched: false,
    lastTap: 0,
    pending: null,
  })
  useEffect(() => () => void (gesture.current.pending && clearTimeout(gesture.current.pending)), [])

  /** Keeps a zoomed page inside the stage. */
  const clampZoom = useCallback((z: Zoom): Zoom => {
    const r = stageRef.current?.getBoundingClientRect()
    const s = Math.min(Math.max(z.s, 1), MAX_ZOOM)
    if (s <= 1.02) return NO_ZOOM
    const mx = r ? ((s - 1) * r.width) / 2 : Infinity
    const my = r ? ((s - 1) * r.height) / 2 : Infinity
    return {
      s,
      x: Math.min(Math.max(z.x, -mx), mx),
      y: Math.min(Math.max(z.y, -my), my),
    }
  }, [])

  /** A point relative to the stage centre. */
  const local = (x: number, y: number) => {
    const r = stageRef.current!.getBoundingClientRect()
    return { x: x - r.left - r.width / 2, y: y - r.top - r.height / 2 }
  }

  /** Zooms to `s` keeping the stage point `p` (from the centre) in place. */
  const zoomAt = (from: Zoom, s: number, p: { x: number; y: number }, to = p): Zoom =>
    clampZoom({
      s,
      x: to.x - ((p.x - from.x) * s) / from.s,
      y: to.y - ((p.y - from.y) * s) / from.s,
    })

  const onPointerDown = (e: ReactPointerEvent) => {
    if (e.pointerType === 'mouse' && e.button !== 0) return
    const g = gesture.current
    try {
      ;(e.currentTarget as HTMLElement).setPointerCapture(e.pointerId)
    } catch {
      /* ignore */
    }
    g.pointers.set(e.pointerId, { x: e.clientX, y: e.clientY })
    if (g.pointers.size === 1) {
      g.start = {
        x: e.clientX,
        y: e.clientY,
        t: Date.now(),
        zoom: zoomRef.current,
      }
      g.moved = false
      g.pinched = false
    } else if (g.pointers.size === 2) {
      const [a, b] = [...g.pointers.values()] as [{ x: number; y: number }, { x: number; y: number }]
      g.pinch = {
        d: Math.hypot(a.x - b.x, a.y - b.y) || 1,
        mid: local((a.x + b.x) / 2, (a.y + b.y) / 2),
        zoom: zoomRef.current,
      }
      g.pinched = true
    }
  }

  const onPointerMove = (e: ReactPointerEvent) => {
    const g = gesture.current
    if (!g.pointers.has(e.pointerId)) return
    g.pointers.set(e.pointerId, { x: e.clientX, y: e.clientY })
    if (g.pinch && g.pointers.size >= 2) {
      const [a, b] = [...g.pointers.values()] as [{ x: number; y: number }, { x: number; y: number }]
      const d = Math.hypot(a.x - b.x, a.y - b.y)
      const mid = local((a.x + b.x) / 2, (a.y + b.y) / 2)
      setZoom(zoomAt(g.pinch.zoom, g.pinch.zoom.s * (d / g.pinch.d), g.pinch.mid, mid))
      return
    }
    if (!g.start) return
    const dx = e.clientX - g.start.x
    const dy = e.clientY - g.start.y
    if (Math.hypot(dx, dy) > 8) g.moved = true
    if (g.start.zoom.s > 1)
      setZoom(
        clampZoom({
          ...g.start.zoom,
          x: g.start.zoom.x + dx,
          y: g.start.zoom.y + dy,
        }),
      )
  }

  const onPointerUp = (e: ReactPointerEvent) => {
    const g = gesture.current
    if (!g.pointers.has(e.pointerId)) return
    g.pointers.delete(e.pointerId)
    if (g.pointers.size > 0) {
      // one finger left after a pinch: it pans from here
      const [p] = [...g.pointers.values()]
      g.pinch = null
      g.start = p ? { x: p.x, y: p.y, t: Date.now(), zoom: zoomRef.current } : null
      return
    }
    const s = g.start
    g.start = null
    g.pinch = null
    if (!s || g.pinched || e.type === 'pointercancel') return
    const dx = e.clientX - s.x
    const dy = e.clientY - s.y
    if (g.moved) {
      // a swipe turns the page when not zoomed: towards the reading direction
      if (s.zoom.s <= 1 && Math.abs(dx) > SWIPE_PX && Math.abs(dx) > Math.abs(dy) * 1.5) {
        go(dir === 'rtl' ? (dx > 0 ? 1 : -1) : dx < 0 ? 1 : -1)
      }
      return
    }
    const now = Date.now()
    const touch = e.pointerType !== 'mouse'
    if (touch && now - g.lastTap < DOUBLE_TAP_MS) {
      // double tap: zoom in there, or back out (the first tap did nothing)
      if (g.pending) clearTimeout(g.pending)
      g.pending = null
      g.lastTap = 0
      const z = zoomRef.current
      setZoom(z.s > 1 ? NO_ZOOM : zoomAt(z, 2.5, local(e.clientX, e.clientY)))
      return
    }
    g.lastTap = now
    if (zoomRef.current.s > 1) return // zoomed: taps do not turn
    const r = stageRef.current!.getBoundingClientRect()
    const d = tapZone(e.clientX - r.left, r.width, dir)
    const act = () => {
      g.pending = null
      if (d === 0) chrome.toggle()
      else go(d)
    }
    // on touch a tap waits for a possible second one, so a double tap never turns the page first
    if (touch) g.pending = setTimeout(act, DOUBLE_TAP_MS)
    else act()
  }

  // a trackpad pinch (ctrl + wheel) zooms on a desktop
  const onWheel = (e: WheelEvent) => {
    if (!e.ctrlKey) return
    const z = zoomRef.current
    setZoom(zoomAt(z, z.s * Math.exp(-e.deltaY / 120), local(e.clientX, e.clientY)))
  }
  // React's wheel listener is passive: block the browser's own page zoom
  useEffect(() => {
    const el = stageRef.current
    if (!el) return
    const block = (e: globalThis.WheelEvent) => e.ctrlKey && e.preventDefault()
    el.addEventListener('wheel', block, { passive: false })
    return () => el.removeEventListener('wheel', block)
  }, [view, status.status])

  // --- ribbon: the page at the top of the screen is the position ---

  const ribbonRef = useRef<HTMLDivElement>(null)
  const pageEls = useRef<(HTMLElement | null)[]>([])
  // each page's own shape once loaded; the first page's for the rest until then
  const [ratios, setRatios] = useState<Record<number, number>>({})
  // the server knows every page's size: placeholders never change height (no jumps on Safari)
  const sizes = status.status === 'ready' ? status.sizes : []
  const ratio = ratios[0] ?? sizeRatio(sizes[0]) ?? 2 / 3
  const restoring = useRef(false)

  useLayoutEffect(() => {
    if (view !== 'ribbon' || !count) return
    // open where the reader was
    restoring.current = true
    pageEls.current[page]?.scrollIntoView({ block: 'start' })
    // Space, PageDown and the arrows scroll the ribbon right away
    ribbonRef.current?.focus({ preventScroll: true })
    const t = setTimeout(() => (restoring.current = false), 150)
    return () => clearTimeout(t)
    // only when the ribbon opens
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [view, count])

  const onRibbonScroll = useCallback(() => {
    const root = ribbonRef.current
    if (!root) return
    const top = root.getBoundingClientRect().top + root.clientHeight * 0.3
    let current = 0
    pageEls.current.forEach((el, i) => {
      if (el && el.getBoundingClientRect().top <= top) current = i
    })
    setPage((p) => (p === current ? p : current))
    // the reader's own scroll (open, slider) keeps the panels up
    if (!restoring.current) {
      hold(false)
      chrome.hide()
    }
  }, [chrome, hold])

  const slider = (v: number) => {
    const i = Math.min(Math.max(v - 1, 0), count - 1)
    if (view === 'ribbon') {
      restoring.current = true
      pageEls.current[i]?.scrollIntoView({ block: 'start' })
      setTimeout(() => (restoring.current = false), 150)
      setPage(i)
    } else {
      setPage(screens.find((s) => s.includes(i))?.[0] ?? i)
      setZoom(NO_ZOOM)
    }
    chrome.poke()
  }

  // «Семья»: the other readers' pages on the slider; a jump is only looked at until the reader reads on
  const places: FamilyPlace[] =
    count > 1
      ? family.members.map((m) => {
          const n = pageOfMember(m, count)
          return { member: m, at: n - 1, label: t('comp.family.page', { n }) }
        })
      : []
  const jumpToPlace = (pl: FamilyPlace) => {
    const mine = family.ret.back?.to ?? page + 1
    hold(true)
    family.ret.offer(mine, t('comp.family.page', { n: mine }))
    slider(pl.at + 1)
  }
  const returnBack = () => {
    const back = family.ret.back
    if (!back) return
    family.ret.clear()
    hold(false)
    slider(back.to)
  }

  const markFailed = (i: number) => setFailed((f) => new Set(f).add(i))

  const titleLine = [book.series?.name, book.card.subtitle || book.card.title].filter(Boolean).join(' · ')
  const label = shown.length > 1 ? `${shown[0]! + 1}–${lastShown + 1} / ${count}` : `${page + 1} / ${count}`
  const showNext = atEnd && book.nextId > 0

  const pageImg = (i: number, eager: boolean) =>
    failed.has(i) ? (
      <span className={styles.broken}>
        <Icon name="warning" size={28} />
        {t('reader.comic.pageFailed', { n: i + 1 })}
      </span>
    ) : (
      <img
        src={comicPageUrl(fileId, i)}
        alt={t('reader.pageN', { n: i + 1 })}
        draggable={false}
        decoding="async"
        loading={eager ? 'eager' : 'lazy'}
        onError={() => markFailed(i)}
        onLoad={(e) => {
          const img = e.currentTarget
          if (img.naturalWidth > 0 && img.naturalHeight > 0) {
            const r = img.naturalWidth / img.naturalHeight
            setRatios((m) => (m[i] === r ? m : { ...m, [i]: r }))
          }
          img.dataset.loaded = 'true'
        }}
      />
    )

  if (status.status !== 'ready') {
    return (
      <div
        className={`${styles.reader} ${styles.blank}`}
        data-testid="comic-reader"
        role={status.status === 'error' ? 'alert' : undefined}
        aria-busy={status.status === 'loading' || undefined}
      >
        {status.status === 'loading' ? (
          <span className={styles.spinner} aria-label={t('reader.comic.opening')} />
        ) : (
          <div className={styles.overlay}>
            <Icon name="book" size={44} strokeWidth={1.4} />
            <h2>{status.gone ? t(GONE_TITLE) : t('reader.comic.openFailed')}</h2>
            <p>{status.message}</p>
            <div className={styles.row}>
              <Button variant="secondary" onClick={onExit}>
                {t('common.back')}
              </Button>
              {!status.gone && <Button onClick={() => setAttempt((n) => n + 1)}>{t('common.retry')}</Button>}
            </div>
          </div>
        )}
      </div>
    )
  }

  return (
    <div
      className={styles.reader}
      data-testid="comic-reader"
      data-view={view}
      data-dir={dir}
      data-page={page + 1}
      data-hidden={chrome.visible ? undefined : ''}
    >
      {view === 'paged' ? (
        <div
          ref={stageRef}
          className={styles.stage}
          onPointerDown={onPointerDown}
          onPointerMove={onPointerMove}
          onPointerUp={onPointerUp}
          onPointerCancel={onPointerUp}
          onWheel={onWheel}
          aria-label={t('reader.comic.pageAria', { title: titleLine, page: label })}
        >
          <div
            className={`${styles.spread} ${dir === 'rtl' ? styles.rtl : ''} ${shown.length > 1 ? styles.two : ''}`}
            style={{
              transform: zoom.s > 1 ? `translate3d(${zoom.x}px, ${zoom.y}px, 0) scale(${zoom.s})` : undefined,
            }}
            data-zoomed={zoom.s > 1 ? 'true' : undefined}
          >
            {shown.map((i) => (
              <div key={i} className={styles.page}>
                {pageImg(i, true)}
              </div>
            ))}
          </div>
        </div>
      ) : (
        <div
          ref={ribbonRef}
          className={styles.ribbon}
          onScroll={onRibbonScroll}
          onClick={() => chrome.toggle()}
          data-testid="comic-ribbon"
          tabIndex={0}
          aria-label={t('reader.comic.ribbonAria', { title: titleLine })}
        >
          {Array.from({ length: count }, (_, i) => (
            <div
              key={i}
              ref={(el) => {
                pageEls.current[i] = el
              }}
              className={styles.strip}
              style={{ aspectRatio: String(sizeRatio(sizes[i]) ?? ratios[i] ?? ratio) }}
              data-page={i + 1}
            >
              {pageImg(i, i >= page - 1 && i <= page + PRELOAD)}
            </div>
          ))}
          {book.nextId > 0 && (
            <div className={styles.ribbonEnd}>
              <Button
                onClick={(e) => {
                  e.stopPropagation()
                  nextVolume()
                }}
              >
                {t('reader.comic.nextVolume')}
              </Button>
            </div>
          )}
        </div>
      )}

      <header className={`${styles.bar} ${styles.top}`} {...{ [CHROME_ATTR]: '' }} {...focusProps}>
        <button type="button" className={styles.back} onClick={exit} aria-label={t('common.back')}>
          <Icon name="chevronLeft" size={22} />
          {!compact && <span>{book.series?.name || t('common.books')}</span>}
        </button>
        <span className={styles.title}>{titleLine}</span>
        <Menu
          label={t('reader.comic.view')}
          placement="bottom-end"
          className={styles.viewMenu}
          onOpenChange={setMenuOpen}
          trigger={(p) => (
            <button {...p} type="button" className={styles.tool} aria-label={t('reader.comic.view')}>
              <Icon name="sliders" size={20} />
            </button>
          )}
        >
          {(close) => (
            <>
              <MenuHeading>{t('reader.comic.mode')}</MenuHeading>
              <MenuItem label={t('reader.comic.paged')} checked={view === 'paged'} onSelect={() => (chooseView('paged'), close())} />
              <MenuItem label={t('reader.comic.ribbon')} detail={t('reader.comic.ribbonDetail')} checked={view === 'ribbon'} onSelect={() => (chooseView('ribbon'), close())} />
              <MenuSeparator />
              <MenuHeading>{t('reader.comic.direction')}</MenuHeading>
              <MenuItem label={t('reader.comic.rtl')} checked={dir === 'rtl'} onSelect={() => (chooseDir('rtl'), close())} />
              <MenuItem label={t('reader.comic.ltr')} checked={dir === 'ltr'} onSelect={() => (chooseDir('ltr'), close())} />
              {!compact && view === 'paged' && (
                <>
                  <MenuSeparator />
                  <MenuHeading>{t('reader.comic.onScreen')}</MenuHeading>
                  <MenuItem label={t('reader.comic.onePage')} checked={!spreadPref} onSelect={() => (chooseSpread(false), close())} />
                  <MenuItem label={t('reader.comic.spread')} detail={t('reader.comic.spreadDetail')} checked={spreadPref} onSelect={() => (chooseSpread(true), close())} />
                </>
              )}
            </>
          )}
        </Menu>
      </header>

      <footer className={`${styles.bar} ${styles.bottom}`} {...{ [CHROME_ATTR]: '' }} {...focusProps}>
        <span className={styles.sliderBox}>
          <input
            type="range"
            className={styles.slider}
            min={1}
            max={count}
            step={1}
            value={page + 1}
            dir={dir}
            aria-label={t('reader.comic.page')}
            aria-valuetext={label}
            onChange={(e) => {
              hold(false)
              family.ret.clear()
              slider(Number(e.target.value))
            }}
            onPointerDown={() => chrome.poke()}
          />
          {places.length > 0 && <FamilyMarks places={places} total={count - 1} dir={dir} onJump={jumpToPlace} />}
        </span>
        {progress.error && (
          <span className={styles.saveError} role="status">
            {progress.error}
          </span>
        )}
        <span className={styles.count} aria-live="polite">
          {label}
        </span>
        {showNext && (
          <Button size="small" className={styles.next} onClick={nextVolume}>
            {t('reader.comic.nextVolume')}
          </Button>
        )}
      </footer>
      {family.ret.back && (
        <ReturnPill className={styles.returnPill} label={family.ret.back.label} onReturn={returnBack} onDismiss={family.ret.clear} />
      )}
    </div>
  )
}
