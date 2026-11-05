import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { session } from '../../state/session'
import { LOCK_PATH, OFFLINE_CACHE, findSaved } from '../saved'
import {
  bindOfflineToSession,
  discardProgress,
  isSavedMedia,
  listOffline,
  noSpace,
  offlineAvailable,
  rememberProgress,
  removeOffline,
  saveOffline,
  sendPendingProgress,
} from '../store'
import { FakeCaches } from './fakeCaches'

const json = (v: unknown, init: ResponseInit = {}) =>
  new Response(JSON.stringify(v), { status: 200, headers: { 'Content-Type': 'application/json' }, ...init })

const ALBUM = {
  card: { id: 7, kind: 'album', title: 'Первый альбом', subtitle: 'Артист', poster: 'album-7.jpg', backdrop: null, logo: null, progress: 0, onServer: true },
  artist: { id: 3, name: 'Артист' },
  year: 2020,
  genres: [],
  durationMs: 4000,
  trackCount: 2,
  quality: 'Lossless · FLAC',
  starred: false,
  tracks: [1, 2].map((id) => ({ id, title: `Трек ${id}`, artist: 'Артист', artistId: 3, albumId: 7, album: 'Первый альбом', discNo: 1, trackNo: id, durationMs: 2000, fileId: 10 + id, lossless: true, hasLyrics: false, cover: 'album-7.jpg', starred: false })),
}

const BOOK = {
  card: { id: 9, kind: 'book', title: 'Книга', subtitle: '', poster: '', backdrop: null, logo: null, progress: 0, onServer: true },
  kind: 'book',
  authors: ['Автор'],
  narrator: '',
  series: null,
  description: '',
  year: 2000,
  lang: 'ru',
  files: [{ id: 90, format: 'EPUB', size: 50 }],
  seriesId: 0,
  nextId: 0,
  readFileId: 90,
  progress: null,
  want: false,
  durationMs: 0,
  chapters: [],
  volumes: [],
  direction: '',
}

/** Headers of /api/health (the server clock). */
let health: Record<string, string> = {}
let progressReply = () => new Response(null, { status: 204 })

let caches: FakeCaches
const calls: { url: string; init?: RequestInit }[] = []

beforeEach(() => {
  localStorage.clear()
  calls.length = 0
  caches = new FakeCaches()
  vi.stubGlobal('caches', caches)
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      calls.push({ url, init })
      if (url === '/api/me') return json({ id: 1, name: 'Александр', isAdmin: true, isKid: false, avatar: 'grad-1' })
      if (url === '/api/login') {
        const id = (JSON.parse(String(init?.body ?? '{}')) as { userId?: number }).userId ?? 1
        return json({ id, name: id === 1 ? 'Александр' : 'Маша', isAdmin: id === 1, isKid: id !== 1, avatar: 'grad-1' })
      }
      if (url === '/api/logout') return new Response(null, { status: 204 })
      if (url === '/api/music/albums/7') return json(ALBUM)
      if (url.startsWith('/api/images/')) return new Response(new Uint8Array(10), { headers: { 'Content-Type': 'image/jpeg' } })
      if (url.startsWith('/api/tracks/')) return new Response(new Uint8Array(100), { headers: { 'Content-Type': 'audio/flac', 'Content-Length': '100' } })
      if (url === '/api/health') return json({ ok: true }, { headers: health })
      if (url === '/api/books/9') return json(BOOK)
      if (url === '/api/stream/90') return new Response(new Uint8Array(50), { headers: { 'Content-Type': 'application/epub+zip' } })
      if (url.startsWith('/api/progress/')) return progressReply()
      return new Response('{"error":"Не найдено"}', { status: 404 })
    }),
  )
  health = {}
  progressReply = () => new Response(null, { status: 204 })
  session.reset()
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.useRealTimers()
})

describe('offline store', () => {
  it('saveOffline caches every album track and cover', async () => {
    await session.login(1)
    const progress: number[] = []
    await saveOffline({ kind: 'album', id: 7 }, (done, total) => progress.push(done / total))
    const keys = [...(await caches.open(OFFLINE_CACHE)).entries.keys()]
    expect(keys).toEqual(expect.arrayContaining(['/api/music/albums/7', '/api/images/album-7.jpg', '/api/tracks/1/stream', '/api/tracks/2/stream']))
    // the original file, kept under its path without the query
    // what this browser plays (jsdom: aac), without the remote bit-rate limit
    expect(calls.map((c) => c.url)).toEqual(expect.arrayContaining(['/api/tracks/1/stream?accept=aac&maxBitRate=0', '/api/tracks/2/stream?accept=aac&maxBitRate=0']))
    expect(isSavedMedia('/api/tracks/1/stream?accept=flac&t=30')).toBe(true)
    expect(isSavedMedia('/api/tracks/9/stream')).toBe(false)
    expect(progress.at(-1)).toBe(1)
    const [entry] = listOffline()
    expect(entry).toMatchObject({ kind: 'album', id: 7, title: 'Первый альбом', subtitle: 'Артист' })
  })

  it('removeOffline deletes cached URLs and index entry', async () => {
    await session.login(1)
    await saveOffline({ kind: 'album', id: 7 })
    await removeOffline({ kind: 'album', id: 7 })
    expect((await caches.open(OFFLINE_CACHE)).entries.size).toBe(0)
    expect(listOffline()).toEqual([])
  })

  it('index survives storage throwing (returns empty list)', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new DOMException('denied', 'SecurityError')
    })
    expect(listOffline()).toEqual([])
  })

  it('engine URL with accept and t hits the cached raw track', async () => {
    const c = await caches.open(OFFLINE_CACHE)
    await c.put('/api/tracks/5/stream', new Response('flac'))
    const hit = await findSaved('/api/tracks/5/stream?accept=flac,aac&t=0')
    expect(hit).toBeDefined()
    expect(await hit!.text()).toBe('flac')
    expect(await findSaved('/api/tracks/6/stream?accept=flac')).toBeUndefined()
  })

  it('pending progress is sent with its capture time', async () => {
    await session.login(1)
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(new Date('2026-10-20T08:00:00Z'))
    rememberProgress({ itemId: 5, positionMs: 60_000, durationMs: 600_000 })
    vi.setSystemTime(new Date('2026-10-20T09:30:00Z'))
    await sendPendingProgress()
    const put = calls.find((c) => c.url === '/api/progress/5')
    expect(put?.init?.method).toBe('PUT')
    expect(JSON.parse(String(put!.init!.body))).toEqual({ positionMs: 60_000, durationMs: 600_000, updatedAt: '2026-10-20T08:00:00.000Z' })
    // sent once: nothing left to send
    calls.length = 0
    await sendPendingProgress()
    expect(calls.find((c) => c.url.startsWith('/api/progress/'))).toBeUndefined()
  })

  it('a watched mark drops this profile\'s positions of the items queued before it', async () => {
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(new Date('2026-10-20T08:00:00Z'))
    await session.login(2)
    rememberProgress({ itemId: 5, positionMs: 1000, durationMs: 9000 }) // another profile's
    await session.login(1)
    rememberProgress({ itemId: 5, positionMs: 590_000, durationMs: 600_000 })
    rememberProgress({ itemId: 7, positionMs: 2000, durationMs: 9000 }) // an episode of the series
    rememberProgress({ itemId: 6, positionMs: 1000, durationMs: 9000 })
    const markedAt = Date.now()
    vi.setSystemTime(new Date('2026-10-20T08:00:05Z'))
    rememberProgress({ itemId: 8, positionMs: 3000, durationMs: 9000 }) // reached while the mark was on the wire
    discardProgress([5, 7, 8], markedAt)
    const left = (JSON.parse(localStorage.getItem('vault.pending-progress.v1') ?? '[]') as { itemId: number; userId: number }[])
      .map((p) => `${p.itemId}/${p.userId}`)
      .sort()
    expect(left).toEqual(['5/2', '6/1', '8/1'])
  })

  it('signing out hides the saved items and another profile signing in deletes them', async () => {
    bindOfflineToSession()
    await session.login(1)
    await saveOffline({ kind: 'album', id: 7 })
    expect(offlineAvailable()).toBe(true)
    await session.logout()
    // locked: the offline screen does not show another profile's items
    expect(listOffline()).toEqual([])
    expect(offlineAvailable()).toBe(false)
    await session.login(1)
    expect(listOffline()).toHaveLength(1)
    await session.logout()
    await session.login(2)
    await vi.waitFor(async () => expect(await caches.keys()).not.toContain(OFFLINE_CACHE))
    expect(listOffline()).toEqual([])
  })

  it('a full device gives a clear message and keeps nothing half-saved', async () => {
    await session.login(1)
    const c = await caches.open(OFFLINE_CACHE)
    const put = c.put.bind(c)
    c.put = async (r, res) => {
      if (String(r).includes('/stream')) throw new DOMException('full', 'QuotaExceededError')
      return put(r, res)
    }
    await expect(saveOffline({ kind: 'album', id: 7 })).rejects.toThrow(noSpace())
    expect(c.entries.size).toBe(0)
    expect(listOffline()).toEqual([])
  })

  it('a 502 from the proxy keeps the position queued; 404 drops it', async () => {
    await session.login(1)
    rememberProgress({ itemId: 5, positionMs: 1000, durationMs: 9000 })
    progressReply = () => new Response('<html>Bad Gateway</html>', { status: 502 })
    await sendPendingProgress()
    expect(JSON.parse(localStorage.getItem('vault.pending-progress.v1') ?? '[]')).toHaveLength(1)
    progressReply = () => new Response('{"error":"Войдите"}', { status: 401 })
    await sendPendingProgress()
    expect(JSON.parse(localStorage.getItem('vault.pending-progress.v1') ?? '[]')).toHaveLength(1)
    await session.login(1) // the 401 signed the app out
    progressReply = () => new Response('{"error":"Не найдено"}', { status: 404 })
    await sendPendingProgress()
    expect(localStorage.getItem('vault.pending-progress.v1')).toBeNull()
  })

  it('the capture time follows the server clock', async () => {
    await session.login(1)
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(new Date('2026-10-20T08:00:00Z'))
    rememberProgress({ itemId: 5, positionMs: 1000, durationMs: 9000 })
    // the server's clock is a minute ahead of this device
    health = { Date: new Date(Date.now() + 60_000).toUTCString() }
    await sendPendingProgress()
    const body = JSON.parse(String(calls.find((c) => c.url === '/api/progress/5')!.init!.body)) as { updatedAt: string }
    const off = Date.parse(body.updatedAt) - Date.parse('2026-10-20T08:00:00Z')
    expect(off).toBeGreaterThanOrEqual(59_000)
    expect(off).toBeLessThanOrEqual(61_000)
  })

  it('a position reached offline is written into the saved copy', async () => {
    await session.login(1)
    await saveOffline({ kind: 'book', id: 9 })
    rememberProgress({ itemId: 9, locator: 'epubcfi(/6/4!/4/2)', fraction: 0.4 })
    await vi.waitFor(async () => {
      const copy = (await (await (await caches.open(OFFLINE_CACHE)).match('/api/books/9'))!.json()) as { progress: { locator: string; fraction: number } | null }
      expect(copy.progress).toMatchObject({ locator: 'epubcfi(/6/4!/4/2)', fraction: 0.4 })
    })
  })

  it('starting signed out hides what was saved', async () => {
    await session.login(1)
    await saveOffline({ kind: 'album', id: 7 })
    session.reset()
    bindOfflineToSession()
    // the session was revoked meanwhile: the app starts at the profile picker
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) =>
        String(input) === '/api/me' ? new Response('{"error":"Войдите"}', { status: 401 }) : json({ needsSetup: false }),
      ),
    )
    await session.bootstrap()
    expect(session.get().status).toBe('signed-out')
    expect(listOffline()).toEqual([])
    expect(offlineAvailable()).toBe(false)
  })

  it('the lock is also a marker the service worker reads; the owner signing in removes it', async () => {
    bindOfflineToSession()
    await session.login(1)
    await saveOffline({ kind: 'album', id: 7 })
    const c = await caches.open(OFFLINE_CACHE)
    await session.logout()
    await vi.waitFor(async () => expect(await c.match(LOCK_PATH)).toBeDefined())
    await session.login(1)
    await vi.waitFor(async () => expect(await c.match(LOCK_PATH)).toBeUndefined())
    expect(listOffline()).toHaveLength(1)
  })
})
