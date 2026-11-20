// «Качество» in the live player (owner, 2026-11-20: «В разделе TV нельзя
// выбрать качество»). Our channel: the film player's menu, the choice kept
// for films and channels alike on this device, a choice plays the channel
// again in that quality (?q=). An external channel: the variants of its
// own master when it has several («Авто» first), switched in hls.js; none
// when it has one.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router'

const hlsLog: { loaded: string[]; instances: FakeHlsType[] } = { loaded: [], instances: [] }
type FakeHlsType = { fire: (event: string, data: unknown) => void; currentLevel: number; levels: { height: number; bitrate: number }[] }

vi.mock('hls.js', () => {
  class FakeHls {
    static Events = { ERROR: 'hlsError', MANIFEST_PARSED: 'hlsManifestParsed', LEVEL_SWITCHED: 'hlsLevelSwitched', AUDIO_TRACKS_UPDATED: 'hlsAudioTracksUpdated', SUBTITLE_TRACKS_UPDATED: 'hlsSubtitleTracksUpdated' }
    static ErrorTypes = { NETWORK_ERROR: 'networkError', MEDIA_ERROR: 'mediaError' }
    static isSupported = () => true
    audioTracks = [
      { name: 'English', lang: 'en' },
      { name: 'Français', lang: 'fr' },
    ]
    subtitleTracks = [{ name: 'English', lang: 'en' }]
    audioTrack = 0
    subtitleTrack = -1
    levels = [
      { height: 720, bitrate: 3_000_000 },
      { height: 1080, bitrate: 6_000_000 },
    ]
    currentLevel = -1
    get autoLevelEnabled() {
      return this.currentLevel < 0
    }
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


import { LivePlayer } from '../LivePlayer'

const iso = (m: number) => new Date(Date.now() + m * 60_000).toISOString()
const CHANNELS = [
  { id: 1, number: 1, name: 'Свой 1', category: 'Кино', personal: false, auto: true, enabled: true, canEdit: false },
  {
    id: 100, number: 100, name: 'Внешний', category: 'Новости', personal: false, auto: false, enabled: true, canEdit: false, external: true,
    stream: { url: 'https://a.example.test/live.m3u8', fallbacks: [], unsupported: [] },
    now: { id: 'x1-100', itemId: 0, kind: 'external', title: 'Передача', start: iso(-10), end: iso(30) },
  },
]

let calls: string[] = []
const json = (body: unknown, status = 200) => Promise.resolve(new Response(body === null ? null : JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

beforeEach(() => {
  calls = []
  hlsLog.loaded = []
  hlsLog.instances = []
  localStorage.clear()
  vi.spyOn(HTMLMediaElement.prototype, 'play').mockImplementation(() => Promise.resolve())
  vi.spyOn(HTMLMediaElement.prototype, 'load').mockImplementation(() => {})
  vi.spyOn(HTMLMediaElement.prototype, 'canPlayType').mockReturnValue('')
  vi.spyOn(globalThis, 'fetch').mockImplementation((input: RequestInfo | URL) => {
    const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url
    calls.push(url)
    if (url.startsWith('/api/channels/last')) return json(null, 204)
    if (url.startsWith('/api/channels?') || url === '/api/channels') return json({ channels: CHANNELS, lastChannel: 1, canManage: false, canCreate: false })
    const q = /^\/api\/channels\/live\/quality\?q=(\w+)/.exec(url)
    if (q) return json({ selected: q[1], applied: q[1] === 'auto' ? '720' : q[1], remote: q[1] === 'auto' })
    if (/^\/api\/channels\/\d+\/guide/.test(url)) return json({ programs: [] })
    if (url === '/api/me/playback-prefs') return json({ audioLangs: ['ru'], subMode: 'off', subLangs: [], subSize: 'm', subBackground: 'translucent', custom: true })
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

describe('«Качество» in the live player', () => {
  it('our channel: the film player’s menu; a choice plays the channel again in it and is kept', async () => {
    open(1)
    await waitFor(() => expect(hlsLog.loaded).toEqual(['/api/channels/live/1/index.m3u8']))
    // «Авто» outside the home: «Качество вне дома» says what it is
    const trigger = await screen.findByRole('button', { name: 'Качество: Авто · 720p' })
    await userEvent.click(trigger)
    await userEvent.click(screen.getByRole('menuitemradio', { name: /480p/ }))
    await waitFor(() => expect(hlsLog.loaded).toEqual(['/api/channels/live/1/index.m3u8', '/api/channels/live/1/index.m3u8?q=480']))
    expect(localStorage.getItem('vault.player.quality')).toBe('480')
    expect(await screen.findByRole('button', { name: 'Качество: 480p' })).toBeInTheDocument()
  })

  it('our channel opens in the quality chosen last (a film’s choice too)', async () => {
    localStorage.setItem('vault.player.quality', '720')
    open(1)
    await waitFor(() => expect(hlsLog.loaded).toEqual(['/api/channels/live/1/index.m3u8?q=720']))
  })

  it('an external channel: its own variants, «Авто» first, switched in place', async () => {
    open(100)
    await waitFor(() => expect(hlsLog.loaded).toEqual(['https://a.example.test/live.m3u8']))
    expect(screen.queryByRole('button', { name: /^Качество/ })).toBeNull()
    const h = hlsLog.instances[0]!
    await act(async () => h.fire('hlsManifestParsed', {}))
    await userEvent.click(await screen.findByRole('button', { name: 'Качество: Авто' }))
    const items = screen.getAllByRole('menuitemradio')
    expect(items.map((i) => i.textContent)).toEqual(['Авто', '1080p', '720p'])
    await userEvent.click(screen.getByRole('menuitemradio', { name: '720p' }))
    expect(h.currentLevel).toBe(0)
    expect(await screen.findByRole('button', { name: 'Качество: 720p' })).toBeInTheDocument()
    // its stream is played as it is: the server's live endpoint is never asked
    expect(calls.some((c) => c.startsWith('/api/channels/live/'))).toBe(false)
    expect(hlsLog.loaded).toHaveLength(1)
  })

  it('an external channel with one variant has no menu', async () => {
    open(100)
    await waitFor(() => expect(hlsLog.instances).toHaveLength(1))
    const h = hlsLog.instances[0]!
    h.levels = [{ height: 720, bitrate: 3_000_000 }]
    await act(async () => h.fire('hlsManifestParsed', {}))
    expect(screen.queryByRole('button', { name: /^Качество/ })).toBeNull()
  })
})
