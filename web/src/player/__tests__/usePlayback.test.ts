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

// the hls.js instances made, with what they were given
const hlsMade = vi.hoisted(() => [] as { src: string; config: Record<string, unknown>; emit: (data: unknown) => void }[])
vi.mock('hls.js', () => {
  class FakeHls {
    static Events = { ERROR: 'error' }
    static ErrorTypes = { NETWORK_ERROR: 'networkError' }
    static isSupported = () => false
    private rec: (typeof hlsMade)[number]
    constructor(config: Record<string, unknown>) {
      this.rec = { src: '', config, emit: () => {} }
      hlsMade.push(this.rec)
    }
    on(_ev: string, f: (e: unknown, data: unknown) => void) {
      this.rec.emit = (data) => f('error', data)
    }
    loadSource(src: string) {
      this.rec.src = src
    }
    attachMedia() {}
    destroy() {}
  }
  return { default: FakeHls }
})

import { touchHls } from '../../api/client'
import { finalError, streamError, usePlayback } from '../usePlayback'

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

describe('usePlayback: a stream', () => {
  it('stream mode plays hls directly and treats 410 as stale', async () => {
    hlsMade.length = 0
    vi.mocked(touchHls).mockClear()
    vi.spyOn(HTMLMediaElement.prototype, 'play').mockResolvedValue(undefined)
    vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {})
    vi.spyOn(HTMLMediaElement.prototype, 'load').mockImplementation(() => {})
    let n = 0
    // an addon's stream: its own proxy URL, played as it is (no core HLS session)
    const start = vi.fn(async () => {
      n++
      return {
        mode: 'stream', kind: 'hls', url: `/api/addons/a/x/s/sid${n}/index.m3u8`, session: `sid${n}`, startSec: 0,
        audio: [], audioIndex: -1, subtitles: [], quality: { options: ['1080p', '720p'], current: '720p' },
      }
    })
    const stop = vi.fn(() => Promise.resolve())
    const video = document.createElement('video')
    const { result } = renderHook(() =>
      usePlayback({
        videoRef: { current: video },
        parts: [{ id: 0, name: 'Фильм', size: 0, audio: [], subs: [] } as MediaFile],
        startSec: 30,
        caps: { videoCodecs: ['h264'], audioCodecs: ['aac'], containers: ['mp4'] } as never,
        onFinished: () => {},
        start: start as never,
        stream: { stop, initialQuality: '720p' },
      }),
    )
    await waitFor(() => expect(hlsMade.length).toBe(1))
    expect(hlsMade[0]!.src).toBe('/api/addons/a/x/s/sid1/index.m3u8')
    // the whole video from 0: hls.js starts at the position itself
    expect(hlsMade[0]!.config).toMatchObject({ startPosition: 30, startFragPrefetch: true })
    expect(start.mock.calls[0]).toMatchObject([{}, { quality: '720p', startSec: 30 }])
    expect(result.current.mode).toBe('stream')
    expect(result.current.stream).toBe(true)
    expect(result.current.streamQuality).toEqual({ options: ['1080p', '720p'], current: '720p' })
    act(() => result.current.mediaProps.onCanPlay())
    // a 410 on a segment, not even fatal: a stale link, played again fresh
    act(() => hlsMade[0]!.emit({ fatal: false, type: 'networkError', response: { code: 410 } }))
    await waitFor(() => expect(start).toHaveBeenCalledTimes(2))
    expect(start.mock.calls[1]).toMatchObject([{}, { refresh: true, replaceSession: 'sid1' }])
    await waitFor(() => expect(hlsMade.length).toBe(2))
    // a 404 that is not fatal is hls.js's to retry
    act(() => hlsMade[1]!.emit({ fatal: false, type: 'networkError', response: { code: 404 } }))
    expect(start).toHaveBeenCalledTimes(2)
    act(() => hlsMade[1]!.emit({ fatal: false, type: 'networkError', response: { code: 410 } }))
    await waitFor(() => expect(start).toHaveBeenCalledTimes(3))
    await waitFor(() => expect(hlsMade.length).toBe(3))
    act(() => hlsMade[2]!.emit({ fatal: false, type: 'networkError', response: { code: 410 } }))
    await waitFor(() => expect(result.current.error).toBe(streamError()))
    expect(streamError()).toBe('Источник не отвечает')
    expect(stop).toHaveBeenCalledWith('sid3', false)
    expect(touchHls).not.toHaveBeenCalled()
  })

  it("an older server's answer (mode «online») still plays as a stream", async () => {
    hlsMade.length = 0
    vi.spyOn(HTMLMediaElement.prototype, 'play').mockResolvedValue(undefined)
    vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {})
    vi.spyOn(HTMLMediaElement.prototype, 'load').mockImplementation(() => {})
    const start = vi.fn(async () => ({ mode: 'online', kind: 'mp4', url: '/s/1/blob', session: 's1', startSec: 0, audio: [], audioIndex: -1, subtitles: [] }))
    const video = document.createElement('video')
    const { result } = renderHook(() =>
      usePlayback({
        videoRef: { current: video },
        parts: [{ id: 0, name: 'Фильм', size: 0, audio: [], subs: [] } as MediaFile],
        startSec: 0,
        caps: { videoCodecs: ['h264'], audioCodecs: ['aac'], containers: ['mp4'] } as never,
        onFinished: () => {},
        start: start as never,
        stream: { stop: () => Promise.resolve() },
      }),
    )
    await waitFor(() => expect(video.getAttribute('src')).toBe('/s/1/blob'))
    expect(result.current.mode).toBe('stream')
    expect(hlsMade.length).toBe(0)
  })
})
