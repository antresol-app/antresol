// Round 5: the live stream carries up to 8 audio and 6 subtitle renditions
// with any names in any order. The menu lists them all; the choice is made
// by NAME and sticks per channel, across its programmes (by language on a
// programme without that name) — through hls.js and through Safari's own
// HLS. With no choice on a channel: the device's language, then the
// profile's, then the original.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router'

type R = { name: string; lang: string }
// «Много голосов (2015)» on channel 1: every rendition of the run; channel 2: a plain film
const MASTERS: Record<number, { audio: R[]; subs: R[] }> = {
  1: {
    audio: [
      { name: 'Дубляж', lang: 'ru' },
      { name: 'LostFilm', lang: 'ru' },
      { name: 'Кураж-Бамбей', lang: 'ru' },
      { name: 'LostFilm (очищено)', lang: 'ru' },
      { name: 'Пифагор', lang: 'ru' },
      { name: 'Оригинал (English)', lang: 'en' },
      { name: 'Français', lang: 'fr' },
      { name: 'Комментарий режиссёра', lang: 'en' },
    ],
    subs: [
      { name: 'Русские', lang: 'ru' },
      { name: 'Русские (полные)', lang: 'ru' },
      { name: 'English', lang: 'en' },
      { name: 'English SDH', lang: 'en' },
      { name: 'Français', lang: 'fr' },
      { name: 'Deutsch', lang: 'de' },
    ],
  },
  2: { audio: [{ name: 'Оригинал (English)', lang: 'en' }, { name: 'Дубляж', lang: 'ru' }], subs: [{ name: 'English', lang: 'en' }] },
}
const idOf = (url: string) => Number(/live\/(\d+)\//.exec(url)?.[1] ?? 0)

const hlsState = vi.hoisted(() => ({ last: null as null | { audioTrack: number; subtitleTrack: number; audioTracks: { name: string }[] } }))

vi.mock('hls.js', () => {
  class FakeHls {
    static Events = { ERROR: 'hlsError', MANIFEST_PARSED: 'hlsManifestParsed', AUDIO_TRACKS_UPDATED: 'hlsAudioTracksUpdated', SUBTITLE_TRACKS_UPDATED: 'hlsSubtitleTracksUpdated' }
    static ErrorTypes = { NETWORK_ERROR: 'networkError', MEDIA_ERROR: 'mediaError' }
    static isSupported = () => true
    audioTracks: R[] = []
    subtitleTracks: R[] = []
    audioTrack = 0
    subtitleTrack = -1
    subtitleDisplay = false
    handlers: Record<string, () => void> = {}
    constructor() {
      hlsState.last = this
    }
    on(ev: string, f: () => void) {
      this.handlers[ev] = f
    }
    loadSource(url: string) {
      const m = MASTERS[idOf(url)]!
      this.audioTracks = m.audio
      this.subtitleTracks = m.subs
      queueMicrotask(() => this.handlers.hlsManifestParsed?.())
    }
    attachMedia() {}
    recoverMediaError() {}
    destroy() {}
  }
  return { default: FakeHls }
})

import { LivePlayer } from '../LivePlayer'
import { resetTrackSession } from '../liveTracks'

const iso = (minutes: number) => new Date(Date.now() + minutes * 60_000).toISOString()
const label = (r: R) => (r.lang === 'ru' && r.name !== 'Комментарий режиссёра' ? `Русский — ${r.name}` : r.name)
function program(ch: number, seq: number, from: number, to: number, audio: R[], subs: R[]) {
  return {
    id: `${ch}-${seq}`,
    itemId: 100 * ch + seq,
    kind: 'movie',
    title: ch === 1 ? 'Много голосов' : `Фильм ${ch}`,
    year: 2015,
    start: iso(from),
    end: iso(to),
    audio: audio.map((r) => ({ name: r.name, label: label(r), lang: r.lang })),
    subtitles: subs.map((r) => ({ name: r.name, label: r.name, lang: r.lang })),
  }
}
const channel = (id: number) => {
  const m = MASTERS[id]!
  return {
    id,
    number: id,
    name: `Канал ${id}`,
    category: 'Кино',
    personal: false,
    auto: false,
    enabled: true,
    canEdit: false,
    now: program(id, 1, -10, 30, m.audio, m.subs),
    next: program(id, 2, 30, 120, m.audio.slice(0, 1), []),
  }
}

let langs = ['ru-RU']
beforeEach(() => {
  resetTrackSession()
  localStorage.clear()
  langs = ['ru-RU']
  vi.spyOn(navigator, 'languages', 'get').mockImplementation(() => langs)
  hlsState.last = null
  vi.spyOn(HTMLMediaElement.prototype, 'play').mockImplementation(() => Promise.resolve())
  vi.spyOn(HTMLMediaElement.prototype, 'load').mockImplementation(() => {})
  vi.spyOn(globalThis, 'fetch').mockImplementation((input: RequestInfo | URL) => {
    const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url
    const json = (b: unknown, status = 200) => Promise.resolve(new Response(b === null ? null : JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } }))
    if (url.startsWith('/api/channels/last')) return json(null, 204)
    if (url.startsWith('/api/channels?') || url === '/api/channels') return json({ channels: [channel(1), channel(2)], canManage: false, canCreate: false })
    const g = /^\/api\/channels\/(\d+)\/guide/.exec(url)
    if (g) {
      const c = channel(Number(g[1]))
      return json({ channel: c, programs: [c.now, c.next] })
    }
    if (url === '/api/me/playback-prefs') return json({ audioLangs: ['ru'], subMode: 'off', subLangs: [], subSize: 'm', subBackground: 'translucent', custom: true })
    return json({ error: 'not here' }, 404)
  })
})

afterEach(() => {
  vi.restoreAllMocks()
  delete (HTMLMediaElement.prototype as { audioTracks?: unknown }).audioTracks
})

function mount(at = '/tv/1') {
  return render(
    <MemoryRouter initialEntries={[at]}>
      <Routes>
        <Route path="/tv/:id" element={<LivePlayer />} />
      </Routes>
    </MemoryRouter>,
  )
}

const openMenu = async () => {
  fireEvent.click(screen.getByRole('button', { name: 'Звук и субтитры' }))
  return screen.findByRole('menu', { name: 'Звук и субтитры' })
}
const zap = async (key: 'ArrowUp' | 'ArrowDown', to: string) => {
  await act(async () => {
    fireEvent.keyDown(window, { key })
  })
  await screen.findByText(to)
}

describe('the live player’s tracks (hls.js)', () => {
  it('lists 8 voices and 6 subtitles, selects by name and keeps the choice per channel', async () => {
    vi.spyOn(HTMLMediaElement.prototype, 'canPlayType').mockReturnValue('')
    mount()
    await screen.findByText('Много голосов')
    await waitFor(() => expect(hlsState.last?.audioTracks.length).toBe(8))
    const menu = await openMenu()
    const [voices, subs] = within(menu).getAllByRole('group')
    expect(within(voices!).getAllByRole('menuitemradio').map((i) => i.textContent)).toEqual(MASTERS[1]!.audio.map(label))
    expect(within(subs!).getAllByRole('menuitemradio')).toHaveLength(7) // «Выключены» + 6
    // the profile's Russian: the first Russian voice the server lists
    expect(within(voices!).getByRole('menuitemradio', { name: 'Русский — Дубляж' })).toHaveAttribute('aria-checked', 'true')

    fireEvent.click(within(voices!).getByRole('menuitemradio', { name: 'Русский — Кураж-Бамбей' }))
    await waitFor(() => expect(hlsState.last?.audioTrack).toBe(2))
    fireEvent.click(within(await openMenu()).getByRole('menuitemradio', { name: 'English SDH' }))
    await waitFor(() => expect(hlsState.last?.subtitleTrack).toBe(3))

    // channel 2: nothing chosen there — the device's Russian, no subtitles (the profile's «off»)
    await zap('ArrowUp', 'Канал 2')
    await waitFor(() => expect(hlsState.last?.audioTracks.length).toBe(2))
    await waitFor(() => expect(hlsState.last?.audioTrack).toBe(1))
    expect(hlsState.last?.subtitleTrack).toBe(-1)
    // and back: the very rendition chosen on channel 1
    await zap('ArrowUp', 'Канал 1')
    await waitFor(() => expect(hlsState.last?.audioTracks.length).toBe(8))
    await waitFor(() => expect(hlsState.last?.audioTrack).toBe(2))
    await waitFor(() => expect(hlsState.last?.subtitleTrack).toBe(3))
  })
})

describe('the default voice', () => {
  it('the device’s language before the profile’s', async () => {
    vi.spyOn(HTMLMediaElement.prototype, 'canPlayType').mockReturnValue('')
    langs = ['en-GB', 'ru']
    mount('/tv/2')
    await screen.findByText('Канал 2')
    await waitFor(() => expect(hlsState.last?.audioTracks.length).toBe(2))
    // the profile says Russian; the browser is English: the original English
    await waitFor(() => expect(hlsState.last?.audioTrack).toBe(0))
  })
  it('no device language present: the profile’s', async () => {
    vi.spyOn(HTMLMediaElement.prototype, 'canPlayType').mockReturnValue('')
    langs = ['ja-JP']
    mount('/tv/2')
    await screen.findByText('Канал 2')
    await waitFor(() => expect(hlsState.last?.audioTracks.length).toBe(2))
    await waitFor(() => expect(hlsState.last?.audioTrack).toBe(1))
  })
})

describe('the live player’s tracks (Safari’s own HLS)', () => {
  it('switches the native audio track by its label and keeps it on its channel', async () => {
    vi.spyOn(HTMLMediaElement.prototype, 'canPlayType').mockReturnValue('maybe')
    // the native lists follow the stream the element plays
    const lists = new Map<number, { label: string; language: string; enabled: boolean }[]>()
    for (const id of [1, 2]) lists.set(id, MASTERS[id]!.audio.map((r, i) => ({ label: r.name, language: r.lang, enabled: i === 0 })))
    Object.defineProperty(HTMLMediaElement.prototype, 'audioTracks', {
      configurable: true,
      get(this: HTMLMediaElement) {
        const l = lists.get(idOf(this.getAttribute('src') ?? '')) ?? []
        return Object.assign([...l], { addEventListener() {}, removeEventListener() {} })
      },
    })
    const enabled = (id: number) => lists.get(id)!.find((t) => t.enabled)?.label
    const { container } = mount()
    await screen.findByText('Много голосов')
    const video = container.querySelector('video')!
    await act(async () => {
      video.dispatchEvent(new Event('loadedmetadata'))
    })
    fireEvent.click(within(await openMenu()).getByRole('menuitemradio', { name: 'Русский — LostFilm (очищено)' }))
    await waitFor(() => expect(enabled(1)).toBe('LostFilm (очищено)'))
    await zap('ArrowUp', 'Канал 2')
    await act(async () => {
      video.dispatchEvent(new Event('loadedmetadata'))
    })
    await waitFor(() => expect(enabled(2)).toBe('Дубляж'))
    await zap('ArrowUp', 'Канал 1')
    await act(async () => {
      video.dispatchEvent(new Event('loadedmetadata'))
    })
    await waitFor(() => expect(enabled(1)).toBe('LostFilm (очищено)'))
  })
})
