import { afterEach, describe, expect, it, vi } from 'vitest'
import { mseSupported, nativeHls } from '../hlsSupport'

const SAFARI = 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.0 Safari/605.1.15'
const CHROME = 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36'

const video = (hls: boolean) => ({ canPlayType: (t: string) => (hls && t === 'application/vnd.apple.mpegurl' ? 'maybe' : '') }) as HTMLVideoElement
const ua = (s: string) => vi.spyOn(navigator, 'userAgent', 'get').mockReturnValue(s)
const mse = (ok: boolean) => vi.stubGlobal('MediaSource', { isTypeSupported: () => ok })

describe('which HLS player', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it('MSE with H.264/AAC is what hls.js needs', () => {
    mse(true)
    expect(mseSupported()).toBe(true)
    mse(false)
    expect(mseSupported()).toBe(false)
    vi.stubGlobal('MediaSource', undefined)
    expect(mseSupported()).toBe(false)
  })

  it('Safari plays HLS itself even with MSE: hls.js is never loaded there', () => {
    ua(SAFARI)
    mse(true)
    expect(nativeHls(video(true))).toBe(true)
  })

  it('Chrome with MSE takes hls.js, even where it could play HLS itself', () => {
    ua(CHROME)
    mse(true)
    expect(nativeHls(video(true))).toBe(false)
    expect(nativeHls(video(false))).toBe(false)
  })

  it('a browser with native HLS and no MSE (an old iPhone) plays it itself', () => {
    ua(CHROME)
    mse(false)
    expect(nativeHls(video(true))).toBe(true)
  })
})
