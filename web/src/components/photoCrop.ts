// The arithmetic of framing a profile's photo: the picture lies under a square
// window (the crop), zoomed from "the short side fills the window" to MAX_ZOOM
// times that, and dragged anywhere that keeps the window covered. Pure, so it
// is tested apart from the sheet that draws it (PhotoCropSheet).

/** The most the picture is magnified past "the short side fills the window". */
export const MAX_ZOOM = 4
/** The largest side of the photo that is sent (the server keeps 512 px too). */
export const OUTPUT_MAX = 512
/** The largest file the person may pick: a phone's photo is a few MB, this only stops a huge decode. */
export const SOURCE_MAX_BYTES = 25 * 1024 * 1024

/** Where the picture lies: its magnification and its top-left corner in the window (x, y ≤ 0). */
export interface Crop {
  zoom: number
  x: number
  y: number
}

/** The magnification (picture px → window px) at zoom 1: the short side fills the window. */
export const baseScale = (iw: number, ih: number, view: number) => view / Math.min(iw, ih)

/** Keeps the zoom in range and the window covered by the picture. */
export function clampCrop(c: Crop, iw: number, ih: number, view: number): Crop {
  const zoom = Math.min(MAX_ZOOM, Math.max(1, c.zoom))
  const s = baseScale(iw, ih, view) * zoom
  return { zoom, x: Math.min(0, Math.max(view - iw * s, c.x)), y: Math.min(0, Math.max(view - ih * s, c.y)) }
}

/** The picture's middle under the window, at zoom 1. */
export const centered = (iw: number, ih: number, view: number): Crop => clampCrop({ zoom: 1, x: (view - iw * baseScale(iw, ih, view)) / 2, y: (view - ih * baseScale(iw, ih, view)) / 2 }, iw, ih, view)

/** Zooms by `factor` about the window point (cx, cy): the picture's point under it stays there. */
export function zoomAt(c: Crop, factor: number, cx: number, cy: number, iw: number, ih: number, view: number): Crop {
  const zoom = Math.min(MAX_ZOOM, Math.max(1, c.zoom * factor))
  const k = zoom / c.zoom
  return clampCrop({ zoom, x: cx - (cx - c.x) * k, y: cy - (cy - c.y) * k }, iw, ih, view)
}

/** The square of the picture (in its own pixels) that the window shows. */
export function sourceRect(c: Crop, iw: number, ih: number, view: number): { sx: number; sy: number; size: number } {
  const s = baseScale(iw, ih, view) * c.zoom
  return { sx: -c.x / s, sy: -c.y / s, size: view / s }
}

/** How many pixels the sent square has: the source's, but never past OUTPUT_MAX. */
export const outputSize = (size: number) => Math.min(OUTPUT_MAX, Math.max(1, Math.round(size)))

/** The framed square as a JPEG (a transparent picture is laid on the dark ground the server uses too). */
export function renderCrop(img: CanvasImageSource, rect: { sx: number; sy: number; size: number }): Promise<Blob> {
  const out = outputSize(rect.size)
  const canvas = document.createElement('canvas')
  canvas.width = canvas.height = out
  const ctx = canvas.getContext('2d')
  if (!ctx) return Promise.reject(new Error('no canvas'))
  ctx.fillStyle = '#2c2c2e'
  ctx.fillRect(0, 0, out, out)
  ctx.imageSmoothingQuality = 'high'
  ctx.drawImage(img, rect.sx, rect.sy, rect.size, rect.size, 0, 0, out, out)
  return new Promise((resolve, reject) => canvas.toBlob((b) => (b ? resolve(b) : reject(new Error('no blob'))), 'image/jpeg', 0.9))
}
