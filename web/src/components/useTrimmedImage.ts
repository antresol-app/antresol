import { useEffect, useState } from 'react'

// Title logos (clearlogo.png, TMDB logos) often carry wide transparent margins,
// which pushes them off the text's left edge and makes them look small. This
// crops the transparent border once per URL; on any failure the original is used.
// It also tells a dark logo (black or grey type, made for light posters): one
// that would vanish on the dark artwork is shown white. A dark logo with
// colour (deep red, navy) keeps it: inverting it would make a brand mark a
// white silhouette.
interface Trimmed {
  url: string
  dark: boolean
}
const cache = new Map<string, Promise<Trimmed>>()

/** Mean relative luminance (0..1) under which a logo reads as dark on the dark hero (contrast < 3:1 against black). */
const DARK_LUMINANCE = 0.1
/** Mean saturation (0..1) under which a dark logo is black or grey type, not a coloured mark. */
const GREY_SATURATION = 0.25
/** Below this channel maximum a pixel is black: its hue is noise, it counts as unsaturated. */
const BLACK_MAX = 32
const linear = (c: number) => (c <= 10.3 ? c / 3294.6 : ((c / 255 + 0.055) / 1.055) ** 2.4)

/**
 * Whether the RGBA pixels read as black/grey type that vanishes on the dark
 * hero: dark (mean luminance) and desaturated (mean HSV saturation) both.
 */
export function isDarkGreyLogo(data: ArrayLike<number>): boolean {
  let lum = 0,
    sat = 0,
    solid = 0
  for (let i = 0; i + 3 < data.length; i += 4) {
    if (data[i + 3]! <= 128) continue
    const r = data[i]!,
      g = data[i + 1]!,
      b = data[i + 2]!
    lum += 0.2126 * linear(r) + 0.7152 * linear(g) + 0.0722 * linear(b)
    const max = Math.max(r, g, b)
    if (max >= BLACK_MAX) sat += (max - Math.min(r, g, b)) / max
    solid++
  }
  return solid > 0 && lum / solid < DARK_LUMINANCE && sat / solid < GREY_SATURATION
}

function trim(src: string): Promise<Trimmed> {
  const hit = cache.get(src)
  if (hit) return hit
  const p = new Promise<Trimmed>((done) => {
    let dark = false
    const resolve = (url: string) => done({ url, dark })
    const img = new Image()
    img.decoding = 'async'
    img.onerror = () => resolve(src)
    img.onload = () => {
      try {
        const w = img.naturalWidth
        const h = img.naturalHeight
        if (!w || !h) return resolve(src)
        const canvas = document.createElement('canvas')
        canvas.width = w
        canvas.height = h
        const ctx = canvas.getContext('2d', { willReadFrequently: true })
        if (!ctx) return resolve(src)
        ctx.drawImage(img, 0, 0)
        const { data } = ctx.getImageData(0, 0, w, h)
        let top = h,
          left = w,
          right = -1,
          bottom = -1
        for (let y = 0; y < h; y++) {
          for (let x = 0; x < w; x++) {
            const i = (y * w + x) * 4
            if (data[i + 3]! > 8) {
              if (x < left) left = x
              if (x > right) right = x
              if (y < top) top = y
              if (y > bottom) bottom = y
            }
          }
        }
        if (right < 0) return resolve(src)
        dark = isDarkGreyLogo(data)
        const cw = right - left + 1
        const ch = bottom - top + 1
        if (cw >= w - 2 && ch >= h - 2) return resolve(src)
        const out = document.createElement('canvas')
        out.width = cw
        out.height = ch
        out.getContext('2d')!.drawImage(canvas, left, top, cw, ch, 0, 0, cw, ch)
        out.toBlob((b) => resolve(b ? URL.createObjectURL(b) : src), 'image/png')
      } catch {
        resolve(src) // cross-origin or canvas unavailable
      }
    }
    img.src = src
  })
  cache.set(src, p)
  return p
}

/** A version of `src` without transparent margins (undefined while working) and whether the logo is dark. */
export function useTrimmedLogo(src: string | undefined): { url?: string; dark: boolean } {
  const [out, setOut] = useState<{ src: string; t: Trimmed } | null>(null)
  useEffect(() => {
    if (!src) return
    let live = true
    void trim(src).then((t) => live && setOut({ src, t }))
    return () => {
      live = false
    }
  }, [src])
  if (!src || !out || out.src !== src) return { dark: false }
  return { url: out.t.url, dark: out.t.dark }
}

/** Returns a version of `src` without transparent margins (or `src` itself while working). */
export function useTrimmedImage(src: string | undefined): string | undefined {
  return useTrimmedLogo(src).url
}
