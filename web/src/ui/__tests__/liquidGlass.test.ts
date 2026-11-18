// Liquid Glass's lens (ui/liquidGlass.ts): which engines are asked to bend the
// backdrop, and the map's shape — neutral in the middle, a push inward that
// grows towards the rim, the refracted band shown fully at the edge.
import { describe, expect, it } from 'vitest'
import { bendsBackdrop, displacement } from '../liquidGlass'

const nav = (userAgent: string, brands?: string[]) => ({ userAgent, userAgentData: brands && { brands: brands.map((brand) => ({ brand })) } }) as unknown as Navigator

describe('bendsBackdrop', () => {
  it('only Chromium (WebKit and Gecko accept url() in backdrop-filter and draw nothing)', () => {
    expect(bendsBackdrop(nav('Mozilla/5.0 (Linux; Android 15) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0 Mobile Safari/537.36'))).toBe(true)
    expect(bendsBackdrop(nav('x', ['Chromium', 'Google Chrome']))).toBe(true)
    expect(bendsBackdrop(nav('Mozilla/5.0 (iPhone; CPU iPhone OS 26_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.0 Mobile/15E148 Safari/604.1'))).toBe(false)
    // Chrome on iOS is WebKit
    expect(bendsBackdrop(nav('Mozilla/5.0 (iPhone; CPU iPhone OS 26_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/141.0 Mobile/15E148 Safari/604.1'))).toBe(false)
    expect(bendsBackdrop(nav('Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:145.0) Gecko/20100101 Firefox/145.0'))).toBe(false)
  })
})

describe('displacement', () => {
  const [w, h, r, bezel] = [300, 62, 28, 14]
  it('is neutral past the band', () => {
    expect(displacement(150, 31, w, h, r, bezel)).toEqual([0, 0, 0])
    expect(displacement(150, 15, w, h, r, bezel)).toEqual([0, 0, 0])
  })
  it('pushes inward, harder towards the rim, and shows the band there', () => {
    const top = displacement(150, 1, w, h, r, bezel)
    const nearTop = displacement(150, 8, w, h, r, bezel)
    expect(top[0]).toBeCloseTo(0)
    expect(top[1]).toBeGreaterThan(nearTop[1])
    expect(nearTop[1]).toBeGreaterThan(0)
    expect(displacement(150, 61, w, h, r, bezel)[1]).toBeLessThan(0)
    expect(displacement(1, 31, w, h, r, bezel)[0]).toBeGreaterThan(0)
    expect(displacement(299, 31, w, h, r, bezel)[0]).toBeLessThan(0)
    expect(top[2]).toBeGreaterThan(0.9)
  })
  it('follows the corner along its radius', () => {
    const [dx, dy] = displacement(14, 14, w, h, r, bezel)
    expect(dx).toBeGreaterThan(0)
    expect(dy).toBeGreaterThan(0)
    expect(dx).toBeCloseTo(dy, 5)
  })
})
