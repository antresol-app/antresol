// The engine follows the signed-in profile: nothing of one profile's
// listening plays on, shows or is saved under the next one.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { request } from '../../../api/client'
import { session } from '../../../state/session'
import { audio, resetAudioForTests, type QueueItem } from '../engine'

const PENDING_KEY = 'vault.pending-progress.v1'

type Call = { url: string; init?: RequestInit }
let calls: Call[] = []
/** Answers of the fake server by path (default 204). */
let answer: (url: string, init?: RequestInit) => Response | undefined

const user = (id: number) => ({ id, name: id === 1 ? 'Анна' : 'Миша', hasPassword: false, isAdmin: id === 1, isKid: id !== 1, maxRating: null })

function book(): QueueItem[] {
  return [0, 1].map((i) => ({
    key: `b7f${i}`,
    fileId: 20 + i,
    title: 'Книга',
    artist: 'Автор',
    cover: '',
    durationSec: 60,
    src: `/api/audio/${20 + i}?accept=mp3`,
    bookStartMs: i * 60_000,
  }))
}

const els = () => [...document.querySelectorAll<HTMLAudioElement>('audio[data-vault-audio]')]
const active = () => els().find((e) => e.hasAttribute('data-active'))!
const fire = (el: HTMLMediaElement, type: string) => el.dispatchEvent(new Event(type))
function heard(el: HTMLMediaElement, upTo: number) {
  for (let t = 1; t <= upTo; t++) {
    Object.defineProperty(el, 'currentTime', { configurable: true, writable: true, value: t })
    Object.defineProperty(el, 'duration', { configurable: true, writable: true, value: 60 })
    fire(el, 'timeupdate')
  }
}
const puts = () => calls.filter((c) => c.url.startsWith('/api/progress/') && c.init?.method === 'PUT')
const plays = () => calls.filter((c) => c.url === '/api/music/plays')
const pending = () => JSON.parse(localStorage.getItem(PENDING_KEY) ?? '[]') as { userId: number; itemId: number; positionMs: number }[]

async function signIn(id: number) {
  await session.login(id)
  expect(session.get().user?.id).toBe(id)
}

beforeEach(() => {
  resetAudioForTests()
  session.reset()
  localStorage.clear()
  calls = []
  answer = () => undefined
  vi.spyOn(HTMLMediaElement.prototype, 'play').mockImplementation(() => Promise.resolve())
  vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {})
  vi.spyOn(HTMLMediaElement.prototype, 'load').mockImplementation(() => {})
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      calls.push({ url, init })
      const a = answer(url, init)
      if (a) return a
      if (url === '/api/login') {
        const { userId } = JSON.parse(String(init?.body)) as { userId: number }
        return new Response(JSON.stringify(user(userId)), { status: 200 })
      }
      return new Response(null, { status: 204 })
    }),
  )
})

afterEach(() => {
  resetAudioForTests()
  session.reset()
  localStorage.clear()
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

describe('the engine follows the session', () => {
  it('sign-out saves the book under the old profile first, then silences and empties the player', async () => {
    await signIn(1)
    audio.play(book(), 0, { kind: 'audiobook', bookId: 7 })
    fire(active(), 'playing')
    heard(active(), 12)
    calls = []

    // what the app sees when signed-out is published
    let seen: { queue: number; src: (string | null)[] } | null = null
    const off = session.subscribe(() => {
      if (session.get().status === 'signed-out') seen = { queue: audio.get().queue.length, src: els().map((e) => e.getAttribute('src')) }
    })
    await session.logout()
    off()

    const put = calls.findIndex((c) => c.url === '/api/progress/7' && c.init?.method === 'PUT')
    const out = calls.findIndex((c) => c.url === '/api/logout')
    expect(put).toBeGreaterThanOrEqual(0)
    expect(put).toBeLessThan(out) // still with the old cookie
    expect(JSON.parse(String(calls[put]!.init?.body))).toMatchObject({ positionMs: 12_000, durationMs: 120_000 })
    expect(seen).toEqual({ queue: 0, src: [null, null] })
    expect(audio.get().playing).toBe(false)
    expect(audio.get().kind).toBeNull()

    // the next profile gets nothing of it
    vi.useFakeTimers()
    await signIn(2)
    calls = []
    vi.advanceTimersByTime(30_000)
    window.dispatchEvent(new Event('pagehide'))
    expect(puts()).toHaveLength(0)
    expect(pending().filter((p) => p.userId === 2)).toHaveLength(0)
  })

  it('an expired session drops the live save but keeps it queued for the old profile', async () => {
    await signIn(1)
    audio.play(book(), 1, { kind: 'audiobook', bookId: 7 })
    fire(active(), 'playing')
    heard(active(), 5)
    calls = []
    answer = (url) => (url === '/api/books/home' ? new Response('{"error":"Войдите"}', { status: 401 }) : undefined)

    let queueWhenOut = -1
    const off = session.subscribe(() => {
      if (session.get().status === 'signed-out') queueWhenOut = audio.get().queue.length
    })
    await request('GET', '/api/books/home').catch(() => {})
    off()

    expect(session.get().status).toBe('signed-out')
    expect(queueWhenOut).toBe(0)
    expect(puts()).toHaveLength(0) // the cookie is gone: nothing goes out under it
    expect(pending()).toEqual([expect.objectContaining({ userId: 1, itemId: 7, positionMs: 65_000 })])
  })

  it('another profile signing in over a live one stops the music before it is published', async () => {
    await signIn(1)
    audio.play(
      [{ key: 't1', trackId: 1, title: 'Песня', artist: 'А', album: 'Б', cover: '', durationSec: 200, src: '/api/tracks/1/stream' }],
      0,
    )
    fire(active(), 'playing')
    let queueWhenB = -1
    const off = session.subscribe(() => {
      if (session.get().user?.id === 2) queueWhenB = audio.get().queue.length
    })
    await signIn(2)
    off()
    expect(queueWhenB).toBe(0)
    heard(els()[0]!, 40)
    expect(plays()).toHaveLength(0)
  })

  it('signing in again as the same profile keeps playing', async () => {
    await signIn(1)
    audio.play(book(), 0, { kind: 'audiobook', bookId: 7 })
    await signIn(1)
    expect(audio.get().queue).toHaveLength(2)
    expect(audio.get().playing).toBe(true)
  })
})
