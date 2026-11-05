import { afterEach, describe, expect, it, vi } from 'vitest'
import { renderHook } from '@testing-library/react'
import { detectCaps, SAFE_CAPS, useCaps, withoutCodecs } from '../useCaps'

type MS = { isTypeSupported: (t: string) => boolean }

function mockMediaSource(supported: (t: string) => boolean) {
  const ms: MS = { isTypeSupported: supported }
  vi.stubGlobal('MediaSource', ms)
}

function mockCanPlay(fn: (t: string) => CanPlayTypeResult) {
  vi.spyOn(HTMLMediaElement.prototype, 'canPlayType').mockImplementation(fn)
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('useCaps', () => {
  it('leaves out HEVC when MediaSource cannot decode it', () => {
    mockMediaSource((t) => !/hvc1|hev1/.test(t))
    mockCanPlay(() => 'probably')
    const { result } = renderHook(() => useCaps())
    expect(result.current.videoCodecs).toContain('h264')
    expect(result.current.videoCodecs).not.toContain('hevc')
    expect(result.current.audioCodecs).toEqual(expect.arrayContaining(['aac', 'ac3', 'eac3', 'opus', 'flac']))
    expect(result.current.containers).toEqual(expect.arrayContaining(['mp4', 'webm']))
  })

  it('never reports MKV, even when the browser says it can play it', () => {
    mockMediaSource(() => true)
    mockCanPlay(() => 'probably')
    const caps = detectCaps()
    expect(caps.containers).not.toContain('mkv')
    expect(caps.videoCodecs).toEqual(['h264', 'hevc', 'av1', 'vp9'])
  })

  it('requires both MediaSource and the video element to agree', () => {
    // MSE says yes, but direct playback through <video src> would fail: not supported.
    mockMediaSource(() => true)
    mockCanPlay((t) => (/ac-3|ec-3/.test(t) ? '' : 'maybe'))
    const caps = detectCaps()
    expect(caps.audioCodecs).not.toContain('ac3')
    expect(caps.audioCodecs).not.toContain('eac3')
    expect(caps.audioCodecs).toContain('aac')
  })

  it('without MediaSource (iPhone native HLS) trusts canPlayType', () => {
    vi.stubGlobal('MediaSource', undefined)
    vi.stubGlobal('ManagedMediaSource', undefined)
    mockCanPlay((t) => (/hvc1/.test(t) || /avc1/.test(t) || /mp4a/.test(t) ? 'probably' : ''))
    const caps = detectCaps()
    expect(caps.videoCodecs).toEqual(['h264', 'hevc'])
    expect(caps.audioCodecs).toEqual(['aac'])
  })

  it('reports nothing it cannot play at all', () => {
    mockMediaSource(() => false)
    mockCanPlay(() => '')
    const caps = detectCaps()
    expect(caps.videoCodecs).toEqual([])
    expect(caps.audioCodecs).toEqual([])
    expect(caps.containers).toEqual([])
  })

  it('withoutCodecs removes a failing codec and falls back to safe caps when nothing is left', () => {
    const caps = { videoCodecs: ['h264', 'hevc'], audioCodecs: ['aac', 'ac3'], containers: ['mp4'] }
    expect(withoutCodecs(caps, ['hevc', 'ac3'])).toEqual({ videoCodecs: ['h264'], audioCodecs: ['aac'], containers: [] })
    expect(withoutCodecs(caps, ['h264', 'hevc']).videoCodecs).toEqual(SAFE_CAPS.videoCodecs)
  })
})
