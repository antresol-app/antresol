// Thumbnail of an entry, loaded only when it scrolls near the viewport
// (IntersectionObserver). Until it arrives — or when there is none — the
// Finder-style icon is shown; the picture fades in over it.
import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { thumbUrl } from '../../api/files'
import type { FileEntry } from '../../api/types'
import { FileIcon, folderEmblem } from './FileIcon'
import styles from './Thumb.module.css'

const io: { obs?: IntersectionObserver; cbs: Map<Element, (inView: boolean) => void> } = { cbs: new Map() }

/** Reports when el comes near the viewport and when it leaves again. */
function observe(el: Element, cb: (inView: boolean) => void) {
  if (typeof IntersectionObserver === 'undefined') {
    cb(true)
    return () => {}
  }
  if (!io.obs) {
    io.obs = new IntersectionObserver(
      (items) => {
        for (const it of items) io.cbs.get(it.target)?.(it.isIntersecting)
      },
      { rootMargin: '300px' },
    )
  }
  io.cbs.set(el, cb)
  io.obs.observe(el)
  return () => {
    io.cbs.delete(el)
    io.obs?.unobserve(el)
  }
}

const dpr = () => (typeof window !== 'undefined' ? Math.min(3, window.devicePixelRatio || 1) : 1)

export interface ThumbProps {
  entry: FileEntry
  /** Box size in CSS pixels (square). */
  size: number
  /** Large previews (Quick Look, Свойства) skip the lazy wait. */
  eager?: boolean
}

export function Thumb({ entry, size, eager }: ThumbProps) {
  const ref = useRef<HTMLSpanElement>(null)
  const imgRef = useRef<HTMLImageElement>(null)
  const [visible, setVisible] = useState(Boolean(eager))
  const [state, setState] = useState<'idle' | 'loaded' | 'failed'>('idle')
  const loaded = useRef(false)
  const src = entry.thumb ? thumbUrl(entry.thumb, size * dpr()) : undefined

  useEffect(() => {
    loaded.current = false
    setState('idle')
  }, [src])
  // Watch while the picture is still on its way: leaving the viewport cancels it.
  useEffect(() => {
    if (eager || !src || state !== 'idle' || !ref.current) return
    return observe(ref.current, setVisible)
  }, [eager, src, state])

  const showImg = src && visible && state !== 'failed'
  // A picture that has not arrived is cancelled when it goes away (scrolled
  // off, unmounted): clearing src aborts the request in every browser.
  useLayoutEffect(() => {
    const img = imgRef.current
    if (!showImg || !img) return
    return () => {
      if (!loaded.current) {
        img.dataset.cancelled = '1'
        img.src = ''
      }
    }
  }, [showImg, src])

  const poster = entry.isDir
  return (
    <span ref={ref} className={styles.box} style={{ width: size, height: size }} data-thumb={state === 'loaded' ? 'loaded' : undefined}>
      {state !== 'loaded' && <FileIcon entry={entry} size={size} emblem={folderEmblem(entry)} className={styles.icon} />}
      {showImg && (
        <span className={`${styles.frame} ${poster ? styles.poster : ''} ${state === 'loaded' ? styles.in : ''}`}>
          <img
            ref={imgRef}
            src={src}
            alt=""
            // Pixel bounds: percentage heights do not resolve inside the grid box.
            style={poster ? { height: size, width: Math.round((size * 2) / 3) } : { maxWidth: size, maxHeight: size }}
            decoding="async"
            draggable={false}
            onLoad={() => {
              loaded.current = true
              setState('loaded')
            }}
            onError={(e) => {
              if (!e.currentTarget.dataset.cancelled) setState('failed')
            }}
          />
          {poster && state === 'loaded' && size >= 48 && (
            <span className={styles.badge} aria-hidden>
              <FileIcon entry={entry} size={Math.max(14, Math.round(size * 0.3))} />
            </span>
          )}
        </span>
      )}
    </span>
  )
}
