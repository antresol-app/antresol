// An external channel in the web player: it plays the provider's stream
// itself (never the server's live endpoint), moves to a fallback when the
// stream fails, says «откроется в приложении» when none plays — or at once
// when the web is listed as unsupported — without retrying in a loop; its
// track choice stays in this browser per channel; and, TV being TV, it
// writes nothing but «last channel».
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router'

const hlsLog: { loaded: string[]; instances: FakeHlsType[] } = { loaded: [], instances: [] }
type FakeHlsType = { fire: (event: string, data: unknown) => void }

vi.mock('hls.js', () => {
  class FakeHls {
    static Events = { ERROR: 'hlsError', MANIFEST_PARSED: 'hlsManifestParsed', AUDIO_TRACKS_UPDATED: 'hlsAudioTracksUpdated', SUBTITLE_TRACKS_UPDATED: 'hlsSubtitleTracksUpdated' }
    static ErrorTypes = { NETWORK_ERROR: 'networkError', MEDIA_ERROR: 'mediaError' }
    static isSupported = () => true
    audioTracks = [
      { name: 'English', lang: 'en' },
      { name: 'Français', lang: 'fr' },
    ]
    subtitleTracks = [{ name: 'English', lang: 'en' }]
    audioTrack = 0
    subtitleTrack = -1
    subtitleDisplay = false
    handlers: Record<string, ((e: string, d: unknown) => void)[]> = {}
    constructor() {
      hlsLog.instances.push(this)
    }
    on(e: string, f: (e: string, d: unknown) => void) {
      ;(this.handlers[e] ??= []).push(f)
    }
    fire(e: string, d: unknown) {
      for (const f of this.handlers[e] ?? []) f(e, d)
    }
    loadSource(u: string) {
      hlsLog.loaded.push(u)
    }
    stopLoad() {}
    attachMedia() {}
    recoverMediaError() {}
    destroy() {}
  }
  return { default: FakeHls }
})

const tsLog: { urls: string[]; players: { fire: (e: string) => void }[] } = { urls: [], players: [] }
vi.mock('mpegts.js', () => {
  class FakePlayer {
    handlers: Record<string, (() => void)[]> = {}
    constructor(public url: string) {
      tsLog.urls.push(url)
      tsLog.players.push(this)
    }
    on(e: string, f: () => void) {
      ;(this.handlers[e] ??= []).push(f)
    }
    fire(e: string) {
      for (const f of this.handlers[e] ?? []) f()
    }
    attachMediaElement() {}
    load() {}
    destroy() {}
  }
  return {
    default: {
      Events: { ERROR: 'error' },
      isSupported: () => true,
      createPlayer: (src: { url: string }) => new FakePlayer(src.url),
    },
  }
})

import { LivePlayer, liveTiming } from '../LivePlayer'

const iso = (m: number) => new Date(Date.now() + m * 60_000).toISOString()
const ext = (id: number, number: number, stream: object) => ({
  id, number, name: `Внешний ${number}`, category: 'Новости', personal: false, auto: false, enabled: true, canEdit: false, external: true, stream,
  now: { id: `x1-${id}`, itemId: 0, kind: 'external', title: `Передача ${number}`, start: iso(-10), end: iso(30) },
})
const CHANNELS = [
  { id: 1, number: 1, name: 'Свой 1', category: 'Кино', personal: false, auto: true, enabled: true, canEdit: false },
  ext(100, 100, { url: 'https://a.example.test/live.m3u8', fallbacks: [{ url: 'https://b.example.test/live.m3u8', unsupported: [] }], unsupported: [] }),
  ext(101, 101, { url: 'https://c.example.test/live.m3u8', userAgent: 'Box/1', fallbacks: [], unsupported: ['web'] }),
  ext(102, 102, { url: 'http://d.example.test/live.m3u8', relay: '/api/channels/live/ext/102/0/relay', userAgent: 'Box/1', fallbacks: [], unsupported: ['web'] }),
  ext(103, 103, { url: 'https://e.example.test/live.m3u8', relay: '/api/channels/live/ext/103/0/relay', fallbacks: [], unsupported: [] }),
  ext(104, 104, { url: 'http://f.example.test/live/u/p/7.ts', relay: '/api/channels/live/ext/104/0/relay', format: 'ts', userAgent: 'Box/1', fallbacks: [], unsupported: ['web'] }),
  ext(105, 105, { url: 'https://g.example.test/stream/7', relay: '/api/channels/live/ext/105/0/relay', fallbacks: [], unsupported: ['web'] }),
]

type Call = { method: string; url: string }
let calls: Call[] = []
const json = (body: unknown, status = 200) => Promise.resolve(new Response(body === null ? null : JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

beforeEach(() => {
  calls = []
  hlsLog.loaded = []
  hlsLog.instances = []
  tsLog.urls = []
  tsLog.players = []
  localStorage.clear()
  vi.spyOn(HTMLMediaElement.prototype, 'play').mockImplementation(() => Promise.resolve())
  vi.spyOn(HTMLMediaElement.prototype, 'load').mockImplementation(() => {})
  vi.spyOn(HTMLMediaElement.prototype, 'canPlayType').mockReturnValue('')
  vi.spyOn(globalThis, 'fetch').mockImplementation((input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url
    calls.push({ method: (init?.method ?? 'GET').toUpperCase(), url })
    if (url.startsWith('/api/channels/last')) return json(null, 204)
    if (url.startsWith('/api/channels?') || url === '/api/channels') return json({ channels: CHANNELS, lastChannel: 1, canManage: false, canCreate: false })
    const g = /^\/api\/channels\/(\d+)\/guide/.exec(url)
    if (g) {
      const c = CHANNELS.find((x) => x.id === Number(g[1]))!
      return json({ channel: c, programs: 'now' in c ? [c.now] : [] })
    }
    if (url === '/api/me/playback-prefs') return json({ audioLangs: ['fr'], subMode: 'off', subLangs: [], subSize: 'm', subBackground: 'translucent', custom: true })
    return json({ error: 'not here' }, 404)
  })
})
afterEach(() => vi.restoreAllMocks())

const open = (id: number) =>
  render(
    <MemoryRouter initialEntries={[`/tv/${id}`]}>
      <Routes>
        <Route path="/tv/:id" element={<LivePlayer />} />
      </Routes>
    </MemoryRouter>,
  )

describe('an external channel in the web player', () => {
  it('plays the provider’s stream, then its fallback, then says it opens in the apps — once', async () => {
    open(100)
    await waitFor(() => expect(hlsLog.loaded).toEqual(['https://a.example.test/live.m3u8']))
    expect(calls.some((c) => c.url.startsWith('/api/channels/live/'))).toBe(false)
    await act(async () => hlsLog.instances[0]!.fire('hlsError', { fatal: true, type: 'networkError' }))
    await waitFor(() => expect(hlsLog.loaded).toEqual(['https://a.example.test/live.m3u8', 'https://b.example.test/live.m3u8']))
    // each attempt has its own engine
    expect(hlsLog.instances).toHaveLength(2)
    await act(async () => hlsLog.instances[1]!.fire('hlsError', { fatal: true, type: 'networkError' }))
    expect(await screen.findByText('Этот канал открывается в приложении Antresol или в IPTV-плеере')).toBeInTheDocument()
    await new Promise((r) => setTimeout(r, 50))
    expect(hlsLog.loaded).toHaveLength(2)
    const writes = calls.filter((c) => c.method !== 'GET')
    expect(writes.every((w) => w.url === '/api/channels/last')).toBe(true)
  })

  it('does not even try a stream the web cannot play', async () => {
    open(101)
    expect(await screen.findByText('Этот канал открывается в приложении Antresol или в IPTV-плеере')).toBeInTheDocument()
    expect(screen.getByText(/user-agent или referrer/)).toBeInTheDocument()
    expect(hlsLog.loaded).toEqual([])
    expect(screen.queryByRole('button', { name: 'Повторить' })).toBeNull()
  })

  it('plays a stream the web cannot play as given through the server’s relay', async () => {
    open(102)
    await waitFor(() => expect(hlsLog.loaded).toEqual(['/api/channels/live/ext/102/0/relay']))
    expect(screen.queryByText('Этот канал открывается в приложении Antresol или в IPTV-плеере')).toBeNull()
    expect(hlsLog.loaded.some((u) => u.includes('d.example.test'))).toBe(false)
  })

  it('falls back to the relay when the provider’s own stream fails (no CORS)', async () => {
    open(103)
    await waitFor(() => expect(hlsLog.loaded).toEqual(['https://e.example.test/live.m3u8']))
    await act(async () => hlsLog.instances[0]!.fire('hlsError', { fatal: true, type: 'networkError' }))
    await waitFor(() => expect(hlsLog.loaded).toEqual(['https://e.example.test/live.m3u8', '/api/channels/live/ext/103/0/relay']))
    expect(screen.queryByText('Этот канал открывается в приложении Antresol или в IPTV-плеере')).toBeNull()
  })

  // reported: the stream plays, and «открывается в приложении…» covers it
  // anyway, ~10 s in. The direct attempt failed (no CORS), the relay plays; then an
  // error arrives — a late one of the abandoned direct attempt, or the relay's
  // own mid-stream hiccup (a playlist reload that failed) while the buffer still
  // plays. Neither is «nothing plays»: no message; when the picture really stops,
  // the same stream is started again once, and the message shows only when that
  // fails too — and goes again as soon as the picture moves.
  it('never says «opens in the apps» over a picture that plays (the race of a late error)', async () => {
    liveTiming.stallMs = 300
    const msg = 'Этот канал открывается в приложении Antresol или в IPTV-плеере'
    const { container } = open(103)
    const video = container.querySelector('video')!
    let time = 0
    Object.defineProperty(video, 'paused', { configurable: true, get: () => false })
    Object.defineProperty(video, 'currentTime', { configurable: true, get: () => time, set: () => {} })
    const tick = () => act(async () => {
      time += 0.25
      fireEvent.timeUpdate(video)
    })
    await waitFor(() => expect(hlsLog.instances).toHaveLength(1))
    await act(async () => hlsLog.instances[0]!.fire('hlsError', { fatal: true, type: 'networkError' }))
    await waitFor(() => expect(hlsLog.instances).toHaveLength(2)) // the relay
    await tick()
    await tick()
    // the abandoned direct attempt reports late: nothing changes
    await act(async () => hlsLog.instances[0]!.fire('hlsError', { fatal: true, type: 'networkError' }))
    // the relay's own error while its buffer still plays: nothing said, nothing restarted
    await act(async () => hlsLog.instances[1]!.fire('hlsError', { fatal: true, type: 'networkError' }))
    await tick()
    expect(screen.queryByText(msg)).toBeNull()
    expect(hlsLog.loaded).toHaveLength(2)
    // the picture stops: the relay is started again, once — still no message
    await waitFor(() => expect(hlsLog.loaded).toEqual(['https://e.example.test/live.m3u8', '/api/channels/live/ext/103/0/relay', '/api/channels/live/ext/103/0/relay']), { timeout: 3000 })
    expect(screen.queryByText(msg)).toBeNull()
    // that fails too, nothing moves: now nothing plays, and it says so
    await act(async () => hlsLog.instances[2]!.fire('hlsError', { fatal: true, type: 'networkError' }))
    expect(await screen.findByText(msg)).toBeInTheDocument()
    // and the moment the picture moves again, the message goes
    await tick()
    await tick()
    await waitFor(() => expect(screen.queryByText(msg)).toBeNull())
    liveTiming.stallMs = 4000
  })

  // mpegts.js gets an absolute URL: its loader runs in a worker
  it('plays a raw MPEG-TS stream through the relay with mpegts.js, not hls.js', async () => {
    open(104)
    await waitFor(() => expect(tsLog.urls).toEqual([expect.stringMatching(/^https?:\/\/[^/]+\/api\/channels\/live\/ext\/104\/0\/relay$/)]))
    expect(hlsLog.loaded).toEqual([])
    await act(async () => tsLog.players[0]!.fire('error'))
    expect(await screen.findByText('Этот канал открывается в приложении Antresol или в IPTV-плеере')).toBeInTheDocument()
    expect(tsLog.urls).toHaveLength(1)
  })

  it('a stream of unknown format tries HLS, then MPEG-TS, once each', async () => {
    open(105)
    await waitFor(() => expect(hlsLog.loaded).toEqual(['/api/channels/live/ext/105/0/relay']))
    await act(async () => hlsLog.instances[0]!.fire('hlsError', { fatal: true, type: 'networkError' }))
    await waitFor(() => expect(tsLog.urls).toEqual([expect.stringMatching(/\/api\/channels\/live\/ext\/105\/0\/relay$/)]))
    expect(screen.queryByText('Этот канал открывается в приложении Antresol или в IPTV-плеере')).toBeNull()
    await act(async () => tsLog.players[0]!.fire('error'))
    expect(await screen.findByText('Этот канал открывается в приложении Antresol или в IPTV-плеере')).toBeInTheDocument()
  })

  it('keeps the track choice per channel in this browser, not on the server', async () => {
    open(100)
    await waitFor(() => expect(hlsLog.instances).toHaveLength(1))
    await act(async () => hlsLog.instances[0]!.fire('hlsManifestParsed', {}))
    fireEvent.click(screen.getByRole('button', { name: 'Звук и субтитры' }))
    fireEvent.click((await screen.findAllByRole('menuitemradio', { name: /English/ }))[0]!)
    expect(JSON.parse(localStorage.getItem('antresol.tv.extTracks') ?? '{}')['100']).toMatchObject({ audioName: 'English' })
    expect(calls.filter((c) => c.method !== 'GET').every((w) => w.url === '/api/channels/last')).toBe(true)
  })
})
