// «Выберите кадр»: the picture a profile's photo is made from, under a round
// window to drag and zoom (a pinch, the wheel, the buttons, the arrow keys).
// What is saved is the square behind the window (photoCrop.ts); the circle is
// how every place draws it.
import { useEffect, useLayoutEffect, useRef, useState, type KeyboardEvent, type PointerEvent } from 'react'
import { IconButton } from '../ui/IconButton'
import { Sheet } from '../ui/Sheet'
import { useT } from '../i18n'
import { MAX_ZOOM, baseScale, centered, clampCrop, renderCrop, sourceRect, zoomAt, type Crop } from './photoCrop'
import styles from './PhotoCropSheet.module.css'

export interface PhotoCropSheetProps {
  file: File
  onCancel: () => void
  /** The framed square, as a JPEG. */
  onDone: (photo: Blob) => void
}

const STEP = 1.2
const PAN = 12

export function PhotoCropSheet({ file, onCancel, onDone }: PhotoCropSheetProps) {
  const t = useT()
  const [img, setImg] = useState<HTMLImageElement | null>(null)
  const [src, setSrc] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const frame = useRef<HTMLDivElement>(null)
  const [view, setView] = useState(0)
  const [crop, setCrop] = useState<Crop | null>(null)
  const pointers = useRef(new Map<number, { x: number; y: number }>())
  const pinch = useRef(0)

  // the picture: the browser decodes it (it also applies a photo's rotation)
  useEffect(() => {
    const url = URL.createObjectURL(file)
    const im = new Image()
    im.onload = () => {
      setSrc(url)
      setImg(im)
    }
    im.onerror = () => setError(t('comp.photo.openFailed'))
    im.src = url
    return () => {
      im.onload = im.onerror = null
      URL.revokeObjectURL(url)
    }
  }, [file, t])

  // the window's size on this screen (a phone's sheet is narrower than the wide one's)
  useLayoutEffect(() => {
    const el = frame.current
    if (!el) return
    const measure = () => setView(Math.round(el.getBoundingClientRect().width))
    measure()
    if (typeof ResizeObserver === 'undefined') return
    const ro = new ResizeObserver(measure)
    ro.observe(el)
    return () => ro.disconnect()
  }, [img])

  const iw = img?.naturalWidth ?? 0
  const ih = img?.naturalHeight ?? 0
  const ready = !!img && view > 0 && iw > 0 && ih > 0

  // framed in the middle once it is known; kept in proportion when the window's size changes
  const lastView = useRef(0)
  useEffect(() => {
    if (!ready) return
    setCrop((c) => {
      if (!c || lastView.current === 0) return centered(iw, ih, view)
      const k = view / lastView.current
      return clampCrop({ zoom: c.zoom, x: c.x * k, y: c.y * k }, iw, ih, view)
    })
    lastView.current = view
  }, [ready, iw, ih, view])

  const change = (f: (c: Crop) => Crop) => setCrop((c) => (c && ready ? f(c) : c))
  const zoomBy = (factor: number, cx = view / 2, cy = view / 2) => change((c) => zoomAt(c, factor, cx, cy, iw, ih, view))

  // the wheel (a trackpad's pinch is a wheel with ctrl): not passive, so the page behind stays put
  useEffect(() => {
    const el = frame.current
    if (!el) return
    const onWheel = (e: WheelEvent) => {
      e.preventDefault()
      const r = el.getBoundingClientRect()
      zoomBy(Math.exp(-e.deltaY * (e.ctrlKey ? 0.01 : 0.0015)), e.clientX - r.left, e.clientY - r.top)
    }
    el.addEventListener('wheel', onWheel, { passive: false })
    return () => el.removeEventListener('wheel', onWheel)
  })

  const dist = () => {
    const [a, b] = [...pointers.current.values()]
    return a && b ? Math.hypot(a.x - b.x, a.y - b.y) : 0
  }
  const onDown = (e: PointerEvent<HTMLDivElement>) => {
    e.currentTarget.setPointerCapture?.(e.pointerId)
    pointers.current.set(e.pointerId, { x: e.clientX, y: e.clientY })
    if (pointers.current.size === 2) pinch.current = dist()
  }
  const onMove = (e: PointerEvent<HTMLDivElement>) => {
    const prev = pointers.current.get(e.pointerId)
    if (!prev) return
    pointers.current.set(e.pointerId, { x: e.clientX, y: e.clientY })
    if (pointers.current.size === 1) {
      const dx = e.clientX - prev.x
      const dy = e.clientY - prev.y
      change((c) => clampCrop({ ...c, x: c.x + dx, y: c.y + dy }, iw, ih, view))
    } else if (pointers.current.size === 2 && pinch.current > 0) {
      const d = dist()
      const r = e.currentTarget.getBoundingClientRect()
      const [a, b] = [...pointers.current.values()]
      zoomBy(d / pinch.current, (a!.x + b!.x) / 2 - r.left, (a!.y + b!.y) / 2 - r.top)
      pinch.current = d
    }
  }
  const onUp = (e: PointerEvent<HTMLDivElement>) => {
    pointers.current.delete(e.pointerId)
    pinch.current = 0
  }
  const onKey = (e: KeyboardEvent<HTMLDivElement>) => {
    const pan: Record<string, [number, number]> = { ArrowLeft: [PAN, 0], ArrowRight: [-PAN, 0], ArrowUp: [0, PAN], ArrowDown: [0, -PAN] }
    const p = pan[e.key]
    if (p) change((c) => clampCrop({ ...c, x: c.x + p[0], y: c.y + p[1] }, iw, ih, view))
    else if (e.key === '+' || e.key === '=') zoomBy(STEP)
    else if (e.key === '-' || e.key === '_') zoomBy(1 / STEP)
    else return
    e.preventDefault()
  }

  const done = async () => {
    if (!img || !crop || !ready) return
    setBusy(true)
    try {
      onDone(await renderCrop(img, sourceRect(crop, iw, ih, view)))
    } catch {
      setError(t('comp.photo.saveFailed'))
      setBusy(false)
    }
  }

  const s = ready && crop ? baseScale(iw, ih, view) * crop.zoom : 0
  return (
    <Sheet
      title={t('comp.photo.cropTitle')}
      role="dialog"
      size="wide"
      busy={busy}
      onCancel={onCancel}
      actions={[
        { label: t('common.cancel'), kind: 'cancel', run: onCancel },
        { label: t('comp.photo.done'), kind: 'primary', disabled: !ready || !crop, run: () => void done() },
      ]}
    >
      <div className={styles.stage}>
        <div
          ref={frame}
          className={styles.frame}
          tabIndex={0}
          role="group"
          aria-label={t('comp.photo.frame')}
          onPointerDown={onDown}
          onPointerMove={onMove}
          onPointerUp={onUp}
          onPointerCancel={onUp}
          onKeyDown={onKey}
          data-testid="photo-frame"
        >
          {ready && crop && src && (
            <img className={styles.img} src={src} alt="" draggable={false} style={{ width: iw * s, height: ih * s, transform: `translate(${crop.x}px, ${crop.y}px)` }} />
          )}
          <div className={styles.mask} aria-hidden />
        </div>
        <div className={styles.zoom}>
          <IconButton icon="minus" label={t('comp.photo.zoomOut')} disabled={!crop || crop.zoom <= 1} onClick={() => zoomBy(1 / STEP)} />
          <IconButton icon="plus" label={t('comp.photo.zoomIn')} disabled={!crop || crop.zoom >= MAX_ZOOM} onClick={() => zoomBy(STEP)} />
        </div>
        {error ? (
          <p className={styles.error} role="alert">
            {error}
          </p>
        ) : (
          <p className={styles.hint}>{t('comp.photo.cropHint')}</p>
        )}
      </div>
    </Sheet>
  )
}
