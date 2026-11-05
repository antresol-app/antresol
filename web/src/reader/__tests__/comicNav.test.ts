import { describe, expect, it } from 'vitest'
import { nextPageKey, tapZone } from '../comicNav'

describe('comicNav', () => {
  it('rtl ArrowLeft goes forward', () => {
    expect(nextPageKey('ArrowLeft', 'rtl')).toBe(1)
    expect(nextPageKey('ArrowRight', 'rtl')).toBe(-1)
    expect(nextPageKey(' ', 'rtl')).toBe(1)
    expect(nextPageKey('PageDown', 'rtl')).toBe(1)
    expect(nextPageKey('PageUp', 'rtl')).toBe(-1)
    expect(nextPageKey('a', 'rtl')).toBe(0)
  })

  it('ltr ArrowRight goes forward', () => {
    expect(nextPageKey('ArrowRight', 'ltr')).toBe(1)
    expect(nextPageKey('ArrowLeft', 'ltr')).toBe(-1)
    expect(nextPageKey(' ', 'ltr')).toBe(1)
    expect(nextPageKey('PageDown', 'ltr')).toBe(1)
    expect(nextPageKey('Enter', 'ltr')).toBe(0)
  })

  it('tap zones by thirds respect direction', () => {
    // right to left: the left third turns forward
    expect(tapZone(10, 900, 'rtl')).toBe(1)
    expect(tapZone(890, 900, 'rtl')).toBe(-1)
    expect(tapZone(450, 900, 'rtl')).toBe(0)
    // left to right: the right third turns forward
    expect(tapZone(10, 900, 'ltr')).toBe(-1)
    expect(tapZone(890, 900, 'ltr')).toBe(1)
    expect(tapZone(450, 900, 'ltr')).toBe(0)
    // the borders belong to the middle
    expect(tapZone(300, 900, 'ltr')).toBe(0)
    expect(tapZone(600, 900, 'ltr')).toBe(0)
  })
})
