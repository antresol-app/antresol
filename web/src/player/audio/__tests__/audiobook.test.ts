import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { BookDetail } from '../../../api/types'
import { absoluteMs, bookQueue, playBook, seekAbsolute } from '../audiobook'
import { audio, resetAudioForTests, type AudioState } from '../engine'

const els = () => [...document.querySelectorAll<HTMLAudioElement>('audio[data-vault-audio]')]
const active = () => els().find((e) => e.hasAttribute('data-active'))!
function media(el: HTMLMediaElement, v: { currentTime?: number; duration?: number }) {
  if (v.currentTime !== undefined) Object.defineProperty(el, 'currentTime', { configurable: true, writable: true, value: v.currentTime })
  if (v.duration !== undefined) Object.defineProperty(el, 'duration', { configurable: true, writable: true, value: v.duration })
}
const fire = (el: HTMLMediaElement, type: string) => el.dispatchEvent(new Event(type))

function detail(over: Partial<BookDetail> = {}): BookDetail {
  return {
    card: {
      id: 7,
      kind: 'audiobook',
      title: 'Книга',
      subtitle: 'Автор',
      poster: '/api/images/local/7/poster',
      backdrop: '',
      logo: '',
      progress: 0,
      onServer: true,
    },
    kind: 'audiobook',
    authors: ['Автор'],
    narrator: 'Чтец',
    series: null,
    description: '',
    year: 0,
    lang: '',
    files: [
      { id: 21, format: 'MP3', size: 1000, durationMs: 60_000 },
      { id: 22, format: 'MP3', size: 1000, durationMs: 60_000 },
    ],
    seriesId: 0,
    nextId: 0,
    readFileId: 0,
    progress: null,
    want: false,
    durationMs: 120_000,
    chapters: [
      { idx: 0, title: 'Глава 1', fileId: 21, startMs: 0, endMs: 60_000 },
      { idx: 1, title: 'Глава 2', fileId: 22, startMs: 60_000, endMs: 120_000 },
    ],
    volumes: [],
    direction: '',
    ...over,
  }
}

type Call = { url: string; init?: RequestInit }
let calls: Call[]

beforeEach(() => {
  resetAudioForTests()
  calls = []
  vi.spyOn(HTMLMediaElement.prototype, 'play').mockImplementation(() => Promise.resolve())
  vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {})
  vi.spyOn(HTMLMediaElement.prototype, 'load').mockImplementation(() => {})
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      calls.push({ url: String(input), init })
      return new Response(null, { status: 204 })
    }),
  )
})

afterEach(() => {
  resetAudioForTests()
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

const saves = () => calls.filter((c) => c.url === '/api/progress/7' && c.init?.method === 'PUT')
const body = (c: Call) => JSON.parse(String(c.init?.body)) as { positionMs: number; durationMs: number }

describe('audiobooks in the engine', () => {
  it('absolute position across files', () => {
    const files = [{ durationMs: 60_000 }, { durationMs: 60_000 }]
    expect(absoluteMs({ index: 1, positionSec: 10 } as AudioState, files)).toBe(70_000)
    expect(absoluteMs({ index: 0, positionSec: 12.5 } as AudioState, files)).toBe(12_500)
    const q = bookQueue(detail(), ['mp3'])
    expect(q).toHaveLength(2)
    expect(q[1]!.src).toBe('/api/audio/22?accept=mp3')
    expect(q[1]!.bookStartMs).toBe(60_000)
  })

  it('resume maps absolute ms to file and offset', () => {
    playBook(detail({ progress: { fraction: 70 / 120, locator: '', positionMs: 70_000, durationMs: 120_000, finished: false } }))
    const s = audio.get()
    expect(s.kind).toBe('audiobook')
    expect(s.bookId).toBe(7)
    expect(s.index).toBe(1)
    expect(s.positionSec).toBe(10)
    expect(active().getAttribute('src')).toMatch(/^\/api\/audio\/22\?/)

    // back into the first file
    seekAbsolute(30_000)
    expect(audio.get().index).toBe(0)
    expect(audio.get().positionSec).toBe(30)
    // within the file
    seekAbsolute(45_000)
    expect(audio.get().index).toBe(0)
    expect(audio.get().positionSec).toBe(45)
  })

  it('the book speed survives loading another file', () => {
    localStorage.setItem('vault.audiobook.rate', '1.5')
    try {
      playBook(detail())
      const el = active()
      // a new src resets playbackRate to defaultPlaybackRate
      expect(el.defaultPlaybackRate).toBe(1.5)
      expect(el.playbackRate).toBe(1.5)
      el.playbackRate = 1 // what the browser's load algorithm does
      fire(el, 'loadedmetadata')
      expect(el.playbackRate).toBe(1.5)
      audio.jumpTo(1, 0)
      expect(active().defaultPlaybackRate).toBe(1.5)
      expect(audio.get().rate).toBe(1.5)
    } finally {
      localStorage.removeItem('vault.audiobook.rate')
    }
  })

  it('progress saved every 10 s with keepalive on pagehide', () => {
    vi.useFakeTimers()
    playBook(detail())
    const el = active()
    fire(el, 'playing')
    // only real listening is saved: nothing yet after opening
    audio.pause()
    window.dispatchEvent(new Event('pagehide'))
    expect(saves()).toHaveLength(0)
    audio.toggle()
    for (const t of [1, 2, 3]) {
      media(el, { currentTime: t, duration: 60 })
      fire(el, 'timeupdate')
    }
    vi.advanceTimersByTime(9_000)
    expect(saves()).toHaveLength(0)
    vi.advanceTimersByTime(1_000)
    expect(saves()).toHaveLength(1)
    expect(body(saves()[0]!)).toMatchObject({ positionMs: 3000, durationMs: 120_000 })

    audio.jumpTo(1, 5)
    window.dispatchEvent(new Event('pagehide'))
    const last = saves().at(-1)!
    expect(last.init?.keepalive).toBe(true)
    expect(body(last)).toMatchObject({ positionMs: 65_000, durationMs: 120_000 })

    // and on pause
    media(active(), { currentTime: 8 })
    fire(active(), 'timeupdate')
    audio.pause()
    expect(body(saves().at(-1)!)).toMatchObject({ positionMs: 68_000 })
  })

  it('a jump to another profile’s place is saved as the place before it until it plays on', () => {
    vi.useFakeTimers()
    playBook(detail())
    const el = active()
    fire(el, 'playing')
    for (const t of [1, 2, 3]) {
      media(el, { currentTime: t, duration: 60 })
      fire(el, 'timeupdate')
    }
    // Мама is at 1:35 of the book (35 s into its second file)
    audio.holdBook({ pos: 3000, from: 95_000 })
    seekAbsolute(95_000)
    audio.pause()
    expect(body(saves().at(-1)!)).toMatchObject({ positionMs: 3000 })
    // a second jump (NowPlaying) reads the place held, once the pill is gone
    expect(audio.bookHold()).toEqual({ pos: 3000, from: 95_000 })

    // played on there for a few seconds: now it is this profile's place
    audio.toggle()
    media(active(), { currentTime: 41, duration: 60 })
    fire(active(), 'timeupdate')
    audio.pause()
    expect(body(saves().at(-1)!)).toMatchObject({ positionMs: 101_000 })
    expect(audio.bookHold()).toBeNull()
  })
})

