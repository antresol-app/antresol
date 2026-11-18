import { describe, expect, it } from 'vitest'
import { MAX_ZOOM, baseScale, centered, clampCrop, outputSize, sourceRect, zoomAt } from '../photoCrop'

const VIEW = 280

describe('photo crop', () => {
  it('starts on the middle square: the short side fills the window', () => {
    const c = centered(1200, 800, VIEW) // landscape
    expect(c.zoom).toBe(1)
    expect(baseScale(1200, 800, VIEW)).toBeCloseTo(0.35)
    expect(c.y).toBeCloseTo(0)
    expect(c.x).toBeCloseTo((VIEW - 1200 * 0.35) / 2)
    const r = sourceRect(c, 1200, 800, VIEW)
    expect(r.size).toBeCloseTo(800)
    expect(r.sx).toBeCloseTo(200)
    expect(r.sy).toBeCloseTo(0)
    // portrait
    const p = sourceRect(centered(600, 1000, VIEW), 600, 1000, VIEW)
    expect(p).toMatchObject({ size: expect.closeTo(600), sx: expect.closeTo(0), sy: expect.closeTo(200) })
  })

  it('never lets the window show past the picture, whatever the drag', () => {
    const c = clampCrop({ zoom: 1, x: 500, y: 500 }, 1200, 800, VIEW)
    expect(c.x).toBe(0)
    expect(c.y).toBe(0)
    const far = clampCrop({ zoom: 1, x: -5000, y: -5000 }, 1200, 800, VIEW)
    expect(far.x).toBeCloseTo(VIEW - 1200 * 0.35)
    expect(far.y).toBeCloseTo(0) // fits exactly: nothing to scroll along the short side at zoom 1
  })

  it('keeps the zoom between 1 and the maximum', () => {
    expect(clampCrop({ zoom: 0.2, x: 0, y: 0 }, 800, 800, VIEW).zoom).toBe(1)
    expect(clampCrop({ zoom: 99, x: 0, y: 0 }, 800, 800, VIEW).zoom).toBe(MAX_ZOOM)
  })

  it('zooms about a point: the picture point under it stays under it', () => {
    const c0 = centered(1000, 1000, VIEW)
    const at = { x: 70, y: 210 }
    const under = (c: typeof c0) => ({ x: (at.x - c.x) / (baseScale(1000, 1000, VIEW) * c.zoom), y: (at.y - c.y) / (baseScale(1000, 1000, VIEW) * c.zoom) })
    const c1 = zoomAt(c0, 2, at.x, at.y, 1000, 1000, VIEW)
    expect(c1.zoom).toBe(2)
    expect(under(c1).x).toBeCloseTo(under(c0).x)
    expect(under(c1).y).toBeCloseTo(under(c0).y)
    // and stops at the edge: zooming out again lands on the whole square
    const back = zoomAt(c1, 0.1, at.x, at.y, 1000, 1000, VIEW)
    expect(back).toMatchObject({ zoom: 1, x: expect.closeTo(0), y: expect.closeTo(0) })
  })

  it('the sent square is the window: at 2x it is half the short side', () => {
    const c = zoomAt(centered(1000, 1000, VIEW), 2, VIEW / 2, VIEW / 2, 1000, 1000, VIEW)
    const r = sourceRect(c, 1000, 1000, VIEW)
    expect(r.size).toBeCloseTo(500)
    expect(r.sx).toBeCloseTo(250)
  })

  it('sends at most 512 px, and a small picture as it is', () => {
    expect(outputSize(3000)).toBe(512)
    expect(outputSize(320.4)).toBe(320)
    expect(outputSize(0.2)).toBe(1)
  })
})
