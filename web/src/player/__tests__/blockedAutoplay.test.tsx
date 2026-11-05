import { afterEach, describe, expect, it, vi } from 'vitest'
import { act, renderHook, waitFor } from '@testing-library/react'
import type { MediaFile } from '../../api/types'

vi.mock('../../api/client', async (orig) => ({
  ...(await orig<typeof import('../../api/client')>()),
  stopHls: vi.fn(() => Promise.resolve()),
  touchHls: vi.fn(() => new Promise(() => {})),
}))

vi.mock('hls.js', () => {
  class FakeHls {
    static Events = { ERROR: 'error' }
    static ErrorTypes = { NETWORK_ERROR: 'networkError' }
    static isSupported = () => false
    on() {}
    loadSource() {}
    attachMedia() {}
    destroy() {}
  }
  return { default: FakeHls }
})

import { usePlayback } from '../usePlayback'

afterEach(() => vi.restoreAllMocks())

// iOS Safari blocks the first play() (it runs after an await, the tap is
// gone) and WebKit then fires «pause». That pause is not the viewer's: a tap
// on «Смотреть» minutes later must play this session, not reload it (a
// reload's play() runs after an await again and is blocked again).
describe('usePlayback: a blocked autoplay on native HLS', () => {
  it('a late tap plays the session instead of reloading it', async () => {
    vi.spyOn(navigator, 'userAgent', 'get').mockReturnValue(
      'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1',
    )
    const start = vi.fn(() =>
      Promise.resolve({
        mode: 'remux', url: '/api/hls/abc123/index.m3u8', startSec: 0, durationMs: 9_000_000,
        audioIndex: 1, audio: [], subtitles: [],
      }),
    )
    const video = document.createElement('video')
    video.canPlayType = () => 'maybe'
    let gesture = false
    let paused = true
    Object.defineProperty(video, 'paused', { get: () => paused })
    video.play = () => (gesture ? ((paused = false), Promise.resolve()) : Promise.reject(new DOMException('blocked', 'NotAllowedError')))
    const { result } = renderHook(() =>
      usePlayback({
        videoRef: { current: video },
        parts: [{ id: 0, durationMs: 9_000_000 } as MediaFile],
        startSec: 0,
        caps: { videoCodecs: ['h264'], audioCodecs: ['aac'], containers: ['mp4'] } as never,
        onFinished: () => {},
        start: start as never,
        slowSource: true,
      }),
    )
    await waitFor(() => expect(result.current.needsTap).toBe(true))
    // WebKit: play, then pause when the autoplay is refused
    act(() => {
      result.current.mediaProps.onPlay()
      result.current.mediaProps.onPause()
    })
    const now = Date.now()
    vi.spyOn(Date, 'now').mockReturnValue(now + 5 * 60_000)
    gesture = true
    act(() => result.current.toggle())
    act(() => result.current.mediaProps.onPlay())
    await waitFor(() => expect(result.current.needsTap).toBe(false))
    await new Promise((r) => setTimeout(r, 20))
    expect(start).toHaveBeenCalledTimes(1)
  })
})
