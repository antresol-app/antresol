import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { audio, resetAudioForTests, type QueueItem } from '../engine'
import { seekAbsolute } from '../audiobook'
import { armSleep, cancelSleep, sleepRemaining } from '../sleep'

const els = () => [...document.querySelectorAll<HTMLAudioElement>('audio[data-vault-audio]')]
const active = () => els().find((e) => e.hasAttribute('data-active'))!

function media(el: HTMLMediaElement, v: { currentTime?: number; duration?: number }) {
  if (v.currentTime !== undefined) Object.defineProperty(el, 'currentTime', { configurable: true, writable: true, value: v.currentTime })
  if (v.duration !== undefined) Object.defineProperty(el, 'duration', { configurable: true, writable: true, value: v.duration })
}
const fire = (el: HTMLMediaElement, type: string) => el.dispatchEvent(new Event(type))

/** Two 60-second files of a book, one chapter each. */
function book(): QueueItem[] {
  const chapters = [
    { idx: 0, title: 'Глава 1', fileId: 1, startMs: 0, endMs: 60_000 },
    { idx: 1, title: 'Глава 2', fileId: 2, startMs: 60_000, endMs: 120_000 },
  ]
  return [1, 2].map((id, i) => ({
    key: `b7f${id}`,
    fileId: id,
    title: 'Книга',
    artist: 'Автор',
    cover: '',
    durationSec: 60,
    src: `/api/audio/${id}?accept=mp3`,
    bookStartMs: i * 60_000,
    chapters,
  }))
}

beforeEach(() => {
  vi.useFakeTimers()
  resetAudioForTests()
  vi.spyOn(HTMLMediaElement.prototype, 'play').mockImplementation(() => Promise.resolve())
  vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {})
  vi.spyOn(HTMLMediaElement.prototype, 'load').mockImplementation(() => {})
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => new Response(null, { status: 204 })),
  )
})

afterEach(() => {
  cancelSleep()
  resetAudioForTests()
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

describe('sleep timer', () => {
  it('15 min pauses at 900 s with fade', () => {
    audio.play(book(), 0, { kind: 'audiobook', bookId: 7 })
    fire(active(), 'playing')
    armSleep({ kind: 'minutes', endsAt: Date.now() + 15 * 60_000 })
    expect(sleepRemaining(Date.now())).toBe(900_000)

    vi.advanceTimersByTime(880_000)
    expect(audio.get().playing).toBe(true)
    expect(active().volume).toBe(1)

    vi.advanceTimersByTime(15_000) // 5 s left: half way down
    expect(audio.get().playing).toBe(true)
    expect(active().volume).toBeGreaterThan(0.3)
    expect(active().volume).toBeLessThan(0.7)

    vi.advanceTimersByTime(5_000)
    expect(audio.get().playing).toBe(false)
    expect(active().volume).toBe(1) // back for the next play
    expect(sleepRemaining(Date.now())).toBeNull()
  })

  it('chapter timer pauses at chapter end', () => {
    audio.play(book(), 0, { kind: 'audiobook', bookId: 7 })
    const el = active()
    fire(el, 'playing')
    media(el, { currentTime: 40, duration: 60 })
    fire(el, 'timeupdate')
    armSleep({ kind: 'chapter' })
    vi.advanceTimersByTime(300)
    expect(sleepRemaining(Date.now())).toBe(20_000)

    media(el, { currentTime: 55 })
    fire(el, 'timeupdate')
    vi.advanceTimersByTime(300)
    expect(audio.get().playing).toBe(true)
    expect(el.volume).toBeLessThan(0.7)

    // the file ends and the next one starts: the chapter is over
    media(el, { currentTime: 60 })
    fire(el, 'ended')
    vi.advanceTimersByTime(300)
    expect(audio.get().playing).toBe(false)
    expect(audio.get().index).toBe(1)
    expect(sleepRemaining(Date.now())).toBeNull()
    expect(active().volume).toBe(1)
  })

  it('a jump to another chapter moves the chapter timer there', () => {
    audio.play(book(), 0, { kind: 'audiobook', bookId: 7 })
    const el = active()
    fire(el, 'playing')
    media(el, { currentTime: 40, duration: 60 })
    fire(el, 'timeupdate')
    armSleep({ kind: 'chapter' })
    vi.advanceTimersByTime(300)
    seekAbsolute(60_000) // «Следующая глава»
    vi.advanceTimersByTime(300)
    expect(audio.get().playing).toBe(true)
    expect(audio.get().index).toBe(1)
    expect(sleepRemaining(Date.now())).toBe(60_000)
    expect(active().volume).toBe(1)
  })

  it('the chapter countdown runs in listening time at the book speed', () => {
    audio.play(book(), 0, { kind: 'audiobook', bookId: 7, rate: 2 })
    const el = active()
    fire(el, 'playing')
    media(el, { currentTime: 40, duration: 60 })
    fire(el, 'timeupdate')
    armSleep({ kind: 'chapter' })
    expect(sleepRemaining(Date.now())).toBe(10_000)
  })

  it('cancel keeps playing', () => {
    audio.play(book(), 0, { kind: 'audiobook', bookId: 7 })
    fire(active(), 'playing')
    armSleep({ kind: 'minutes', endsAt: Date.now() + 15 * 60_000 })
    vi.advanceTimersByTime(895_000)
    expect(active().volume).toBeLessThan(1)
    cancelSleep()
    expect(active().volume).toBe(1)
    expect(sleepRemaining(Date.now())).toBeNull()
    vi.advanceTimersByTime(30_000)
    expect(audio.get().playing).toBe(true)
  })
})
