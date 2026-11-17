// usePlayback's last word on a slow source (an addon's input): a stream
// that keeps stalling ends in the core's own «Источник не отдаёт данные»;
// any other source keeps the plain playback error.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { act, renderHook, waitFor } from '@testing-library/react'
import type { MediaFile } from '../../api/types'
import { MAX_RESTARTS } from '../recovery'

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

import { finalError, usePlayback } from '../usePlayback'

afterEach(() => vi.restoreAllMocks())

async function failUntilError(slowSource: boolean) {
  vi.spyOn(HTMLMediaElement.prototype, 'play').mockResolvedValue(undefined)
  vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {})
  const start = vi.fn(() =>
    Promise.resolve({ mode: 'direct', url: '/media/1', startSec: 0, durationMs: 60_000, audioIndex: 1, audio: [], subtitles: [] }),
  )
  const video = document.createElement('video')
  // MEDIA_ERR_NETWORK: on a slow source the source, not the device
  Object.defineProperty(video, 'error', { get: () => ({ code: 2 }) })
  const { result } = renderHook(() =>
    usePlayback({
      videoRef: { current: video },
      parts: [{ id: 0, durationMs: 60_000 } as MediaFile],
      startSec: 0,
      caps: { videoCodecs: ['h264'], audioCodecs: ['aac'], containers: ['mp4'] } as never,
      onFinished: () => {},
      start: start as never,
      slowSource,
    }),
  )
  for (let n = 1; n <= MAX_RESTARTS + 2 && !result.current.error; n++) {
    await waitFor(() => expect(start.mock.calls.length).toBeGreaterThanOrEqual(n))
    await waitFor(() => expect(video.getAttribute('src')).toBeTruthy())
    act(() => result.current.mediaProps.onError())
  }
  await waitFor(() => expect(result.current.error).toBeTruthy())
  return result.current.error
}

describe('usePlayback: a slow source', () => {
  it("stalled is the source's on a slow source", async () => {
    expect(await failUntilError(true)).toBe('Источник не отдаёт данные')
  })

  it('another source: the plain playback error', async () => {
    expect(await failUntilError(false)).toBe(finalError())
  })
})
