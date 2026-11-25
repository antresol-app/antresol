import { describe, expect, it } from 'vitest'
import { canvasScale, MAX_CANVAS_PIXELS } from '../pdfScale'

const a4 = { width: 595, height: 842 }

describe('canvasScale', () => {
  it('is the width over the page width times the device ratio while the canvas is small', () => {
    expect(canvasScale(a4, 880, 2)).toBeCloseTo((880 / 595) * 2)
  })

  it('caps the canvas at the pixel budget when zoomed in on a dense screen', () => {
    const scale = canvasScale(a4, 2400, 3)
    expect(scale).toBeLessThan((2400 / 595) * 3)
    const pixels = a4.width * scale * (a4.height * scale)
    expect(pixels).toBeLessThanOrEqual(MAX_CANVAS_PIXELS * 1.0001)
    expect(pixels).toBeGreaterThan(MAX_CANVAS_PIXELS * 0.99)
  })
})
