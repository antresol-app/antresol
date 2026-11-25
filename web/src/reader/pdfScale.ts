// How sharp a PDF page is drawn: the device's pixels per CSS pixel, but never more
// canvas than a phone can hold (iOS Safari draws nothing beyond about 16.7 Mpx and
// kills the tab when several big canvases are alive).

/** The most pixels one page's canvas may have. */
export const MAX_CANVAS_PIXELS = 12e6

/** The viewport scale for a page of `base` size (scale 1) shown `width` CSS pixels wide. */
export function canvasScale(base: { width: number; height: number }, width: number, dpr: number): number {
  const scale = (width / base.width) * dpr
  const pixels = base.width * base.height * scale * scale
  return pixels > MAX_CANVAS_PIXELS ? scale * Math.sqrt(MAX_CANVAS_PIXELS / pixels) : scale
}
