// /play/input/:id — an addon's video in the core player: the props it gets
// from GET /api/play/input/{id}, the addon's own `playerNotes` while the
// video loads, «Видео больше недоступно» once the input is gone, and the
// track choices remembered for the input's library item only.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createElement } from 'react'
import { MemoryRouter, Route, Routes } from 'react-router'
import { ApiError } from '../../api/client'
import type { PlayerInput, Track } from '../../api/types'
import type { VideoPlayerProps } from '../../player/VideoPlayer'
import { session } from '../../state/session'
import { registerAddon, resetAddonsForTests, setAddonUser } from '../../addons/registry'
import { setLanguagePref } from '../../i18n'
import { rememberSettled } from '../../player/rememberQueue'

const h = vi.hoisted(() => ({
  seen: [] as unknown[],
  getInput: vi.fn(),
  inputPlay: vi.fn(),
  saveInputProgress: vi.fn(() => Promise.resolve()),
}))
vi.mock('../../api/inputs', async (orig) => ({
  ...(await orig<typeof import('../../api/inputs')>()),
  getInput: h.getInput,
  inputPlay: h.inputPlay,
  saveInputProgress: h.saveInputProgress,
}))
vi.mock('../../player/VideoPlayer', async (orig) => {
  const m = await orig<typeof import('../../player/VideoPlayer')>()
  return {
    ...m,
    VideoPlayer: (p: VideoPlayerProps) => {
      h.seen.push(p)
      return createElement(m.VideoPlayer, p)
    },
  }
})
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

import { InputPlayer } from '../InputPlayer'

const mom = { id: 2, name: 'Мама', avatar: 'grad-1', hasPassword: false, isAdmin: false, isKid: false, maxRating: null }
const json = (b: unknown, status = 200) => new Response(b === undefined ? null : JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } })
const input = (over: Partial<PlayerInput> = {}): PlayerInput => ({
  id: 'abc', addonId: 'cloud', name: 'Show/Show.S01E01.mkv', itemId: 0, slow: true, startSec: 42, ...over,
})
const audio: Track[] = [
  { index: 1, codec: 'aac', lang: 'eng', title: '', default: true },
  { index: 2, codec: 'aac', lang: 'rus', title: 'Кубик в Кубе', default: false },
]
type Call = { url: string; method: string }
let calls: Call[]
const last = () => h.seen[h.seen.length - 1] as VideoPlayerProps

const page = (id: string) => (
  <MemoryRouter initialEntries={[`/play/input/${id}`]}>
    <Routes>
      <Route path="/play/input/:id" element={<InputPlayer />} />
    </Routes>
  </MemoryRouter>
)

function show(id = 'abc') {
  return render(page(id))
}

describe('input player', () => {
  beforeEach(async () => {
    session.reset()
    resetAddonsForTests()
    localStorage.clear()
    h.seen.length = 0
    h.getInput.mockReset()
    h.inputPlay.mockReset()
    h.inputPlay.mockImplementation(() => new Promise(() => {}))
    h.saveInputProgress.mockClear()
    calls = []
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    vi.spyOn(console, 'error').mockImplementation(() => {})
    vi.spyOn(HTMLMediaElement.prototype, 'play').mockResolvedValue(undefined)
    vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {})
    vi.stubGlobal(
      'fetch',
      vi.fn(async (u: RequestInfo | URL, init?: RequestInit) => {
        const url = String(u)
        calls.push({ url, method: init?.method ?? 'GET' })
        if (url.endsWith('/api/login')) return json(mom)
        if (url.includes('/track-prefs') && init?.method === 'PUT') return json(undefined, 204)
        return json({})
      }),
    )
    await session.login(2)
  })
  afterEach(() => {
    vi.unstubAllGlobals()
    resetAddonsForTests()
    session.reset()
  })

  it('hands the input to VideoPlayer', async () => {
    h.getInput.mockResolvedValue(input({ itemId: 7 }))
    show()
    await waitFor(() => expect(h.seen.length).toBeGreaterThan(0))
    const p = last()
    expect(h.getInput).toHaveBeenCalledWith('abc', expect.anything())
    expect(p.itemId).toBe(7)
    expect(p.startSec).toBe(42)
    expect(p.title).toBe('Show.S01E01')
    expect(p.slowSource).toBe(true)
    expect(p.parts).toEqual([{ id: 0, name: 'Show/Show.S01E01.mkv', size: 0, audio: [], subs: [] }])
    expect(screen.getByRole('heading', { name: 'Show.S01E01' })).toBeInTheDocument()
    expect(await screen.findByText('Загружается…')).toBeInTheDocument()
    // the play decision goes to the input
    await waitFor(() => expect(h.inputPlay).toHaveBeenCalled())
    expect(h.inputPlay.mock.calls[0]![0]).toBe('abc')
    // the position goes to the input's progress
    await p.saveProgress!(1000, 2000, true)
    expect(h.saveInputProgress).toHaveBeenCalledWith('abc', 1000, 2000, true)
    // a re-render keeps the same parts (a new array would restart playback)
    const first = h.seen[0] as VideoPlayerProps
    expect(last().parts).toBe(first.parts)
  })

  it('the addon’s playerNotes show while it loads; another addon’s and admin-only ones do not', async () => {
    act(() => setAddonUser({ isAdmin: false }))
    registerAddon('cloud', {
      playerNotes: [
        { element: ({ input }) => <span>Готовим {input.name}</span> },
        { admin: true, element: () => <span>Только админу</span> },
      ],
    })
    registerAddon('other', { playerNotes: [{ element: () => <span>Чужая заметка</span> }] })
    h.getInput.mockResolvedValue(input())
    show()
    expect(await screen.findByText('Готовим Show/Show.S01E01.mkv')).toBeInTheDocument()
    expect(screen.queryByText('Только админу')).toBeNull()
    expect(screen.queryByText('Чужая заметка')).toBeNull()
    expect(screen.queryByText('Загружается…')).toBeNull()
  })

  it('a note that throws leaves the page working', async () => {
    registerAddon('cloud', {
      playerNotes: [
        {
          element: () => {
            throw new Error('boom')
          },
        },
      ],
    })
    h.getInput.mockResolvedValue(input())
    show()
    expect(await screen.findByRole('heading', { name: 'Show.S01E01' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Закрыть плеер' })).toBeInTheDocument()
    // the plain note takes its place
    expect(await screen.findByText('Загружается…')).toBeInTheDocument()
  })

  it('a gone input: «Видео больше недоступно» and «Назад»', async () => {
    h.getInput.mockRejectedValue(new ApiError(404, 'Видео не найдено'))
    show()
    expect(await screen.findByText('Видео больше недоступно')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Назад' })).toBeInTheDocument()
    expect(h.seen).toHaveLength(0)
  })

  const chooseRussian = async () => {
    h.inputPlay.mockImplementation(async (_id: string, body: { audioIndex?: number }) => ({
      mode: 'direct', url: '/api/play/input/abc/direct', startSec: 0, durationMs: 60_000, audioIndex: body.audioIndex ?? 1, audio, subtitles: [],
    }))
    show()
    await waitFor(() => expect(h.inputPlay).toHaveBeenCalled())
    await userEvent.click(await screen.findByRole('button', { name: 'Аудио и субтитры' }))
    const menu = await screen.findByRole('menu', { name: 'Аудио и субтитры' })
    await userEvent.click(within(menu).getByRole('menuitemradio', { name: /Кубик в Кубе/ }))
    await waitFor(() => expect(h.inputPlay).toHaveBeenCalledTimes(2))
  }

  it('a track chosen with an item is remembered for it', async () => {
    h.getInput.mockResolvedValue(input({ itemId: 7 }))
    await chooseRussian()
    await waitFor(() => expect(calls.some((c) => c.method === 'PUT' && c.url.endsWith('/api/items/7/track-prefs'))).toBe(true))
  })

  it('without an item nothing is remembered', async () => {
    h.getInput.mockResolvedValue(input({ itemId: 0 }))
    await chooseRussian()
    // the choice is queued (or not) when it is made: once the queue settles, a PUT would have gone
    await act(() => rememberSettled())
    expect(calls.some((c) => c.url.includes('/track-prefs') && c.method === 'PUT')).toBe(false)
  })

  it('a language switch does not restart playback', async () => {
    h.getInput.mockResolvedValue(input())
    show()
    await waitFor(() => expect(h.inputPlay).toHaveBeenCalled())
    const parts = last().parts
    act(() => setLanguagePref('en'))
    expect(await screen.findByRole('button', { name: 'Close player' })).toBeInTheDocument()
    expect(h.getInput).toHaveBeenCalledTimes(1)
    expect(h.inputPlay).toHaveBeenCalledTimes(1)
    expect(last().parts).toBe(parts)
  })

  it('a failure to open is shown in the current language', async () => {
    h.getInput.mockRejectedValue(new Error('network'))
    show()
    expect(await screen.findByText('Не удалось открыть видео.')).toBeInTheDocument()
    act(() => setLanguagePref('en'))
    expect(await screen.findByText('Couldn’t open the video.')).toBeInTheDocument()
    expect(h.getInput).toHaveBeenCalledTimes(1)
  })

  it('a re-render keeps the same waiting note (the notes are memoised)', async () => {
    registerAddon('cloud', { playerNotes: [{ element: () => <span>Готовим</span> }] })
    h.getInput.mockResolvedValue(input())
    const { rerender } = show()
    expect(await screen.findByText('Готовим')).toBeInTheDocument()
    const n = h.seen.length
    const note = last().waitingNote
    rerender(page('abc')) // a re-render with nothing changed
    expect(h.seen.length).toBeGreaterThan(n)
    expect(last().waitingNote).toBe(note)
  })

  it('note actions sit in the top bar; useProgress feeds sourceProgress', async () => {
    let got = 0
    registerAddon('cloud', {
      playerNotes: [
        {
          element: () => <span>Готовим</span>,
          actions: ({ input }) => <button type="button">Оставить {input.id}</button>,
          useProgress: (i) => (i.id === 'abc' ? got : -1),
        },
        // one that throws costs only itself
        {
          element: () => null,
          actions: () => {
            throw new Error('boom')
          },
          useProgress: () => {
            throw new Error('boom')
          },
        },
      ],
    })
    registerAddon('other', { playerNotes: [{ element: () => null, actions: () => <button type="button">Чужая</button>, useProgress: () => 999 }] })
    h.getInput.mockResolvedValue(input())
    const { rerender } = show()
    const button = await screen.findByRole('button', { name: 'Оставить abc' })
    expect(button.closest('header')).not.toBeNull()
    expect(screen.queryByRole('button', { name: 'Чужая' })).toBeNull()
    expect(last().sourceProgress!()).toBe(0)
    got = 5000
    rerender(page('abc'))
    await waitFor(() => expect(last().sourceProgress!()).toBe(5000))
  })

  it('without notes that report: no sourceProgress, no top actions', async () => {
    registerAddon('cloud', { playerNotes: [{ element: () => <span>Готовим</span> }] })
    h.getInput.mockResolvedValue(input())
    show()
    expect(await screen.findByText('Готовим')).toBeInTheDocument()
    expect(last().sourceProgress).toBeUndefined()
    expect(last().topActions).toBeFalsy()
  })
})
