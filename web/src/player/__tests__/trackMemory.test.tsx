import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { MediaFile, PlaybackPrefs, SubTrack, Track, TrackPrefs } from '../../api/types'
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

const mom = { id: 2, name: 'Мама', avatar: 'grad-1', hasPassword: false, isAdmin: false, isKid: false, maxRating: null }
const json = (b: unknown, status = 200) => new Response(b === undefined ? null : JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } })
const audio: Track[] = [
  { index: 1, codec: 'aac', lang: 'eng', title: '', default: true },
  { index: 2, codec: 'aac', lang: 'rus', title: 'TVShows', default: false },
  { index: 3, codec: 'aac', lang: 'rus', title: 'Кубик в Кубе', default: false },
]
const sub = (index: number, lang: string, title = '', forced = false): SubTrack => ({
  index, codec: 'subrip', lang, title, forced, default: false, external: false, burnIn: false, url: `/api/subs/5/${index}.vtt`,
})
const subs = [sub(4, 'rus', 'Надписи', true), sub(5, 'eng', 'Full'), sub(6, 'rus', 'Полные')]
const prefs: PlaybackPrefs = { audioLangs: ['ru'], subMode: 'off', subLangs: [], subSize: 'xl', subBackground: 'solid', custom: true }
const file: MediaFile = { id: 5, name: 'e02.mkv', size: 1, durationMs: 60000, audio, subs: [] }

type Call = { url: string; method: string; body?: unknown }
let calls: Call[]

describe('VideoPlayer: tracks that follow the profile', () => {
  beforeEach(() => {
    session.reset()
    localStorage.clear()
    calls = []
    vi.spyOn(HTMLMediaElement.prototype, 'play').mockResolvedValue(undefined)
    vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {})
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input)
        const method = init?.method ?? 'GET'
        calls.push({ url, method, body: typeof init?.body === 'string' ? JSON.parse(init.body) : undefined })
        if (url.endsWith('/api/login')) return json(mom)
        if (url.includes('/subtitle-offset')) return json({ offsetMs: (JSON.parse(String(init?.body)) as { offsetMs: number }).offsetMs })
        if (url.includes('/track-prefs') && method === 'PUT') return json(undefined, 204)
        if (url.endsWith('.vtt')) return new Response('WEBVTT\n\n00:00.000 --> 00:30.000\nПривет\n', { status: 200 })
        return json({})
      }),
    )
  })
  afterEach(() => vi.unstubAllGlobals())

  const play = async (tracks: TrackPrefs) => {
    await session.login(2)
    const start = vi.fn(async (_f: MediaFile, body: { audioIndex?: number }) => ({
      mode: 'direct', url: '/api/stream/5', startSec: 0, durationMs: 60000, audioIndex: body.audioIndex ?? 1, audio, subtitles: subs,
    }))
    render(<VideoPlayer itemId={9} title="Сёгун" parts={[file]} startSec={0} onClose={() => {}} start={start as never} tracks={tracks} />)
    await waitFor(() => expect(start).toHaveBeenCalled())
    return start
  }
  const openMenu = async () => {
    await userEvent.click(screen.getByRole('button', { name: 'Аудио и субтитры' }))
    return screen.findByRole('menu', { name: 'Аудио и субтитры' })
  }

  it('starts with the voice remembered for the series and its subtitles; the profile’s look', async () => {
    const start = await play({ prefs, series: { audio: { lang: 'rus', title: 'Кубик в кубе' }, subtitle: { lang: 'rus', title: 'Полные' }, subtitlesOff: false }, offsets: {} })
    expect(start.mock.calls[0]![1]).toMatchObject({ audioIndex: 3 })
    const menu = await openMenu()
    await waitFor(() => expect(within(menu).getByRole('menuitemradio', { name: /Русский.*Полные/ })).toHaveAttribute('aria-checked', 'true'))
    const line = await screen.findByText('Привет')
    expect(line.closest('[data-size]')).toHaveAttribute('data-size', 'xl')
    expect(line.closest('[data-bg]')).toHaveAttribute('data-bg', 'solid')
  })

  it('without a series choice: the profile’s language, forced subtitles only', async () => {
    const start = await play({ prefs, series: null, offsets: {} })
    expect(start.mock.calls[0]![1]).toMatchObject({ audioIndex: 2 })
    const menu = await openMenu()
    await waitFor(() => expect(within(menu).getByRole('menuitemradio', { name: /Надписи|Только надписи/ })).toHaveAttribute('aria-checked', 'true'))
  })

  it('a choice in the menu is remembered for the series', async () => {
    await play({ prefs, series: null, offsets: {} })
    let menu = await openMenu()
    await userEvent.click(within(menu).getByRole('menuitemradio', { name: /Кубик в Кубе/ }))
    await waitFor(() => expect(calls.some((c) => c.method === 'PUT' && c.url.endsWith('/api/items/9/track-prefs'))).toBe(true))
    expect(calls.find((c) => c.method === 'PUT' && c.url.endsWith('/track-prefs'))!.body).toEqual({ audio: { lang: 'rus', title: 'Кубик в Кубе' } })
    menu = await openMenu()
    await userEvent.click(within(menu).getByRole('menuitemradio', { name: 'Выключены' }))
    await waitFor(() => expect(calls.filter((c) => c.method === 'PUT' && c.url.endsWith('/track-prefs')).map((c) => c.body)).toContainEqual({ subtitleOff: true }))
  })

  it('«Субтитры раньше / позже» shows the file’s offset, moves it and saves it for the file', async () => {
    await play({ prefs: { ...prefs, subMode: 'always', subLangs: ['en'] }, series: null, offsets: { '5': 500 } })
    const menu = await openMenu()
    await waitFor(() => expect(within(menu).getByText('Сдвиг: +0,50 с')).toBeInTheDocument())
    await userEvent.click(within(menu).getByRole('menuitem', { name: 'Субтитры позже на 0,25 с' }))
    expect(within(menu).getByText('Сдвиг: +0,75 с')).toBeInTheDocument()
    // the menu stays open for the next press
    await userEvent.click(within(menu).getByRole('menuitem', { name: 'Субтитры раньше на 0,25 с' }))
    await userEvent.click(within(menu).getByRole('menuitem', { name: 'Субтитры раньше на 0,25 с' }))
    expect(within(menu).getByText('Сдвиг: +0,25 с')).toBeInTheDocument()
    await waitFor(() => expect(calls.find((c) => c.url.endsWith('/api/files/5/subtitle-offset'))?.body).toEqual({ offsetMs: 250 }), { timeout: 2000 })
    const reset = within(menu).getByRole('menuitem', { name: 'Сбросить сдвиг' })
    await userEvent.click(reset)
    expect(within(menu).getByText('Без сдвига')).toBeInTheDocument()
    // still there (disabled): the focus stays on it
    expect(reset).toBeInTheDocument()
    expect(reset).toHaveAttribute('aria-disabled', 'true')
    expect(reset).toHaveFocus()
  })
})

describe('VideoPlayer: the audio of each part', () => {
  beforeEach(() => {
    session.reset()
    localStorage.clear()
    vi.spyOn(HTMLMediaElement.prototype, 'play').mockResolvedValue(undefined)
    vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {})
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => (String(input).endsWith('/api/login') ? json(mom) : json({}))))
  })
  afterEach(() => vi.unstubAllGlobals())

  const t = (index: number, lang: string, title = '', def = false): Track => ({ index, codec: 'aac', lang, title, default: def })
  const cd = (id: number, audio: Track[]): MediaFile => ({ id, name: `film cd${id}.mkv`, size: 1, durationMs: 60_000, audio, subs: [] })
  const run = async (parts: MediaFile[], startSec: number) => {
    await session.login(2)
    const start = vi.fn(async (f: MediaFile, body: { audioIndex?: number }) => ({
      mode: 'direct', url: `/api/stream/${f.id}`, startSec: 0, durationMs: 60_000,
      audioIndex: body.audioIndex ?? (f.audio.find((x) => x.default) ?? f.audio[0])!.index, audio: f.audio, subtitles: [],
    }))
    render(<VideoPlayer itemId={9} title="Фильм" parts={parts} startSec={startSec} onClose={() => {}} start={start as never} tracks={{ prefs, series: null, offsets: {} }} />)
    await waitFor(() => expect(start).toHaveBeenCalled())
    return start
  }

  it('resuming in a later part picks among that part’s own tracks', async () => {
    const parts = [cd(1, [t(1, 'eng', '', true), t(2, 'rus')]), cd(2, [t(1, 'eng', '', true), t(3, 'rus')])]
    const start = await run(parts, 90)
    expect(start.mock.calls[0]![0].id).toBe(2)
    expect(start.mock.calls[0]![1]).toMatchObject({ audioIndex: 3 })
  })

  it('the next part keeps the voice that played, by its title, even untagged and even when it was part 1’s default', async () => {
    const kubik = 'Кубик в кубе'
    const parts = [cd(1, [t(1, 'und', kubik, true), t(2, 'eng')]), cd(2, [t(1, 'und', 'Другая'), t(2, 'eng', '', true), t(5, 'und', kubik)])]
    const start = await run(parts, 0)
    expect(start.mock.calls[0]![1].audioIndex).toBeUndefined() // part 1's default is the profile's Russian
    const video = document.querySelector('video')!
    Object.defineProperty(video, 'currentTime', { configurable: true, writable: true, value: 59.9 })
    act(() => {
      fireEvent.ended(video)
    })
    await waitFor(() => expect(start).toHaveBeenCalledTimes(2))
    expect(start.mock.calls[1]![0].id).toBe(2)
    expect(start.mock.calls[1]![1]).toMatchObject({ audioIndex: 5 })
  })
})
