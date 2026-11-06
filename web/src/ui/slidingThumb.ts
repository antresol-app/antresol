// The white thumb that slides to the chosen item (SegmentedControl, the top
// bar's sections): one element per group, placed on the chosen item's box
// and moved with a CSS transition — no layout animation library. The first
// placement does not slide; later ones do.
import { useLayoutEffect, useState, type CSSProperties, type RefObject } from 'react'

export interface Thumb {
  /** The thumb's position and width (inline style). */
  style: CSSProperties
  /** True once it has been placed: from then on it slides (data-slide). */
  slide: boolean
}

/** Where the thumb goes inside `group` (a positioned element): over the element `selector` matches; `key` changes with the choice. */
export function useSlidingThumb(group: RefObject<HTMLElement | null>, selector: string, key: unknown): Thumb | null {
  const [box, setBox] = useState<{ x: number; y: number; w: number; h: number } | null>(null)
  const [placed, setPlaced] = useState(false)
  useLayoutEffect(() => {
    const root = group.current
    if (!root) return
    const measure = () => {
      const el = root.querySelector<HTMLElement>(selector)
      if (!el) return setBox(null)
      const next = { x: el.offsetLeft, y: el.offsetTop, w: el.offsetWidth, h: el.offsetHeight }
      setBox((b) => (b && b.x === next.x && b.y === next.y && b.w === next.w && b.h === next.h ? b : next))
    }
    measure()
    if (typeof ResizeObserver === 'undefined') return
    const ro = new ResizeObserver(measure)
    ro.observe(root)
    for (const child of Array.from(root.querySelectorAll<HTMLElement>(':scope > *, :scope > * > *'))) ro.observe(child)
    return () => ro.disconnect()
  }, [group, selector, key])
  // slide only after the first frame in place
  useLayoutEffect(() => {
    if (!box || placed) return
    const id = requestAnimationFrame(() => setPlaced(true))
    return () => cancelAnimationFrame(id)
  }, [box, placed])
  if (!box) return null
  return { style: { transform: `translate(${box.x}px, ${box.y}px)`, width: box.w, height: box.h }, slide: placed }
}
