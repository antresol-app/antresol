import { afterEach, describe, expect, it, vi } from 'vitest'
import { placeFloating, safeInsets, type FloatInput } from '../floating'

const anchor = (top: number, left = 600, h = 36, w = 120) => ({ top, bottom: top + h, left, right: left + w, width: w, height: h })

const base = (over: Partial<FloatInput>): FloatInput => ({
  anchor: anchor(200),
  size: { width: 260, height: 300 },
  viewport: { width: 1440, height: 900 },
  ceiling: 0,
  floor: 900,
  placement: 'bottom-start',
  ...over,
})

describe('placeFloating', () => {
  it('opens below the trigger when it fits there', () => {
    const r = placeFloating(base({}))
    expect(r.side).toBe('bottom')
    expect(r.top).toBe(236 + 8)
    expect(r.left).toBe(600)
    expect(r.maxHeight).toBeGreaterThanOrEqual(300)
  })

  it('flips above when only the space above holds it', () => {
    const r = placeFloating(base({ anchor: anchor(700) }))
    expect(r.side).toBe('top')
    // its bottom edge sits the gap above the trigger
    expect(r.top + 300).toBe(700 - 8)
  })

  it('never goes above the ceiling (the top bar): the roomier side, shortened to scroll', () => {
    // a 600px menu, trigger low in a 900px window under a 76px bar
    const r = placeFloating(base({ anchor: anchor(500), size: { width: 260, height: 600 }, ceiling: 76 }))
    expect(r.side).toBe('top')
    expect(r.top).toBeGreaterThanOrEqual(76 + 8)
    expect(r.top + r.maxHeight).toBeLessThanOrEqual(500 - 8)
    expect(r.maxHeight).toBeLessThan(600)
  })

  it('keeps above the floor (the tab bar)', () => {
    const r = placeFloating(base({ anchor: anchor(300), size: { width: 260, height: 500 }, floor: 700 }))
    // 300 above vs 700-336 below: below is roomier, cut to it
    expect(r.side).toBe('bottom')
    expect(r.top + r.maxHeight).toBeLessThanOrEqual(700 - 8)
  })

  it('end placement aligns the right edges', () => {
    const r = placeFloating(base({ placement: 'bottom-end' }))
    expect(r.left + 260).toBe(720)
  })

  it('clamps horizontally inside the window', () => {
    expect(placeFloating(base({ anchor: anchor(200, 1400) })).left).toBe(1440 - 8 - 260)
    expect(placeFloating(base({ anchor: anchor(200, -40), placement: 'bottom-end', size: { width: 260, height: 100 } })).left).toBe(8)
  })

  it('stays out of the notch and the rounded corners of a phone on its side (the safe area)', () => {
    // a trigger at the right edge of an 844-wide landscape phone, 59 pt insets
    const r = placeFloating(base({ anchor: anchor(100, 760, 36, 40), viewport: { width: 844, height: 390 }, floor: 390, placement: 'bottom-start', insets: { left: 59, right: 59 } }))
    expect(r.left + 260).toBeLessThanOrEqual(844 - 59)
    const l = placeFloating(base({ anchor: anchor(100, 10, 36, 40), viewport: { width: 844, height: 390 }, floor: 390, placement: 'bottom-end', insets: { left: 59, right: 59 } }))
    expect(l.left).toBeGreaterThanOrEqual(59)
  })

  it('a top placement opens above when it fits, else below', () => {
    expect(placeFloating(base({ anchor: anchor(700), placement: 'top-end' })).side).toBe('top')
    expect(placeFloating(base({ anchor: anchor(100), placement: 'top-end' })).side).toBe('bottom')
  })

  it('caps the height at the cap even with room', () => {
    const r = placeFloating(base({ size: { width: 260, height: 900 }, viewport: { width: 1440, height: 2000 }, floor: 2000, cap: 560 }))
    expect(r.maxHeight).toBe(560)
  })
})

describe('safeInsets', () => {
  afterEach(() => vi.restoreAllMocks())
  const real = window.getComputedStyle.bind(window)

  it('reads the --safe-* tokens where the browser resolves env() in them (Chromium)', () => {
    vi.spyOn(window, 'getComputedStyle').mockImplementation((el: Element) => {
      if (el === document.documentElement)
        return { getPropertyValue: (n: string) => ({ '--safe-top': '59px', '--safe-right': '0px', '--safe-bottom': '34px', '--safe-left': '0px' })[n] ?? '' } as CSSStyleDeclaration
      return real(el)
    })
    expect(safeInsets()).toEqual({ top: 59, right: 0, bottom: 34, left: 0 })
  })

  it('falls back to a probe’s padding where a custom property keeps the raw env() (WebKit)', () => {
    vi.spyOn(window, 'getComputedStyle').mockImplementation((el: Element) => {
      if (el === document.documentElement) return { getPropertyValue: (n: string) => (n.startsWith('--safe-') ? 'env(safe-area-inset-top)' : '') } as CSSStyleDeclaration
      if ((el as HTMLElement).dataset?.safeAreaProbe !== undefined)
        return { paddingTop: '47px', paddingRight: '47px', paddingBottom: '21px', paddingLeft: '47px' } as CSSStyleDeclaration
      return real(el)
    })
    expect(safeInsets()).toEqual({ top: 47, right: 47, bottom: 21, left: 47 })
  })
})
