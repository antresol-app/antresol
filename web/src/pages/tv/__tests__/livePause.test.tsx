// Pause on live in the real LivePlayer: the centre play/pause comes with
// the pointer and goes after four quiet seconds (it stays while paused); a
// short pause resumes where it stopped, a pause past our channels' minute
// resumes at the live edge; more than 10 s behind, «В эфир» replaces
// «Прямой эфир» and jumps to the edge — the same for an external channel.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router'

const live = { sync: 982 as number | null, live: true }

vi.mock('hls.js', () => {
  class FakeHls {
    static Events = { ERROR: 'hlsError', MANIFEST_PARSED: 'hlsManifestParsed', AUDIO_TRACKS_UPDATED: 'hlsAudioTracksUpdated', SUBTITLE_TRACKS_UPDATED: 'hlsSubtitleTracksUpdated' }
    static ErrorTypes = { NETWORK_ERROR: 'networkError', MEDIA_ERROR: 'mediaError' }
    static isSupported = () => true
    audioTracks = []
    subtitleTracks = []
    audioTrack = 0
    subtitleTrack = -1
    subtitleDisplay = false
    get liveSyncPosition() {
      return live.sync
    }
    get latestLevelDetails() {
      return { live: live.live }
    }
    on() {}
    loadSource() {}
    stopLoad() {}
    attachMedia() {}
    recoverMediaError() {}
    destroy() {}
  }
  return { default: FakeHls }
})

import { LivePlayer } from '../LivePlayer'

const iso = (m: number) => new Date(Date.now() + m * 60_000).toISOString()
const CHANNELS = [
  { id: 1, number: 1, name: 'Свой 1', category: 'Кино', personal: false, auto: true, enabled: true, canEdit: false, now: { id: '1-1', itemId: 5, kind: 'movie', title: 'Фильм', start: iso(-10), end: iso(30) } },
  {
    id: 100, number: 100, name: 'Внешний', category: 'Новости', personal: false, auto: false, enabled: true, canEdit: false, external: true,
    stream: { url: 'https://a.example.test/live.m3u8', fallbacks: [], unsupported: [] },
  },
]
const json = (body: unknown, status = 200) => Promise.resolve(new Response(body === null ? null : JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

// the video element: a timeline the test moves, play/pause that fire their events
type Media = { t: number; paused: boolean; start: number; end: number }
let media: Media
beforeEach(() => {
  vi.useFakeTimers({ shouldAdvanceTime: true })
  live.sync = 982
  live.live = true
  media = { t: 982, paused: false, start: 922, end: 1000 }
  const proto = HTMLMediaElement.prototype
  vi.spyOn(proto, 'load').mockImplementation(() => {})
  vi.spyOn(proto, 'canPlayType').mockReturnValue('')
  vi.spyOn(proto, 'play').mockImplementation(function (this: HTMLMediaElement) {
    if (media.paused) {
      media.paused = false
      this.dispatchEvent(new Event('play'))
    }
    return Promise.resolve()
  })
  vi.spyOn(proto, 'pause').mockImplementation(function (this: HTMLMediaElement) {
    if (!media.paused) {
      media.paused = true
      this.dispatchEvent(new Event('pause'))
    }
  })
  Object.defineProperty(proto, 'currentTime', { configurable: true, get: () => media.t, set: (v: number) => void (media.t = v) })
  Object.defineProperty(proto, 'paused', { configurable: true, get: () => media.paused })
  Object.defineProperty(proto, 'duration', { configurable: true, get: () => 1010 })
  Object.defineProperty(proto, 'seekable', { configurable: true, get: () => ({ length: 1, start: () => media.start, end: () => media.end }) })
  vi.spyOn(globalThis, 'fetch').mockImplementation((input: RequestInfo | URL) => {
    const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url
    if (url.startsWith('/api/channels/last')) return json(null, 204)
    if (url.startsWith('/api/channels?') || url === '/api/channels') return json({ channels: CHANNELS, lastChannel: 1, canManage: false, canCreate: false })
    if (/^\/api\/channels\/\d+\/guide/.test(url)) return json({ programs: [] })
    if (url === '/api/me/playback-prefs') return json({ audioLangs: [], subMode: 'off', subLangs: [], subSize: 'm', subBackground: 'translucent', custom: true })
    return json({ error: 'not here' }, 404)
  })
})
afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
  for (const k of ['currentTime', 'paused', 'duration', 'seekable']) delete (HTMLMediaElement.prototype as unknown as Record<string, unknown>)[k]
})

const open = async (id: number) => {
  render(
    <MemoryRouter initialEntries={[`/tv/${id}`]}>
      <Routes>
        <Route path="/tv/:id" element={<LivePlayer />} />
      </Routes>
    </MemoryRouter>,
  )
  await screen.findByRole('heading', { name: new RegExp(CHANNELS.find((c) => c.id === id)!.name) })
  await act(async () => {
    await new Promise((r) => setTimeout(r, 30))
    document.querySelector('video')!.dispatchEvent(new Event('playing'))
  })
  await screen.findByRole('button', { name: 'Пауза' })
}
const center = () => screen.getByRole('button', { name: /^(Пауза|Продолжить)$/ }).parentElement!
const hidden = () => center().hasAttribute('data-hidden')

describe('pause on live', () => {
  it('the centre play/pause shows with the pointer, hides after 4 s, stays while paused', async () => {
    await open(1)
    expect(hidden()).toBe(false)
    act(() => void vi.advanceTimersByTime(4100))
    expect(hidden()).toBe(true)
    fireEvent.pointerMove(screen.getByTestId('live-player'), { pointerType: 'mouse' })
    expect(hidden()).toBe(false)
    fireEvent.click(screen.getByRole('button', { name: 'Пауза' }))
    expect(media.paused).toBe(true)
    act(() => void vi.advanceTimersByTime(10_000))
    expect(hidden()).toBe(false)
    expect(screen.getByRole('button', { name: 'Продолжить' })).toBeInTheDocument()
  })

  it('our channel: a short pause goes on from the stopped picture, past the minute from the edge', async () => {
    await open(1)
    fireEvent.click(screen.getByRole('button', { name: 'Пауза' }))
    act(() => void vi.advanceTimersByTime(20_000))
    fireEvent.click(screen.getByRole('button', { name: 'Продолжить' }))
    expect(media.t).toBe(982)
    // a pause of 70 s: the stream moved on, the picture is gone — resume at the edge
    fireEvent.click(screen.getByRole('button', { name: 'Пауза' }))
    act(() => void vi.advanceTimersByTime(70_000))
    live.sync = 1052
    media.start = 992
    media.end = 1070
    fireEvent.click(screen.getByRole('button', { name: 'Продолжить' }))
    expect(media.t).toBe(1052)
  })

  it('«В эфир» more than 10 s behind the edge jumps back to it; «Прямой эфир» at the edge', async () => {
    await open(1)
    expect(screen.getByText('Прямой эфир')).toBeInTheDocument()
    media.t = 950
    act(() => void vi.advanceTimersByTime(600))
    expect(screen.queryByText('Прямой эфир')).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: 'В эфир' }))
    expect(media.t).toBe(982)
    act(() => void vi.advanceTimersByTime(600))
    expect(screen.getByText('Прямой эфир')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'В эфир' })).toBeNull()
  })

  it('an external channel: the same, its seekable range the window', async () => {
    await open(100)
    media.t = 960
    act(() => void vi.advanceTimersByTime(600))
    expect(screen.getByRole('button', { name: 'В эфир' })).toBeInTheDocument()
    // 40 s paused, 22 s behind already: past its 60 s range — the edge
    fireEvent.click(screen.getByRole('button', { name: 'Пауза' }))
    act(() => void vi.advanceTimersByTime(40_000))
    fireEvent.click(screen.getByRole('button', { name: 'Продолжить' }))
    expect(media.t).toBe(982)
  })

  it('a stream that is not live: no «В эфир», a pause resumes where it stopped', async () => {
    live.live = false
    live.sync = null
    await open(100)
    media.t = 30
    act(() => void vi.advanceTimersByTime(600))
    expect(screen.queryByRole('button', { name: 'В эфир' })).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: 'Пауза' }))
    act(() => void vi.advanceTimersByTime(600_000))
    fireEvent.click(screen.getByRole('button', { name: 'Продолжить' }))
    expect(media.t).toBe(30)
  })
})
