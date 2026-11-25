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

describe('usePlayback: parts whose length item info does not know', () => {
  // two parts of 14 s each; `known` is what the item info says of them (0: the probe has not reached the file)
  function mount(known: [number, number], startSec: number) {
    vi.spyOn(HTMLMediaElement.prototype, 'play').mockResolvedValue(undefined)
    vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {})
    vi.spyOn(HTMLMediaElement.prototype, 'load').mockImplementation(() => {})
    const start = vi.fn(async (file: MediaFile) => ({
      mode: 'direct', url: `/media/${file.id}`, startSec: 0, durationMs: 14_000, audioIndex: 1, audio: [], subtitles: [],
    }))
    const video = document.createElement('video')
    const parts = [10, 11].map((id, i) => ({ id, name: `Фильм cd${i + 1}`, size: 0, durationMs: known[i], audio: [], subs: [] }) as MediaFile)
    const hook = renderHook(() =>
      usePlayback({
        videoRef: { current: video },
        parts,
        startSec,
        caps: { videoCodecs: ['h264'], audioCodecs: ['aac'], containers: ['mp4'] } as never,
        onFinished: () => {},
        start: start as never,
      }),
    )
    return { ...hook, start, video }
  }
  /** What each /api/play asked: the file and where in it. */
  const asked = (start: ReturnType<typeof mount>['start']) => start.mock.calls.map((c) => [c[0].id, (c as unknown as [unknown, { startSec: number }])[1].startSec])

  it('a start that lies in part 2 is found from the lengths the answers give, not from zeros', async () => {
    const { result, start, video } = mount([0, 0], 25)
    await waitFor(() => expect(video.getAttribute('src')).toBe('/media/11'))
    // part 1 was asked first (its length is what tells where 25 s is), then part 2 at 25 - 14
    expect(asked(start)).toEqual([[10, 0], [11, 11]])
    expect(result.current.part).toBe(1)
    expect(result.current.total).toBe(28)
    expect(result.current.durRef.current).toEqual([14, 14])
  })

  it('a start at the beginning stays in part 1 (all zeros do not mean the last part)', async () => {
    const { result, start, video } = mount([0, 0], 0)
    await waitFor(() => expect(video.getAttribute('src')).toBe('/media/10'))
    expect(asked(start)).toEqual([[10, 0]])
    expect(result.current.part).toBe(0)
  })

  it('a start inside part 1 stays there once its answer gives the length', async () => {
    const { result, start, video } = mount([0, 0], 5)
    await waitFor(() => expect(video.getAttribute('src')).toBe('/media/10'))
    await waitFor(() => expect(asked(start)).toEqual([[10, 0], [10, 5]]))
    expect(result.current.part).toBe(0)
    expect(result.current.durRef.current).toEqual([14, 0])
  })

  it('lengths item info knows are used as they are: one answer, no extra ask', async () => {
    const { result, start, video } = mount([14_000, 14_000], 25)
    await waitFor(() => expect(video.getAttribute('src')).toBe('/media/11'))
    expect(asked(start)).toEqual([[11, 11]])
    expect(result.current.part).toBe(1)
  })

  it('a length item info has wrong is corrected by the answer for that part', async () => {
    // item info says 20 s for a 14 s part: 16 s is 2 s into part 2, not 16 s into part 1
    const { result, start, video } = mount([20_000, 14_000], 16)
    await waitFor(() => expect(video.getAttribute('src')).toBe('/media/11'))
    expect(asked(start)).toEqual([[10, 16], [11, 2]])
    expect(result.current.part).toBe(1)
    expect(result.current.durRef.current).toEqual([14, 14])
  })
})
