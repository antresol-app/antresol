// Item D, «TV is TV»: the live player writes nothing but the profile's
// «last channel» and its stars. It mounts the real LivePlayer, tunes in,
// zaps, stars, switches the voice — and checks every request it made: no
// progress, no watched flags, no playback session or report, no track
// memory (PUT /api/items/{id}/track-prefs), no scrobble, no beacon.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router'

vi.mock('hls.js', () => {
  class FakeHls {
    static Events = { ERROR: 'hlsError', MANIFEST_PARSED: 'hlsManifestParsed', AUDIO_TRACKS_UPDATED: 'hlsAudioTracksUpdated', SUBTITLE_TRACKS_UPDATED: 'hlsSubtitleTracksUpdated' }
    static ErrorTypes = { NETWORK_ERROR: 'networkError', MEDIA_ERROR: 'mediaError' }
    static isSupported = () => true
    audioTracks = [{ name: 'Русский' }, { name: 'Русский 2' }, { name: 'English' }]
    subtitleTracks = [{ name: 'Русские' }, { name: 'English' }]
    audioTrack = 0
    subtitleTrack = -1
    subtitleDisplay = false
    on() {}
    loadSource() {}
    attachMedia() {}
    recoverMediaError() {}
    destroy() {}
  }
  return { default: FakeHls }
})

import { LivePlayer } from '../LivePlayer'
import { resetTrackSession } from '../liveTracks'

const iso = (minutes: number) => new Date(Date.now() + minutes * 60_000).toISOString()
const prog = (ch: number, seq: number, from: number, to: number, title: string) => ({
  id: `${ch}-${seq}`, itemId: 100 + seq, kind: 'movie', title, start: iso(from), end: iso(to),
  audio: [
    { slot: 'ru', name: 'Русский', label: 'Русский — LostFilm', lang: 'ru' },
    { slot: 'en', name: 'English', label: 'English', lang: 'en' },
  ],
  subtitles: [{ slot: 'en', name: 'English', label: 'English', lang: 'en' }],
})
const channel = (id: number, number: number, favorite = false) => ({
  id, number, name: `Канал ${number}`, category: 'Кино', personal: false, auto: true, enabled: true, canEdit: false, favorite,
  now: prog(id, 1, -10, 30, `Фильм ${number}`), next: prog(id, 2, 30, 120, `Следом ${number}`),
})

type Call = { method: string; url: string }
let calls: Call[] = []

function json(body: unknown, status = 200) {
  return Promise.resolve(new Response(body === null ? null : JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))
}

beforeEach(() => {
  calls = []
  resetTrackSession()
  vi.spyOn(HTMLMediaElement.prototype, 'play').mockImplementation(() => Promise.resolve())
  vi.spyOn(HTMLMediaElement.prototype, 'load').mockImplementation(() => {})
  vi.spyOn(HTMLMediaElement.prototype, 'canPlayType').mockReturnValue('')
  Object.defineProperty(navigator, 'sendBeacon', { configurable: true, value: vi.fn(() => true) })
  vi.spyOn(globalThis, 'fetch').mockImplementation((input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url
    const method = (init?.method ?? 'GET').toUpperCase()
    calls.push({ method, url })
    if (url.startsWith('/api/channels/last') || url.includes('/favorite')) return json(null, 204)
    if (url.startsWith('/api/channels?') || url === '/api/channels') {
      return json({ channels: [channel(1, 1), channel(2, 2, true), channel(3, 3), channel(4, 4, true)], lastChannel: 2, canManage: false, canCreate: true })
    }
    const g = /^\/api\/channels\/(\d+)\/guide/.exec(url)
    if (g) {
      const id = Number(g[1])
      const c = channel(id, id)
      return json({ channel: c, programs: [c.now, c.next] })
    }
    if (url === '/api/me/playback-prefs') return json({ audioLangs: ['en', 'ru'], subMode: 'off', subLangs: [], subSize: 'm', subBackground: 'translucent', custom: true })
    return json({ error: 'not here' }, 404)
  })
})

afterEach(() => {
  vi.restoreAllMocks()
})

const FORBIDDEN = /\/api\/(progress|play\/|items\/\d+\/(watched|track-prefs)|music\/plays|history|scrobble|sessions|report)|\/api\/hls\//

describe('the live player sends no progress or history (TV is TV)', () => {
  it('tunes in, zaps, stars and switches the voice; writes only «last channel» and the star', async () => {
    render(
      <MemoryRouter initialEntries={['/tv/2?f=fav']}>
        <Routes>
          <Route path="/tv/:id" element={<LivePlayer />} />
        </Routes>
      </MemoryRouter>,
    )
    // tuned in: the channel, the programme, its «last channel» write
    await screen.findByText('Канал 2')
    await screen.findByText('Фильм 2')
    await waitFor(() => expect(calls).toContainEqual({ method: 'PUT', url: '/api/channels/last' }))

    // ↑ zaps within favourites (2 → 4), not to 3
    await act(async () => {
      fireEvent.keyDown(window, { key: 'ArrowUp' })
    })
    await screen.findByText('Канал 4')
    // and wraps round (4 → 2)
    await act(async () => {
      fireEvent.keyDown(window, { key: 'ArrowUp' })
    })
    await screen.findByText('Канал 2')

    // the star
    fireEvent.click(screen.getByRole('button', { name: 'Убрать из избранного' }))
    await waitFor(() => expect(calls.some((c) => c.method === 'DELETE' && c.url === '/api/channels/2/favorite')).toBe(true))

    // a voice from the menu: chosen for the session, not saved anywhere
    fireEvent.click(screen.getByRole('button', { name: 'Звук и субтитры' }))
    fireEvent.click(await screen.findByRole('menuitemradio', { name: /Русский — LostFilm/ }))

    // info, the channels panel, Escape
    fireEvent.keyDown(window, { key: 'i' })
    fireEvent.keyDown(window, { key: 'c' })
    fireEvent.keyDown(window, { key: 'Escape' })
    await new Promise((r) => setTimeout(r, 50))

    const writes = calls.filter((c) => c.method !== 'GET')
    expect(writes.length).toBeGreaterThan(0)
    for (const w of writes) {
      expect(w.url === '/api/channels/last' || /^\/api\/channels\/\d+\/favorite$/.test(w.url)).toBe(true)
    }
    expect(calls.filter((c) => FORBIDDEN.test(c.url))).toEqual([])
    expect(navigator.sendBeacon).not.toHaveBeenCalled()
  })
})
