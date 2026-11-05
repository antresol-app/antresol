// PDF through pdfjs-dist: a vertical ribbon of pages, each a canvas drawn
// only while it is within two pages of the screen. «−» / «+» / «По ширине»
// zoom; ←/→ jump a page. The position is `page:N`, the fraction N / pages.
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { getDocument, GlobalWorkerOptions, type PDFDocumentProxy, type RenderTask } from 'pdfjs-dist'
import workerUrl from 'pdfjs-dist/build/pdf.worker.min.mjs?url'
import { annotationsExportUrl, downloadUrl, fileUrl } from '../api/client'
import type { BookDetail } from '../api/types'
import { Button, ButtonLink } from '../ui/Button'
import { Icon } from '../components/Icon'
import { ChromeButton, chromeKey, inChrome, ReaderChrome, useAutoHide } from './ReaderChrome'
import { useAnnotations } from './useAnnotations'
import { useReaderFamily } from './useReaderFamily'
import { useReadingProgress, type ReadingPosition } from './useReadingProgress'
import { FamilyMarks, ReturnPill } from '../components/FamilyMarks'
import { pageOfMember, type FamilyPlace } from '../player/familyMarks'
import styles from './Reader.module.css'
import { useT } from '../i18n'

GlobalWorkerOptions.workerSrc = workerUrl

export interface PdfReaderProps {
  book: BookDetail
  onExit: () => void
}

/** Pages drawn around the visible ones. */
const AROUND = 2
const MAX_AUTO_WIDTH = 880
const MIN_WIDTH = 240
const MAX_WIDTH = 2400
const ZOOM_STEP = 1.2
/** Top and bottom bars plus the ribbon's padding around a page. */
const BARS = 64 + 72
const MIN_AUTO_WIDTH = 480
/** Up to this many pages are measured before the first draw. */
const MAX_MEASURED = 400

/** `page:N` → N (1-based), or 0. */
export function pageOf(locator: string | undefined): number {
  const m = /^page:(\d+)$/.exec(locator ?? '')
  return m ? Number(m[1]) : 0
}

type Zoom = { kind: 'auto' } | { kind: 'fit' } | { kind: 'width'; px: number }
type Status = { status: 'loading' } | { status: 'ready' } | { status: 'error'; message: string }

export function PdfReader({ book, onExit }: PdfReaderProps) {
  const t = useT()
  const scrollRef = useRef<HTMLDivElement>(null)
  const [doc, setDoc] = useState<PDFDocumentProxy | null>(null)
  const [status, setStatus] = useState<Status>({ status: 'loading' })
  const [aspects, setAspects] = useState<number[]>([])
  const [zoom, setZoom] = useState<Zoom>({ kind: 'auto' })
  const [viewport, setViewport] = useState(() => (typeof window === 'undefined' ? 1024 : window.innerWidth))
  const [viewportH, setViewportH] = useState(() => (typeof window === 'undefined' ? 768 : window.innerHeight))
  const [visible, setVisible] = useState<Set<number>>(new Set([1]))
  const [page, setPage] = useState(0)
  /** The bottom of the last page is on screen. */
  const [atEnd, setAtEnd] = useState(false)
  /** The reader scrolled or paged: only then is the position saved. */
  const [moved, setMoved] = useState(false)
  const holdRef = useRef<(on: boolean) => void>(() => {})
  /** The reader scrolled or paged itself: a held jump (another profile's page) is now where it reads. */
  const markMoved = useCallback(() => {
    setMoved(true)
    holdRef.current(false)
  }, [])
  const [locked, setLocked] = useState(false)
  // the panels stay up over a loading or failed document: «‹ Книги» is the way out
  const chrome = useAutoHide(locked || status.status !== 'ready')
  const chromeRef = useRef(chrome)
  chromeRef.current = chrome
  const restored = useRef(false)
  const total = doc?.numPages ?? 0
  // the fraction counts pages passed; the whole document only at its end
  const pos: ReadingPosition | null =
    page > 0 && total > 0 ? { locator: `page:${page}`, fraction: atEnd ? 1 : (page - 1) / total } : null
  const initial = book.progress?.locator ? { locator: book.progress.locator, fraction: book.progress.fraction } : null
  const { flush, error: saveError, hold } = useReadingProgress(book.card.id, moved ? pos : null, initial)
  holdRef.current = hold
  const notes = useAnnotations(book.card.id)
  const family = useReaderFamily<number>(book.card.id)

  const exit = useCallback(() => void flush(false).finally(onExit), [flush, onExit])
  const exitRef = useRef(exit)
  exitRef.current = exit

  // open
  useEffect(() => {
    if (!book.readFileId) return
    const task = getDocument({ url: fileUrl(book.readFileId), withCredentials: true }) // pdf.js 6 has no eval path (isEvalSupported was removed)
    let cancelled = false
    task.promise
      .then(async (d) => {
        if (cancelled) return
        const first = await d.getPage(1)
        const v = first.getViewport({ scale: 1 })
        let aspects = Array.from({ length: d.numPages }, () => v.height / v.width)
        // mixed page sizes: lay out every page at its own size up front (cheap
        // on the worker), so the restored position is right
        if (d.numPages <= MAX_MEASURED) {
          aspects = await Promise.all(
            aspects.map((a, i) =>
              i === 0
                ? a
                : d.getPage(i + 1).then(
                    (p) => {
                      const pv = p.getViewport({ scale: 1 })
                      return pv.height / pv.width
                    },
                    () => a,
                  ),
            ),
          )
        }
        if (cancelled) return
        setAspects(aspects)
        setDoc(d)
        setStatus({ status: 'ready' })
      })
      .catch((e: unknown) => {
        if (cancelled) return
        console.error(e)
        setStatus({ status: 'error', message: t('reader.pdfBroken') })
      })
    return () => {
      cancelled = true
      void task.destroy()
    }
  }, [book.readFileId])

  useEffect(() => {
    const onResize = () => {
      setViewport(window.innerWidth)
      setViewportH(window.innerHeight)
    }
    window.addEventListener('resize', onResize)
    return () => window.removeEventListener('resize', onResize)
  }, [])

  const fitWidth = Math.max(MIN_WIDTH, viewport - (viewport < 700 ? 16 : 64))
  // «auto» on a wide screen: the whole first page between the two bars
  const pageFit = aspects[0] ? Math.floor((viewportH - BARS) / aspects[0]) : MAX_AUTO_WIDTH
  const autoWidth = viewport < 700 ? fitWidth : Math.min(fitWidth, MAX_AUTO_WIDTH, Math.max(MIN_AUTO_WIDTH, pageFit))
  const width = zoom.kind === 'fit' ? fitWidth : zoom.kind === 'auto' ? autoWidth : zoom.px

  // which pages are near the screen
  useEffect(() => {
    const root = scrollRef.current
    if (!root || !doc) return
    const io = new IntersectionObserver(
      (entries) =>
        setVisible((prev) => {
          const next = new Set(prev)
          for (const e of entries) {
            const n = Number((e.target as HTMLElement).dataset.page)
            if (e.isIntersecting) next.add(n)
            else next.delete(n)
          }
          return next
        }),
      { root, rootMargin: '200px 0px' },
    )
    root.querySelectorAll('[data-page]').forEach((el) => io.observe(el))
    return () => io.disconnect()
  }, [doc, aspects.length])

  const range = useMemo(() => {
    if (visible.size === 0) return [1, 1 + AROUND] as const
    const v = [...visible]
    return [Math.max(1, Math.min(...v) - AROUND), Math.min(total, Math.max(...v) + AROUND)] as const
  }, [visible, total])

  // the current page: the one crossing a line a third down the screen
  const measure = useCallback(() => {
    const root = scrollRef.current
    if (!root) return
    const line = root.scrollTop + root.clientHeight / 3
    const pages = root.querySelectorAll<HTMLElement>('[data-page]')
    let cur = pages.length ? 1 : 0
    for (const el of pages) {
      if (el.offsetTop <= line) cur = Number(el.dataset.page)
      else break
    }
    if (!restored.current) return
    setPage(cur)
    const last = pages[pages.length - 1]
    setAtEnd(!!last && last.offsetTop + last.offsetHeight <= root.scrollTop + root.clientHeight + 2)
  }, [])

  const goToPage = useCallback((n: number, smooth = false) => {
    const root = scrollRef.current
    const el = root?.querySelector<HTMLElement>(`[data-page="${n}"]`)
    if (!root || !el) return
    root.scrollTo({ top: el.offsetTop - 64, behavior: smooth && !window.matchMedia('(prefers-reduced-motion: reduce)').matches ? 'smooth' : 'auto' })
  }, [])

  // restore the saved page once the ribbon is laid out
  useLayoutEffect(() => {
    if (!doc || restored.current) return
    const n = Math.min(total, Math.max(1, pageOf(book.progress?.locator)))
    if (n > 1) goToPage(n)
    restored.current = true
    setPage(n)
    requestAnimationFrame(measure) // a short document may be on screen whole
  }, [doc, total, book.progress?.locator, goToPage, measure])

  // keep the page in view when zooming
  const pageRef = useRef(page)
  pageRef.current = page
  useLayoutEffect(() => {
    if (restored.current && pageRef.current > 1) goToPage(pageRef.current)
  }, [width, goToPage])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.defaultPrevented || e.altKey || e.ctrlKey || e.metaKey) return
      const t = e.target
      if (t instanceof Element && t.closest('input, textarea, [role="menu"], [role="dialog"], [role="alertdialog"]')) return
      // the document's own keys scroll or page; any other shows the panels (Esc: then leaves)
      if (!['ArrowDown', 'ArrowUp', 'PageDown', 'PageUp', ' ', 'Home', 'End', 'ArrowLeft', 'ArrowRight'].includes(e.key) || inChrome(e)) {
        chromeKey(e, chromeRef.current, exitRef.current)
        return
      }
      markMoved()
      if (e.key === 'ArrowRight') {
        e.preventDefault()
        markMoved()
        goToPage(Math.min(total, pageRef.current + 1), true)
      } else if (e.key === 'ArrowLeft') {
        e.preventDefault()
        markMoved()
        goToPage(Math.max(1, pageRef.current - 1), true)
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [goToPage, total, markMoved])

  const setAspect = useCallback((i: number, a: number) => {
    setAspects((prev) => (Math.abs((prev[i] ?? 0) - a) < 1e-3 ? prev : prev.map((x, k) => (k === i ? a : x))))
  }, [])

  const zoomBy = (f: number) => setZoom({ kind: 'width', px: Math.round(Math.min(MAX_WIDTH, Math.max(MIN_WIDTH, width * f))) })

  const zoomControls = (
    <>
      <ChromeButton label={t('reader.zoomOut')} onClick={() => zoomBy(1 / ZOOM_STEP)} disabled={width <= MIN_WIDTH}>
        <span className={styles.zoomGlyph}>−</span>
      </ChromeButton>
      <ChromeButton label={t('reader.zoomIn')} onClick={() => zoomBy(ZOOM_STEP)} disabled={width >= MAX_WIDTH}>
        <span className={styles.zoomGlyph}>+</span>
      </ChromeButton>
      <button type="button" className={styles.fitButton} aria-pressed={zoom.kind === 'fit'} onClick={() => setZoom({ kind: 'fit' })}>
        {t('reader.fitWidth')}
      </button>
    </>
  )

  // «Семья»: the other readers' pages on the document's line
  const places: FamilyPlace[] =
    total > 0
      ? family.members.map((m) => {
          const n = pageOfMember(m, total)
          return { member: m, at: n - 1, label: t('comp.family.page', { n }) }
        })
      : []
  const jumpToPlace = (pl: FamilyPlace) => {
    const mine = family.ret.back?.to ?? page
    hold(true)
    if (mine > 0) family.ret.offer(mine, t('comp.family.page', { n: mine }))
    goToPage(pl.at + 1)
    chrome.poke()
  }
  const returnBack = () => {
    const back = family.ret.back
    if (!back) return
    family.ret.clear()
    hold(false)
    goToPage(back.to)
  }

  // bookmarks: `page:N`
  const here = page > 0 ? `page:${page}` : ''
  const pageMarks = notes.list.filter((a) => a.kind === 'bookmark' && a.locator === here)
  const toggleBookmark = () => {
    if (!here || !pos) return
    if (pageMarks.length > 0) pageMarks.forEach(notes.remove)
    else notes.add({ kind: 'bookmark', locator: here, fraction: pos.fraction, label: t('reader.annotations.page', { n: page }) })
  }

  return (
    <div className={`${styles.reader} ${styles.pdf}`} data-theme="night" data-testid="reader" data-locator={pos?.locator}>
      <div
        ref={scrollRef}
        className={styles.ribbon}
        onScroll={measure}
        onWheel={markMoved}
        onTouchMove={markMoved}
        onPointerDown={(e) => {
          // dragging the scroll bar
          if (e.target === e.currentTarget) markMoved()
        }}
        onClick={(e) => {
          if ((e.target as Element).closest('button, a')) return
          chrome.toggle()
        }}
      >
        {doc &&
          aspects.map((a, i) => (
            <PdfPage key={i} doc={doc} n={i + 1} width={width} aspect={a} draw={i + 1 >= range[0] && i + 1 <= range[1]} onAspect={setAspect} />
          ))}
      </div>
      {status.status === 'loading' && (
        <div className={styles.overlay} aria-busy="true" aria-label={t('reader.openingDocument')}>
          <span className={styles.spinner} />
        </div>
      )}
      {status.status === 'error' && (
        <div className={styles.overlay} role="alert">
          <Icon name="book" size={44} strokeWidth={1.4} />
          <h2>{t('reader.documentOpenFailed')}</h2>
          <p>{status.message}</p>
          <div className={styles.row}>
            <Button variant="secondary" onClick={exit}>
              {t('common.back')}
            </Button>
            <ButtonLink to={downloadUrl(book.readFileId)} reloadDocument variant="secondary" icon="download">
              {t('reader.downloadFile')}
            </ButtonLink>
          </div>
        </div>
      )}
      <ReaderChrome
        title={book.card.title}
        visible={chrome.visible}
        theme="night"
        onBack={exit}
        chapter={page > 0 && total > 0 ? t('reader.pageOf', { page, total }) : ''}
        fraction={pos ? pos.fraction : null}
        onLockChange={setLocked}
        extra={zoomControls}
        note={saveError ?? notes.notice}
        bookmarked={pageMarks.length > 0}
        onBookmark={total > 0 ? toggleBookmark : undefined}
        annotations={{
          list: notes.list.filter((a) => a.kind === 'bookmark'),
          onJump: (a) => {
            const n = pageOf(a.locator)
            if (n > 0) {
              markMoved()
              family.ret.clear()
              goToPage(n)
            }
          },
          onRemove: notes.remove,
          exportUrl: annotationsExportUrl(book.card.id),
          failed: notes.failed,
        }}
        family={places.length > 0 && <FamilyMarks places={places} total={total} onJump={jumpToPlace} />}
      />
      {family.ret.back && (
        <ReturnPill className={styles.returnPill} label={family.ret.back.label} onReturn={returnBack} onDismiss={family.ret.clear} />
      )}
    </div>
  )
}

interface PdfPageProps {
  doc: PDFDocumentProxy
  n: number
  width: number
  aspect: number
  draw: boolean
  onAspect: (index: number, aspect: number) => void
}

function PdfPage({ doc, n, width, aspect, draw, onAspect }: PdfPageProps) {
  const t = useT()
  const canvasRef = useRef<HTMLCanvasElement>(null)
  const [drawn, setDrawn] = useState(false)

  useEffect(() => {
    const canvas = canvasRef.current
    if (!canvas) return
    if (!draw) {
      // free the bitmap of pages far away
      canvas.width = 0
      canvas.height = 0
      setDrawn(false)
      return
    }
    let task: RenderTask | null = null
    let cancelled = false
    void doc
      .getPage(n)
      .then((p) => {
        if (cancelled) return
        const base = p.getViewport({ scale: 1 })
        onAspect(n - 1, base.height / base.width)
        const dpr = Math.min(window.devicePixelRatio || 1, 3)
        const vp = p.getViewport({ scale: (width / base.width) * dpr })
        canvas.width = Math.floor(vp.width)
        canvas.height = Math.floor(vp.height)
        task = p.render({ canvas, viewport: vp })
        return task.promise.then(() => !cancelled && setDrawn(true))
      })
      .catch((e: unknown) => {
        if (!cancelled && (e as { name?: string })?.name !== 'RenderingCancelledException') console.error(e)
      })
    return () => {
      cancelled = true
      task?.cancel()
    }
  }, [doc, n, width, draw, onAspect])

  return (
    <div className={styles.page} data-page={n} style={{ width, height: Math.round(width * aspect) }}>
      <canvas ref={canvasRef} className={styles.canvas} aria-label={t('reader.pageN', { n })} data-drawn={drawn || undefined} />
    </div>
  )
}
