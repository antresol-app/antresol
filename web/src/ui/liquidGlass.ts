// Liquid Glass's refraction (design/glass.css .glass-liquid) where the
// engine can draw it. iOS 26 bends what passes under a bar's rim like a lens;
// on the web that is an SVG feDisplacementMap applied to the backdrop
// (backdrop-filter: url(#…)), which only Chromium draws. WebKit and Gecko
// accept the same declaration and draw nothing at all, not even the blur
// beside it, and CSS.supports() says yes in all three, so the engine is told
// by its name, and everywhere else the bar keeps the CSS-only material.
//
// The filter is the bar's whole material behind its tint: the backdrop
// blurred as .glass-float blurs it, and over that, in the band --liquid-bezel
// in from the rounded edge, the backdrop refracted and only lightly blurred,
// so what slides under the rim bulges and stays recognisable while the
// labels sit on colour (iOS 26). The refraction is a displacement map, a
// picture of the element: neutral in the middle and, near the edge, a push
// inward (red for x, green for y) growing towards the rim, so the rim shows
// what lies a little further in — the bulge of a convex lens; its blue is how
// much of the sharp band shows over the blur there. The map is drawn once per
// size and radius (a 1x canvas: it is smooth, the filter scales it) and kept
// in one hidden <svg>; the element points at it through --liquid-lens.
import { useEffect, type RefObject } from 'react'

const NS = 'http://www.w3.org/2000/svg'
/** How long a size must hold before its map is drawn (a fold's spring keeps changing it). */
const SETTLE_MS = 120
/** Filters kept; the bars use a handful of sizes (each width, folded and not). */
const KEEP = 24

interface Nav {
  userAgentData?: { brands?: { brand: string }[] }
}

/** Chromium (Blink) draws an SVG filter in backdrop-filter; nothing else does. */
export function bendsBackdrop(nav: Navigator & Nav = navigator): boolean {
  if (nav.userAgentData?.brands?.some((b) => /Chromium/.test(b.brand))) return true
  // userAgentData is only there in a secure context (not on a LAN address over http)
  const ua = nav.userAgent
  return /\b(?:Chrome|Chromium)\/\d/.test(ua) && !/\b(?:CriOS|FxiOS|EdgiOS|Firefox)\//.test(ua)
}

/**
 * The lens at a pixel of a w×h rounded rectangle of radius r: [dx, dy] in
 * −1…1, the push, pointing inward, strongest at the rim and gone `bezel` in;
 * and `rim` in 0…1, how much of the refracted band shows there (1 at the
 * edge, 0 past the band). Exported for the tests.
 */
export function displacement(x: number, y: number, w: number, h: number, r: number, bezel: number): [number, number, number] {
  const px = x - w / 2
  const py = y - h / 2
  const qx = Math.abs(px) - (w / 2 - r)
  const qy = Math.abs(py) - (h / 2 - r)
  // the signed distance to the rounded edge (negative inside)
  const outside = Math.hypot(Math.max(qx, 0), Math.max(qy, 0))
  const d = -(outside + Math.min(Math.max(qx, qy), 0) - r)
  if (d >= bezel) return [0, 0, 0]
  if (d <= 0) return [0, 0, 1]
  // the outward normal: along the corner's radius, else the nearer side's
  let nx: number
  let ny: number
  if (qx > 0 && qy > 0) {
    const l = Math.hypot(qx, qy)
    nx = (qx / l) * Math.sign(px)
    ny = (qy / l) * Math.sign(py)
  } else if (qx > qy) {
    nx = Math.sign(px)
    ny = 0
  } else {
    nx = 0
    ny = Math.sign(py)
  }
  // a thick rim: the slope, and so the bend, rises fast towards the edge
  const t = 1 - d / bezel
  const m = t * t
  return [-nx * m, -ny * m, t]
}

function mapURL(w: number, h: number, r: number, bezel: number): string {
  const c = document.createElement('canvas')
  c.width = w
  c.height = h
  const g = c.getContext('2d')
  if (!g) return ''
  const img = g.createImageData(w, h)
  for (let y = 0; y < h; y++) {
    for (let x = 0; x < w; x++) {
      const [dx, dy, rim] = displacement(x + 0.5, y + 0.5, w, h, r, bezel)
      const i = (y * w + x) * 4
      img.data[i] = Math.round(128 + 127 * dx)
      img.data[i + 1] = Math.round(128 + 127 * dy)
      img.data[i + 2] = Math.round(255 * rim)
      img.data[i + 3] = 255
    }
  }
  g.putImageData(img, 0, 0)
  return c.toDataURL('image/png')
}

let defs: SVGSVGElement | null = null
const made = new Map<string, Element>()

interface Lens {
  /** the band along the rim that refracts */
  bezel: number
  /** how far its rim pushes */
  shift: number
  /** the bar's blur (the middle) and the band's */
  blur: number
  rimBlur: number
}

const attrs = (el: Element, a: Record<string, string>) => {
  for (const [k, v] of Object.entries(a)) el.setAttribute(k, v)
  return el
}

/** The id of the filter for this size, drawn on first use. */
function filterFor(w: number, h: number, r: number, { bezel, shift, blur, rimBlur }: Lens): string {
  const id = `liquid-${w}x${h}-${Math.round(r)}-${bezel}-${shift}-${blur}-${rimBlur}`
  if (made.has(id)) return id
  const href = mapURL(w, h, r, bezel)
  if (!href) return ''
  if (!defs) {
    defs = document.createElementNS(NS, 'svg')
    defs.setAttribute('aria-hidden', 'true')
    defs.setAttribute('width', '0')
    defs.setAttribute('height', '0')
    // not display:none: a filter inside an undisplayed <svg> is not drawn
    defs.style.cssText = 'position:absolute;width:0;height:0;overflow:hidden;pointer-events:none'
    document.body.appendChild(defs)
  }
  const box = { x: '0', y: '0', width: String(w), height: String(h) }
  const el = (tag: string, a: Record<string, string>) => attrs(document.createElementNS(NS, tag), a)
  const f = el('filter', { ...box, filterUnits: 'userSpaceOnUse', primitiveUnits: 'userSpaceOnUse', 'color-interpolation-filters': 'sRGB' })
  f.id = id
  f.append(
    // the middle: the bar's own blur
    el('feGaussianBlur', { in: 'SourceGraphic', stdDeviation: String(blur), edgeMode: 'duplicate', result: 'soft' }),
    el('feImage', { ...box, href, preserveAspectRatio: 'none', result: 'map' }),
    // the rim: bent (a channel at 255 moves by scale / 2: the full push is `shift`), barely blurred
    el('feDisplacementMap', { in: 'SourceGraphic', in2: 'map', scale: String(2 * shift), xChannelSelector: 'R', yChannelSelector: 'G', result: 'bent' }),
    el('feGaussianBlur', { in: 'bent', stdDeviation: String(rimBlur), edgeMode: 'duplicate', result: 'bentSoft' }),
    // shown where the map's blue says, over the blur
    el('feColorMatrix', { in: 'map', type: 'matrix', values: '0 0 0 0 0  0 0 0 0 0  0 0 0 0 0  0 0 1 0 0', result: 'band' }),
    el('feComposite', { in: 'bentSoft', in2: 'band', operator: 'in', result: 'rim' }),
    el('feComposite', { in: 'rim', in2: 'soft', operator: 'over' }),
  )
  defs.appendChild(f)
  made.set(id, f)
  if (made.size > KEEP) {
    const [old, gone] = made.entries().next().value!
    made.delete(old)
    gone.remove()
  }
  return id
}

const px = (el: Element, prop: string) => parseFloat(getComputedStyle(el).getPropertyValue(prop)) || 0

/**
 * Gives a .glass-liquid element its refraction where the engine draws it:
 * keeps --liquid-lens pointing at a map of the element's size and radius.
 * While the size moves (a fold) the map is dropped and the plain material
 * shows; it comes back once the size holds.
 */
export function useLiquidGlass(ref: RefObject<HTMLElement | null>) {
  useEffect(() => {
    const el = ref.current
    if (!el || typeof ResizeObserver === 'undefined' || !bendsBackdrop()) return
    const transparencyOff = window.matchMedia?.('(prefers-reduced-transparency: reduce)')
    let timer = 0
    const apply = () => {
      const w = el.offsetWidth
      const h = el.offsetHeight
      const shift = px(el, '--liquid-refraction')
      if (!w || !h || !shift || transparencyOff?.matches) return el.style.removeProperty('--liquid-lens')
      const r = Math.min(parseFloat(getComputedStyle(el).borderTopLeftRadius) || 0, w / 2, h / 2)
      const id = filterFor(w, h, r, { bezel: px(el, '--liquid-bezel'), shift, blur: px(el, '--glass-blur-float'), rimBlur: px(el, '--liquid-rim-blur') })
      if (id) el.style.setProperty('--liquid-lens', `url(#${id})`)
    }
    const ro = new ResizeObserver(() => {
      el.style.removeProperty('--liquid-lens')
      window.clearTimeout(timer)
      timer = window.setTimeout(apply, SETTLE_MS)
    })
    ro.observe(el)
    transparencyOff?.addEventListener?.('change', apply)
    return () => {
      ro.disconnect()
      window.clearTimeout(timer)
      transparencyOff?.removeEventListener?.('change', apply)
      el.style.removeProperty('--liquid-lens')
    }
  }, [ref])
}
