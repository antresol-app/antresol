// A channel by its number: digits typed on a keyboard or a TV remote show
// «23_» and switch after a pause or at once with Enter (TiviMate's way).
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router'

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
    liveSyncPosition = 10
    latestLevelDetails = { live: true }
    on() {}
    loadSource() {}
    stopLoad() {}
    attachMedia() {}
    recoverMediaError() {}
    destroy() {}
  }
  return { default: FakeHls }
})

import { LivePlayer, liveTiming } from '../LivePlayer'

const iso = (m: number) => new Date(Date.now() + m * 60_000).toISOString()
const CHANNELS = [
  { id: 1, number: 1, name: 'Свой 1', category: 'Кино', personal: false, auto: true, enabled: true, canEdit: false, now: { id: '1-1', itemId: 5, kind: 'movie', title: 'Фильм', start: iso(-10), end: iso(30) } },
  { id: 2, number: 2, name: 'Свой 2', category: 'Кино', personal: false, auto: true, enabled: true, canEdit: false, now: { id: '2-1', itemId: 6, kind: 'movie', title: 'Другой', start: iso(-10), end: iso(30) } },
  { id: 7, number: 23, name: 'Двадцать три', category: 'Кино', personal: false, auto: true, enabled: true, canEdit: false, now: { id: '7-1', itemId: 7, kind: 'movie', title: 'Третий', start: iso(-10), end: iso(30) } },
]
const json = (body: unknown, status = 200) => Promise.resolve(new Response(body === null ? null : JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

beforeEach(() => {
  localStorage.clear()
  const proto = HTMLMediaElement.prototype
  vi.spyOn(proto, 'load').mockImplementation(() => {})
  vi.spyOn(proto, 'canPlayType').mockReturnValue('')
  vi.spyOn(proto, 'play').mockImplementation(() => Promise.resolve())
  vi.spyOn(proto, 'pause').mockImplementation(() => {})
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
  // the player goes first: a late effect must not meet the restored (unimplemented) play()
  cleanup()
  vi.restoreAllMocks()
})

function Where() {
  return <output data-testid="where">{useLocation().pathname}</output>
}
const open = async () => {
  render(
    <MemoryRouter initialEntries={['/tv/1']}>
      <Where />
      <Routes>
        <Route path="/tv/:id" element={<LivePlayer />} />
      </Routes>
    </MemoryRouter>,
  )
  await screen.findByRole('heading', { name: /Свой 1/ })
  await act(async () => {
    await new Promise((r) => setTimeout(r, 30))
  })
}
const where = () => screen.getByTestId('where').textContent

const wait = (ms: number) => act(async () => {
  await new Promise((r) => setTimeout(r, ms))
})
const key = (k: string) => fireEvent.keyDown(document.body, { key: k })

describe('live player: a channel by its number', () => {
  beforeEach(() => {
    liveTiming.numberMs = 60
  })
  afterEach(() => {
    liveTiming.numberMs = 1500
  })

  it('digits show «23_» on the picture and switch after a pause', async () => {
    await open()
    key('2')
    expect(screen.getByTestId('channel-number-entry').textContent).toBe('2_')
    key('3')
    expect(screen.getByTestId('channel-number-entry').textContent).toBe('23_')
    expect(where()).toBe('/tv/1')
    await wait(120)
    expect(where()).toBe('/tv/7')
    expect(screen.queryByTestId('channel-number-entry')).toBeNull()
  })

  it('Enter switches at once; Escape gives up', async () => {
    await open()
    key('2')
    key('Enter')
    expect(where()).toBe('/tv/2')
    key('2')
    key('Escape')
    await wait(120)
    expect(where()).toBe('/tv/2')
    expect(screen.queryByTestId('channel-number-entry')).toBeNull()
  })

  it('a number no channel has says so and stays', async () => {
    await open()
    key('9')
    key('9')
    key('Enter')
    expect(screen.getByTestId('channel-number-entry').textContent).toBe('Нет канала 99')
    expect(where()).toBe('/tv/1')
    await wait(120)
    expect(screen.queryByTestId('channel-number-entry')).toBeNull()
  })
})
