// The live player's volume: the speaker and its slider lead the tools row
// (nothing to their left, so nothing moves when they change), and ↑/↓ on the
// focused slider are the slider's, not channel zapping.
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

import { LivePlayer } from '../LivePlayer'

const iso = (m: number) => new Date(Date.now() + m * 60_000).toISOString()
const CHANNELS = [
  { id: 1, number: 1, name: 'Свой 1', category: 'Кино', personal: false, auto: true, enabled: true, canEdit: false, now: { id: '1-1', itemId: 5, kind: 'movie', title: 'Фильм', start: iso(-10), end: iso(30) } },
  { id: 2, number: 2, name: 'Свой 2', category: 'Кино', personal: false, auto: true, enabled: true, canEdit: false, now: { id: '2-1', itemId: 6, kind: 'movie', title: 'Другой', start: iso(-10), end: iso(30) } },
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

describe('live player: volume', () => {
  it('starts at the level and mute the film player remembered, and remembers its own changes', async () => {
    localStorage.setItem('vault.player.volume', '0.4')
    localStorage.setItem('vault.player.muted', '1')
    await open()
    const video = document.querySelector('video')!
    expect(video.volume).toBeCloseTo(0.4)
    expect(video.muted).toBe(true)
    expect(screen.getByRole('slider', { name: 'Громкость' })).toHaveValue('0')
    fireEvent.change(screen.getByRole('slider', { name: 'Громкость' }), { target: { value: '0.7' } })
    expect(localStorage.getItem('vault.player.volume')).toBe('0.7')
    expect(localStorage.getItem('vault.player.muted')).toBe('0')
    fireEvent.keyDown(document.body, { key: 'm' })
    expect(localStorage.getItem('vault.player.muted')).toBe('1')
  })

  it('a mute the browser forces at the start (play() refused) is not remembered', async () => {
    vi.spyOn(HTMLMediaElement.prototype, 'play').mockRejectedValueOnce(new Error('NotAllowedError')).mockResolvedValue(undefined)
    await open()
    expect(document.querySelector('video')!.muted).toBe(true)
    expect(localStorage.getItem('vault.player.muted')).toBeNull()
  })

  it('the speaker and the slider come first in the tools row, before the channel buttons', async () => {
    await open()
    const speaker = screen.getByRole('button', { name: 'Выключить звук' })
    const slider = screen.getByRole('slider', { name: 'Громкость' })
    const up = screen.getByRole('button', { name: 'Следующий канал' })
    expect(speaker.compareDocumentPosition(slider) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(slider.compareDocumentPosition(up) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    // the same group as the channel buttons — the left one, not the right one that grows leftwards
    expect(speaker.parentElement!.parentElement).toBe(up.parentElement)
  })

  it('the slider sets the video volume; moving it unmutes; M mutes', async () => {
    await open()
    const video = document.querySelector('video')!
    const slider = screen.getByRole('slider', { name: 'Громкость' })
    fireEvent.change(slider, { target: { value: '0.4' } })
    expect(video.volume).toBeCloseTo(0.4)
    fireEvent.keyDown(document.body, { key: 'm' })
    expect(video.muted).toBe(true)
    expect(screen.getByRole('button', { name: 'Включить звук' })).toBeInTheDocument()
    expect(slider).toHaveValue('0')
    fireEvent.change(slider, { target: { value: '0.6' } })
    expect(video.muted).toBe(false)
    expect(video.volume).toBeCloseTo(0.6)
  })

  it('↑/↓ with the slider focused are the slider\'s (no zapping); elsewhere they change the channel', async () => {
    await open()
    const slider = screen.getByRole('slider', { name: 'Громкость' })
    slider.focus()
    const down = fireEvent.keyDown(slider, { key: 'ArrowUp' })
    expect(down).toBe(true) // not prevented: the range steps itself
    expect(where()).toBe('/tv/1')
    fireEvent.keyDown(slider, { key: 'ArrowDown' })
    expect(where()).toBe('/tv/1')
    fireEvent.keyDown(document.body, { key: 'ArrowUp' })
    expect(where()).not.toBe('/tv/1')
  })
})
