import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import type { MediaFile, SubTrack } from '../../api/types'
import { forgetMediaKey, hasMediaKey, KEY_MARGIN_MS, mediaKey, withMediaKey } from '../airplay'
import { langKey } from '../timeline'
import { session } from '../../state/session'

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

import { VideoPlayer } from '../VideoPlayer'

const KEY = 'v1.3.1.1999999999.abcdefghijklmnopqrstuv' // gitleaks:allow
const json = (v: unknown, status = 200) => new Response(JSON.stringify(v), { status, headers: { 'Content-Type': 'application/json' } })

let keyReply: () => Response
let keyCalls = 0

beforeEach(() => {
  forgetMediaKey()
  keyCalls = 0
  keyReply = () => json({ key: KEY, expiresAt: new Date(Date.now() + 12 * 3600_000).toISOString() })
  vi.spyOn(HTMLMediaElement.prototype, 'play').mockImplementation(() => Promise.resolve())
  vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {})
  vi.spyOn(HTMLMediaElement.prototype, 'load').mockImplementation(() => {})
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (url.includes('/api/auth/media-key')) {
        keyCalls++
        return keyReply()
      }
      if (url.includes('/api/login')) return json({ id: 1, name: 'Папа', isAdmin: true, isKid: false, avatar: 'grad-1' })
      if (url.includes('/api/logout')) return new Response(null, { status: 204 })
      return json({})
    }),
  )
})
afterEach(() => {
  session.reset()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
  delete (window as unknown as Record<string, unknown>).WebKitPlaybackTargetAvailabilityEvent
  try {
    localStorage.clear()
  } catch {
    /* ignore */
  }
})

/** Makes the page look like Safari (the event class exists). */
const safari = () => vi.stubGlobal('WebKitPlaybackTargetAvailabilityEvent', class {})

function fire(el: HTMLElement, type: string, extra: Record<string, unknown> = {}) {
  const e = Object.assign(new Event(type), extra)
  act(() => {
    el.dispatchEvent(e)
  })
}

/** Safari routes the element to a receiver (or back). */
function route(video: HTMLVideoElement, wireless: boolean) {
  Object.defineProperty(video, 'webkitCurrentPlaybackTargetIsWireless', { value: wireless, configurable: true })
  fire(video, 'webkitcurrentplaybacktargetiswirelesschanged')
}

const RU_SUB: SubTrack = { index: 2, codec: 'subrip', lang: 'ru', title: '', forced: false, default: false, external: false, burnIn: false, url: '/api/subs/5/2.vtt' }

function mount(subtitles: SubTrack[] = []) {
  const start = vi.fn(async () => ({ mode: 'direct', url: '/api/stream/5?x=1', startSec: 0, durationMs: 6_000_000, audioIndex: 0, audio: [], subtitles }))
  const r = render(
    <VideoPlayer itemId={9} title="Дюна" parts={[{ id: 5, name: 'dune.mkv', durationMs: 6_000_000 } as MediaFile]} startSec={120} onClose={() => {}} start={start as never} />,
  )
  const video = r.container.querySelector('video')!
  return { start, video }
}

describe('video AirPlay', () => {
  it('noButtonOutsideSafari', async () => {
    const { start, video } = mount()
    await waitFor(() => expect(start).toHaveBeenCalled())
    expect(video).toHaveAttribute('x-webkit-airplay', 'allow')
    expect(video).not.toHaveAttribute('disableremoteplayback')
    fire(video, 'webkitplaybacktargetavailabilitychanged', { availability: 'available' })
    expect(screen.queryByRole('button', { name: 'AirPlay' })).not.toBeInTheDocument()
  })

  it('buttonOpensThePickerWhileAReceiverIsAround', async () => {
    safari()
    const { start, video } = mount()
    await waitFor(() => expect(start).toHaveBeenCalled())
    const picker = vi.fn()
    ;(video as HTMLVideoElement & { webkitShowPlaybackTargetPicker?: () => void }).webkitShowPlaybackTargetPicker = picker
    fire(video, 'webkitplaybacktargetavailabilitychanged', { availability: 'available' })
    fireEvent.click(await screen.findByRole('button', { name: 'AirPlay' }))
    expect(picker).toHaveBeenCalledTimes(1)
    fire(video, 'webkitplaybacktargetavailabilitychanged', { availability: 'not-available' })
    await waitFor(() => expect(screen.queryByRole('button', { name: 'AirPlay' })).not.toBeInTheDocument())
  })

  it('goingToAReceiverReloadsWithAMediaKeyAtTheSamePlace', async () => {
    safari()
    const { start, video } = mount()
    await waitFor(() => expect(video.getAttribute('src')).toBe('/api/stream/5?x=1'))
    expect(keyCalls).toBe(0) // nothing asked while it plays here
    fire(video, 'webkitplaybacktargetavailabilitychanged', { availability: 'available' })
    route(video, true)
    expect(screen.getByRole('button', { name: 'AirPlay' })).toHaveAttribute('aria-pressed', 'true')
    await waitFor(() => expect(start).toHaveBeenCalledTimes(2))
    expect((start.mock.calls[1] as unknown as [MediaFile, { startSec: number }])[1].startSec).toBe(120)
    await waitFor(() => expect(video.getAttribute('src')).toBe(`/api/stream/5?x=1&mk=${encodeURIComponent(KEY)}`))
    expect(keyCalls).toBe(1)
    // coming back keeps the keyed stream: no second reload
    route(video, false)
    await new Promise((r) => setTimeout(r, 20))
    expect(start).toHaveBeenCalledTimes(2)
    expect(screen.getByRole('button', { name: 'AirPlay' })).toHaveAttribute('aria-pressed', 'false')
  })

  it('subtitlesOnAirPlayGetANote', async () => {
    safari()
    localStorage.setItem('vault.player.subLang', langKey('ru'))
    const { start, video } = mount([RU_SUB])
    await waitFor(() => expect(start).toHaveBeenCalled())
    expect(screen.queryByText('Субтитры на AirPlay не показываются')).not.toBeInTheDocument()
    route(video, true)
    expect(await screen.findByText('Субтитры на AirPlay не показываются')).toBeInTheDocument()
    route(video, false)
    await waitFor(() => expect(screen.queryByText('Субтитры на AirPlay не показываются')).not.toBeInTheDocument())
  })

  it('aFailureOnTheReceiverAsksForANewKey', async () => {
    safari()
    const { start, video } = mount()
    await waitFor(() => expect(start).toHaveBeenCalled())
    route(video, true)
    await waitFor(() => expect(hasMediaKey(video.getAttribute('src') ?? '')).toBe(true))
    expect(keyCalls).toBe(1)
    // the receiver was refused (the browser's session ended elsewhere): the restart asks again
    Object.defineProperty(video, 'error', { value: { code: 2 }, configurable: true })
    fire(video, 'error')
    await waitFor(() => expect(start).toHaveBeenCalledTimes(3))
    await waitFor(() => expect(keyCalls).toBe(2))
  })

  it('noKeySaysSoAndKeepsTheStream', async () => {
    safari()
    keyReply = () => json({ error: 'Нужно войти' }, 401)
    const { start, video } = mount()
    await waitFor(() => expect(start).toHaveBeenCalled())
    route(video, true)
    expect(await screen.findByText(/AirPlay не получил доступ к видео/)).toBeInTheDocument()
    expect(hasMediaKey(video.getAttribute('src') ?? '')).toBe(false)
  })
})

describe('media key URLs', () => {
  it('withMediaKeyKeepsTheQueryAndReplacesAnOldKey', () => {
    expect(withMediaKey('/api/hls/abc/index.m3u8', 'k+1')).toBe('/api/hls/abc/index.m3u8?mk=k%2B1')
    expect(withMediaKey('/api/stream/5?t=3&mk=old', 'new')).toBe('/api/stream/5?t=3&mk=new')
    expect(withMediaKey('https://h/api/online/s/x/v.m3u8?q=1#f', 'k')).toBe('https://h/api/online/s/x/v.m3u8?q=1&mk=k#f')
    expect(hasMediaKey('/a?mk=1')).toBe(true)
    expect(hasMediaKey('/a?xmk=1')).toBe(false)
  })

  it('mediaKeyIsSharedAndRenewedBeforeItEnds', async () => {
    const now = Date.now()
    keyReply = () => json({ key: `k${keyCalls}`, expiresAt: new Date(now + 2 * KEY_MARGIN_MS).toISOString() })
    const [a, b] = await Promise.all([mediaKey(now), mediaKey(now)])
    expect([a, b, keyCalls]).toEqual(['k1', 'k1', 1])
    expect(await mediaKey(now + KEY_MARGIN_MS / 2)).toBe('k1')
    // less than an hour left: a new one
    expect(await mediaKey(now + KEY_MARGIN_MS + 1)).toBe('k2')
  })

  it('signingOutForgetsTheKey', async () => {
    keyReply = () => json({ key: `k${keyCalls}`, expiresAt: new Date(Date.now() + 4 * 3600_000).toISOString() })
    await session.login(1)
    expect(await mediaKey()).toBe('k1')
    expect(await mediaKey()).toBe('k1')
    await session.logout()
    await session.login(1)
    expect(await mediaKey()).toBe('k2')
  })
})
