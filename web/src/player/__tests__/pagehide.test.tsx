import { afterEach, describe, expect, it, vi } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import type { MediaFile } from '../../api/types'

const stopHls = vi.fn(() => Promise.resolve())
vi.mock('../../api/client', async (orig) => ({
  ...(await orig<typeof import('../../api/client')>()),
  stopHls: (...a: unknown[]) => (stopHls as (...x: unknown[]) => Promise<void>)(...a),
  touchHls: vi.fn(() => new Promise(() => {})),
}))

vi.mock('hls.js', () => {
  class FakeHls {
    static Events = { ERROR: 'error' }
    static ErrorTypes = { NETWORK_ERROR: 'networkError' }
    static isSupported = () => true
    on() {}
    loadSource() {}
    attachMedia() {}
    destroy() {}
  }
  return { default: FakeHls }
})

import { usePlayback } from '../usePlayback'

afterEach(() => stopHls.mockClear())

describe('usePlayback pagehide', () => {
  it('stops the server session with keepalive when the page is closed or reloaded', async () => {
    const start = vi.fn(() =>
      Promise.resolve({
        mode: 'transcode', url: '/api/hls/abc123/index.m3u8', startSec: 0, durationMs: 60000,
        audioIndex: 0, audio: [], subtitles: [],
      }),
    )
    const video = document.createElement('video')
    video.play = () => Promise.resolve()
    renderHook(() =>
      usePlayback({
        videoRef: { current: video },
        parts: [{ id: 1, durationMs: 60000 } as MediaFile],
        startSec: 0,
        caps: { videoCodecs: ['h264'], audioCodecs: ['aac'], containers: ['mp4'] } as never,
        onFinished: () => {},
        start: start as never,
      }),
    )
    await waitFor(() => expect(start).toHaveBeenCalled())
    await new Promise((r) => setTimeout(r, 20))
    window.dispatchEvent(new Event('pagehide'))
    expect(stopHls).toHaveBeenCalledWith('abc123', true)
  })
})
