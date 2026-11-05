import { describe, expect, it } from 'vitest'
import { isDarkGreyLogo } from '../useTrimmedImage'

/** `n` opaque pixels of one colour, plus transparent ones that must not count. */
const px = (rgb: [number, number, number], n = 4) => [...Array.from({ length: n }, () => [...rgb, 255]).flat(), 255, 255, 255, 0]

describe('isDarkGreyLogo', () => {
  it('turns black and dark grey type white', () => {
    expect(isDarkGreyLogo(px([0, 0, 0]))).toBe(true)
    expect(isDarkGreyLogo(px([40, 40, 44]))).toBe(true)
    expect(isDarkGreyLogo(px([6, 0, 2]))).toBe(true) // near-black: its hue is noise
  })
  it('keeps a dark coloured mark and a light logo as they are', () => {
    expect(isDarkGreyLogo(px([110, 8, 12]))).toBe(false) // deep red
    expect(isDarkGreyLogo(px([14, 30, 90]))).toBe(false) // navy
    expect(isDarkGreyLogo(px([240, 240, 240]))).toBe(false)
    expect(isDarkGreyLogo([0, 0, 0, 0])).toBe(false)
  })
})
