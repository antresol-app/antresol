import { describe, expect, it, vi } from 'vitest'
import { act, renderHook, waitFor } from '@testing-library/react'
import type { PlayRequest, PlayResponse } from '../../api/types'
import { mediaErrorReason, onlineExpired, ONLINE_REPLAY_WINDOW_MS, planSeek, recover, recoverOnline, repeatedEarlyEnd, waitVerdict } from '../recovery'

vi.mock('../../api/client', async (orig) => ({
  ...(await orig<typeof import('../../api/client')>()),
  touchHls: vi.fn(() => new Promise(() => {})),
  stopHls: vi.fn(() => Promise.resolve()),
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

import { touchHls } from '../../api/client'
import { onlineError, usePlayback } from '../usePlayback'

describe('recover', () => {
  it('restarts an expired session, then falls back, then gives up', () => {
    expect(recover({ restarts: 0, fallbackUsed: false }, 'expired')).toEqual({ action: 'restart' })
    expect(recover({ restarts: 1, fallbackUsed: false }, 'expired')).toEqual({ action: 'restart' })
    expect(recover({ restarts: 2, fallbackUsed: false }, 'expired')).toEqual({ action: 'fallback' })
    expect(recover({ restarts: 2, fallbackUsed: true }, 'expired')).toEqual({ action: 'error' })
  })

  it('retries a decoding failure once with transcode, never twice', () => {
    expect(recover({ restarts: 0, fallbackUsed: false }, 'media')).toEqual({ action: 'fallback' })
    expect(recover({ restarts: 0, fallbackUsed: true }, 'media')).toEqual({ action: 'error' })
  })

  it('treats a network error on HLS as an expired session', () => {
    expect(mediaErrorReason('transcode', 2)).toBe('expired')
    expect(mediaErrorReason('remux', 2)).toBe('expired')
    expect(mediaErrorReason('direct', 2)).toBe('media')
    expect(mediaErrorReason('remux', 4)).toBe('media')
  })
})

describe('planSeek', () => {
  const hls = { part: 0, mode: 'transcode', offset: 600, seekableEnd: 120 }

  it('seeks inside the transcoded range', () => {
    expect(planSeek(hls, 0, 650)).toEqual({ kind: 'local', time: 50 })
    expect(planSeek(hls, 0, 720.5)).toEqual({ kind: 'local', time: 120.5 })
  })

  it('restarts the session outside it (ahead or before the start)', () => {
    expect(planSeek(hls, 0, 900)).toEqual({ kind: 'reload', part: 0, at: 900 })
    expect(planSeek(hls, 0, 100)).toEqual({ kind: 'reload', part: 0, at: 100 })
  })

  it('direct play seeks anywhere; another part always reloads', () => {
    expect(planSeek({ part: 0, mode: 'direct', offset: 0, seekableEnd: 0 }, 0, 3000)).toEqual({ kind: 'local', time: 3000 })
    expect(planSeek({ part: 0, mode: 'direct', offset: 0, seekableEnd: 0 }, 1, 20)).toEqual({ kind: 'reload', part: 1, at: 20 })
  })
})

describe('repeatedEarlyEnd', () => {
  it('treats a second end at the same spot as the real end', () => {
    expect(repeatedEarlyEnd(null, 0, 3000)).toBe(false)
    expect(repeatedEarlyEnd({ part: 0, pos: 3000 }, 0, 3001.5)).toBe(true)
    expect(repeatedEarlyEnd({ part: 0, pos: 3000 }, 0, 3100)).toBe(false)
    expect(repeatedEarlyEnd({ part: 0, pos: 3000 }, 1, 3000)).toBe(false)
  })
})

describe('online playback', () => {
  it('online seek is local', () => {
    expect(planSeek({ part: 0, mode: 'online', offset: 0, seekableEnd: 0 }, 0, 3000)).toEqual({ kind: 'local', time: 3000 })
    expect(planSeek({ part: 0, mode: 'online', offset: 0, seekableEnd: 120 }, 0, 10)).toEqual({ kind: 'local', time: 10 })
  })

  it('a stale link is a 410 or the proxy header', () => {
    expect(onlineExpired(410)).toBe(true)
    expect(onlineExpired(200, 'expired')).toBe(true)
    expect(onlineExpired(502)).toBe(false)
    expect(onlineExpired(undefined, null)).toBe(false)
  })

  it('re-plays a stale link twice in 5 minutes, then gives up', () => {
    const t0 = 1_000_000
    const a = recoverOnline([], t0)
    expect(a).toEqual({ action: 'replay', replays: [t0] })
    const b = recoverOnline(a.action === 'replay' ? a.replays : [], t0 + 60_000)
    expect(b.action).toBe('replay')
    expect(recoverOnline(b.action === 'replay' ? b.replays : [], t0 + 120_000)).toEqual({ action: 'error' })
    // after 5 minutes the old re-plays no longer count
    expect(recoverOnline([t0, t0 + 60_000], t0 + 60_000 + ONLINE_REPLAY_WINDOW_MS + 1).action).toBe('replay')
  })

  it('a stall is a stale link; a slow first byte gets a while', () => {
    const c = { phase: 'stall' as const, mode: 'online', slow: false, progressed: false, idleChecks: 0, elapsedSec: 40 }
    expect(waitVerdict(c)).toBe('expired')
    expect(waitVerdict({ ...c, phase: 'start' })).toBe('wait')
    expect(waitVerdict({ ...c, phase: 'start', elapsedSec: 120 })).toBe('expired')
  })

  it('online expired replays twice then fails', async () => {
    vi.spyOn(HTMLMediaElement.prototype, 'play').mockImplementation(() => Promise.resolve())
    vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {})
    vi.spyOn(HTMLMediaElement.prototype, 'load').mockImplementation(() => {})
    const video = document.createElement('video')
    let n = 0
    const start = vi.fn(async (_f: unknown, _b: PlayRequest): Promise<PlayResponse> => {
      n++
      return {
        mode: 'online', kind: 'mp4', url: `/api/online/s/sid${n}/blob`, session: `sid${n}`, startSec: 0, audio: [], audioIndex: -1, subtitles: [],
        quality: { options: ['1080p', '720p'], current: '1080p' },
      }
    })
    const stop = vi.fn(() => Promise.resolve())
    const caps = { videoCodecs: ['h264'], audioCodecs: ['aac'], containers: ['mp4'] }
    const parts = [{ id: 0, name: 'Фильм', size: 0, audio: [], subs: [] }]
    const { result } = renderHook(() =>
      usePlayback({ videoRef: { current: video }, parts, startSec: 42, caps, onFinished: () => {}, start, online: { stop } }),
    )
    await waitFor(() => expect(start).toHaveBeenCalledTimes(1))
    await waitFor(() => expect(video.getAttribute('src')).toBe('/api/online/s/sid1/blob'))
    act(() => result.current.mediaProps.onCanPlay()) // it played
    const expire = async (calls: number) => {
      // the proxy's 410 on a range: the element reports a network error
      Object.defineProperty(video, 'error', { configurable: true, value: { code: 2 } })
      act(() => result.current.mediaProps.onError())
      await waitFor(() => expect(start).toHaveBeenCalledTimes(calls))
      await waitFor(() => expect(video.getAttribute('src')).toBe(`/api/online/s/sid${calls}/blob`))
    }
    await expire(2)
    expect(start.mock.calls[1]![1]).toMatchObject({ refresh: true, startSec: 42, replaceSession: 'sid1', quality: '1080p' })
    expect(result.current.refreshing).toBe(true)
    await expire(3)
    expect(start.mock.calls[2]![1]).toMatchObject({ refresh: true, startSec: 42, replaceSession: 'sid2' })
    // the third within 5 minutes: ask for another source
    Object.defineProperty(video, 'error', { configurable: true, value: { code: 2 } })
    act(() => result.current.mediaProps.onError())
    await waitFor(() => expect(result.current.error).toBe(onlineError()))
    expect(start).toHaveBeenCalledTimes(3)
    expect(stop).toHaveBeenCalledWith('sid3', false)
    // no heartbeat for an online session
    expect(touchHls).not.toHaveBeenCalled()
  })

  it('a source that never showed a frame gets one fresh try; a refused first MP4 request is a stale link', async () => {
    expect(recoverOnline([], 0, false).action).toBe('replay')
    expect(recoverOnline([0], 1000, false)).toEqual({ action: 'error' })
    expect(mediaErrorReason('online', 4, false, false)).toBe('expired')
    expect(mediaErrorReason('online', 4, false, true)).toBe('media')

    vi.spyOn(HTMLMediaElement.prototype, 'play').mockImplementation(() => Promise.resolve())
    vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {})
    vi.spyOn(HTMLMediaElement.prototype, 'load').mockImplementation(() => {})
    const video = document.createElement('video')
    let n = 0
    const start = vi.fn(async (_f: unknown, _b: PlayRequest): Promise<PlayResponse> => {
      n++
      return { mode: 'online', kind: 'mp4', url: `/api/online/s/sid${n}/blob`, session: `sid${n}`, startSec: 0, audio: [], audioIndex: -1, subtitles: [] }
    })
    const parts = [{ id: 0, name: 'Фильм', size: 0, audio: [], subs: [] }]
    const caps = { videoCodecs: ['h264'], audioCodecs: ['aac'], containers: ['mp4'] }
    const { result } = renderHook(() =>
      usePlayback({ videoRef: { current: video }, parts, startSec: 0, caps, onFinished: () => {}, start, online: { stop: () => Promise.resolve() } }),
    )
    await waitFor(() => expect(video.getAttribute('src')).toBe('/api/online/s/sid1/blob'))
    // Chrome: a 410 on the first request of video.src is MEDIA_ERR_SRC_NOT_SUPPORTED
    Object.defineProperty(video, 'error', { configurable: true, value: { code: 4 } })
    act(() => result.current.mediaProps.onError())
    await waitFor(() => expect(video.getAttribute('src')).toBe('/api/online/s/sid2/blob'))
    expect(start.mock.calls[1]![1]).toMatchObject({ refresh: true })
    act(() => result.current.mediaProps.onError())
    await waitFor(() => expect(result.current.error).toBe(onlineError()))
    expect(start).toHaveBeenCalledTimes(2)
  })
})
