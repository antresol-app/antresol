import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { audio, resetAudioForTests } from '../../../player/audio/engine'
import { playShuffled, shuffleQuery } from '../shuffle'
import { json, song } from './fixtures'

beforeEach(() => {
  resetAudioForTests()
  vi.spyOn(HTMLMediaElement.prototype, 'play').mockImplementation(() => Promise.resolve())
  vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {})
})
afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
  resetAudioForTests()
})

describe('shuffleQuery', () => {
  it('shuffleQueryParams', () => {
    expect(shuffleQuery({ kind: 'all' })).toBe('?source=all')
    expect(shuffleQuery({ kind: 'genre', genre: 'Рок' })).toBe('?source=genre&genre=%D0%A0%D0%BE%D0%BA')
    expect(shuffleQuery({ kind: 'artist', artistId: 8 })).toBe('?source=artist&artist=8')
  })
})

describe('playShuffled', () => {
  it('playShuffledPlaysWithShuffle', async () => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL) => json({ tracks: [1, 2, 3, 4, 5].map((i) => song(i)) }))
    vi.stubGlobal('fetch', fetchMock)
    expect(await playShuffled({ kind: 'all' })).toBeNull()
    expect(String(fetchMock.mock.calls[0]![0])).toBe('/api/music/shuffle?source=all')
    const active = document.querySelector<HTMLAudioElement>('audio[data-vault-audio][data-active]')!
    expect(active.getAttribute('src')).toMatch(/^\/api\/tracks\/10[1-5]\/stream/)
    active.dispatchEvent(new Event('playing'))
    const s = audio.get()
    expect(s.queue).toHaveLength(5)
    expect(new Set(s.queue.map((q) => q.trackId))).toEqual(new Set([101, 102, 103, 104, 105]))
    expect(s.shuffle).toBe(true)
    expect(s.playing).toBe(true)
  })

  it('returns the server message on an HTTP error', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json({ error: 'Сервер занят' }, 503)))
    expect(await playShuffled({ kind: 'genre', genre: 'Рок' })).toBe('Сервер занят')
    expect(audio.get().queue).toHaveLength(0)
  })

  it('a network error leaves the queue as it was', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json({ tracks: [song(9)] })))
    await playShuffled({ kind: 'all' })
    const before = audio.get().queue
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => {
        throw new TypeError('Failed to fetch')
      }),
    )
    expect(await playShuffled({ kind: 'artist', artistId: 8 })).toBe('Не удалось перемешать песни. Проверьте связь с сервером.')
    expect(audio.get().queue).toBe(before)
  })
})
