import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { audio, audioNotice, engineMode, resetAudioForTests } from '../engine'
import type { QueueItem } from '../engine'
import { session } from '../../../state/session'

function items(n: number, over: Partial<QueueItem> = {}): QueueItem[] {
  return Array.from({ length: n }, (_, i) => ({
    key: `t${i + 1}`,
    trackId: i + 1,
    title: `Трек ${i + 1}`,
    artist: 'Артист',
    album: 'Альбом',
    cover: `/api/images/local/9/poster`,
    durationSec: 200,
    src: `/api/tracks/${i + 1}/stream?accept=flac`,
    ...over,
  }))
}

const els = () => [...document.querySelectorAll<HTMLAudioElement>('audio[data-vault-audio]')]
const active = () => {
  const el = els().find((e) => e.hasAttribute('data-active'))
  if (!el) throw new Error('no active element')
  return el
}
const other = () => els().find((e) => !e.hasAttribute('data-active'))!

/** jsdom has no media pipeline: give an element the numbers a browser would. */
function media(el: HTMLMediaElement, v: { currentTime?: number; duration?: number; seekable?: [number, number][] }) {
  if (v.currentTime !== undefined) Object.defineProperty(el, 'currentTime', { configurable: true, writable: true, value: v.currentTime })
  if (v.duration !== undefined) Object.defineProperty(el, 'duration', { configurable: true, writable: true, value: v.duration })
  if (v.seekable !== undefined) {
    const r = v.seekable
    Object.defineProperty(el, 'seekable', {
      configurable: true,
      value: { length: r.length, start: (i: number) => r[i]![0], end: (i: number) => r[i]![1] },
    })
  }
}
/** jsdom has no maxTouchPoints; removed again in afterEach. */
const touchPoints = (n: number) => Object.defineProperty(navigator, 'maxTouchPoints', { configurable: true, value: n })
const fire = (el: HTMLMediaElement, type: string) => el.dispatchEvent(new Event(type))

let play: ReturnType<typeof vi.fn>

beforeEach(() => {
  resetAudioForTests()
  play = vi.fn(() => Promise.resolve())
  vi.spyOn(HTMLMediaElement.prototype, 'play').mockImplementation(play as unknown as () => Promise<void>)
  vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {})
  vi.spyOn(HTMLMediaElement.prototype, 'load').mockImplementation(() => {})
  vi.stubGlobal('fetch', vi.fn(async () => new Response(null, { status: 204 })))
})

afterEach(() => {
  resetAudioForTests()
  vi.useRealTimers()
  vi.unstubAllGlobals()
  delete (navigator as unknown as { maxTouchPoints?: number }).maxTouchPoints
})

describe('audio engine', () => {
  it('audio elements are attached to document.body', () => {
    audio.play(items(2), 0)
    expect(els()).toHaveLength(2)
    for (const el of els()) {
      expect(el.parentElement).toBe(document.body)
      expect(el.hidden).toBe(true)
    }
    expect(active().getAttribute('src')).toBe(items(2)[0]!.src)
    expect(audio.get().playing).toBe(true)
    expect(audio.get().kind).toBe('music')
  })

  it('next at end with repeat=all wraps to first', () => {
    audio.play(items(3), 2)
    audio.cycleRepeat()
    expect(audio.get().repeat).toBe('all')
    fire(active(), 'ended')
    expect(audio.get().index).toBe(0)
    expect(audio.get().playing).toBe(true)
    expect(active().getAttribute('src')).toBe(items(3)[0]!.src)
  })

  it('repeat=off stops after last (playing=false)', () => {
    audio.play(items(3), 2)
    fire(active(), 'ended')
    expect(audio.get().index).toBe(2)
    expect(audio.get().playing).toBe(false)
    expect(audio.get().queue).toHaveLength(3)
  })

  it('repeat=one replays the same index', () => {
    audio.play(items(3), 1)
    audio.cycleRepeat()
    audio.cycleRepeat()
    expect(audio.get().repeat).toBe('one')
    media(active(), { currentTime: 200, duration: 200 })
    const calls = play.mock.calls.length
    fire(active(), 'ended')
    expect(audio.get().index).toBe(1)
    expect(active().currentTime).toBe(0)
    expect(play.mock.calls.length).toBe(calls + 1)
    // a manual «next» still moves on
    audio.next()
    expect(audio.get().index).toBe(2)
  })

  it('previous after 3 s restarts the track', () => {
    audio.play(items(3), 1)
    media(active(), { currentTime: 5, duration: 200 })
    fire(active(), 'timeupdate')
    expect(audio.get().positionSec).toBe(5)
    audio.previous()
    expect(audio.get().index).toBe(1)
    expect(active().currentTime).toBe(0)
    media(active(), { currentTime: 1 })
    fire(active(), 'timeupdate')
    audio.previous()
    expect(audio.get().index).toBe(0)
  })

  it('shuffle keeps current first and permutes the rest', () => {
    const list = items(6)
    let seed = 7
    vi.spyOn(Math, 'random').mockImplementation(() => {
      seed = (seed * 16807) % 2147483647
      return seed / 2147483647
    })
    audio.play(list, 3, { shuffle: true })
    const s = audio.get()
    expect(s.shuffle).toBe(true)
    expect(s.index).toBe(0)
    expect(s.queue[0]!.key).toBe('t4')
    expect(s.queue.map((q) => q.key).sort()).toEqual(list.map((q) => q.key).sort())
    expect(s.queue.map((q) => q.key)).not.toEqual(['t4', 't1', 't2', 't3', 't5', 't6'])
    // turning it off restores the album order at the same track
    audio.setShuffle(false)
    expect(audio.get().queue.map((q) => q.key)).toEqual(list.map((q) => q.key))
    expect(audio.get().index).toBe(3)
  })

  it('preloads next src 15 s before end', () => {
    const list = items(3, { durationSec: 20 })
    audio.play(list, 0)
    media(active(), { currentTime: 4, duration: 20 })
    fire(active(), 'timeupdate')
    expect(other().getAttribute('src')).toBeNull()
    media(active(), { currentTime: 6 })
    fire(active(), 'timeupdate')
    const next = other()
    expect(next.getAttribute('src')).toBe(list[1]!.src)
    expect(next.preload).toBe('auto')
    fire(active(), 'ended')
    expect(audio.get().index).toBe(1)
    expect(active()).toBe(next)
    expect(play).toHaveBeenCalled()
  })

  it('seek outside seekable reloads with t=120', () => {
    audio.play(items(2), 0)
    const el = active()
    // a transcode: no duration, nothing seekable
    media(el, { currentTime: 0, duration: Infinity, seekable: [] })
    fire(el, 'loadedmetadata')
    audio.seek(120)
    expect(active().getAttribute('src')).toBe('/api/tracks/1/stream?accept=flac&t=120')
    expect(audio.get().positionSec).toBe(120)
    media(active(), { currentTime: 2 })
    fire(active(), 'timeupdate')
    expect(audio.get().positionSec).toBe(122)
    expect(audio.get().durationSec).toBe(200)
  })

  it('seek inside seekable sets currentTime', () => {
    audio.play(items(2), 0)
    media(active(), { currentTime: 0, duration: 200, seekable: [[0, 200]] })
    fire(active(), 'loadedmetadata')
    audio.seek(50)
    expect(active().currentTime).toBe(50)
    expect(active().getAttribute('src')).toBe(items(2)[0]!.src)
  })

  /** Plays `sec` seconds the way a browser reports them: a timeupdate every second. */
  function listen(from: number, sec: number) {
    for (let t = from + 1; t <= from + sec; t++) {
      media(active(), { currentTime: t })
      fire(active(), 'timeupdate')
    }
  }

  it('records a play once after 30 s', async () => {
    audio.play(items(2), 0)
    media(active(), { currentTime: 0, duration: 200 })
    listen(0, 29)
    expect(fetch).not.toHaveBeenCalled()
    listen(29, 20)
    expect(fetch).toHaveBeenCalledTimes(1)
    const [url, init] = vi.mocked(fetch).mock.calls[0]!
    expect(url).toBe('/api/music/plays')
    expect(JSON.parse(String((init as RequestInit).body))).toEqual({ trackId: 1 })
  })

  it('a seek past half the track is not a play', () => {
    audio.play(items(2), 0)
    media(active(), { currentTime: 0, duration: 200, seekable: [[0, 200]] })
    fire(active(), 'loadedmetadata')
    audio.seek(150)
    fire(active(), 'timeupdate')
    listen(150, 5)
    expect(fetch).not.toHaveBeenCalled()
    listen(155, 26)
    expect(fetch).toHaveBeenCalledTimes(1)
  })

  it('a short track counts after half of it', () => {
    audio.play(items(2, { durationSec: 20 }), 0)
    media(active(), { currentTime: 0, duration: 20 })
    listen(0, 9)
    expect(fetch).not.toHaveBeenCalled()
    listen(9, 2)
    expect(fetch).toHaveBeenCalledTimes(1)
  })

  it('sets mediaSession metadata on play', () => {
    vi.useFakeTimers()
    const handlers: Record<string, unknown> = {}
    const session = {
      metadata: null as unknown,
      playbackState: 'none',
      setActionHandler: vi.fn((name: string, fn: unknown) => {
        handlers[name] = fn
      }),
      setPositionState: vi.fn(),
    }
    Object.defineProperty(navigator, 'mediaSession', { configurable: true, value: session })
    vi.stubGlobal(
      'MediaMetadata',
      class {
        constructor(init: object) {
          Object.assign(this, init)
        }
      },
    )
    try {
      audio.play(items(2), 0)
      const md = session.metadata as { title: string; artist: string; album: string; artwork: { src: string; sizes: string }[] }
      expect(md.title).toBe('Трек 1')
      expect(md.artist).toBe('Артист')
      expect(md.album).toBe('Альбом')
      expect(md.artwork[0]!).toMatchObject({ src: '/api/images/local/9/poster', sizes: '512x512' })
      for (const a of ['play', 'pause', 'previoustrack', 'nexttrack', 'seekto', 'seekbackward', 'seekforward']) {
        expect(typeof handlers[a]).toBe('function')
      }
      media(active(), { currentTime: 3, duration: 200 })
      vi.advanceTimersByTime(1000)
      expect(session.setPositionState).toHaveBeenCalledWith({ duration: 200, position: 3, playbackRate: 1 })
      ;(handlers.nexttrack as () => void)()
      expect(audio.get().index).toBe(1)
      audio.stop()
      expect(session.metadata).toBeNull()
      expect(handlers.nexttrack).toBeNull()
    } finally {
      delete (navigator as unknown as { mediaSession?: unknown }).mediaSession
    }
  })

  it('yields the media session to a video and claims it back on resume', () => {
    const handlers: Record<string, unknown> = {}
    const session = {
      metadata: null as unknown,
      playbackState: 'none',
      setActionHandler: vi.fn((name: string, fn: unknown) => {
        handlers[name] = fn
      }),
      setPositionState: vi.fn(),
    }
    Object.defineProperty(navigator, 'mediaSession', { configurable: true, value: session })
    vi.stubGlobal(
      'MediaMetadata',
      class {
        constructor(init: object) {
          Object.assign(this, init)
        }
      },
    )
    try {
      audio.play(items(2), 0)
      audio.yieldSession()
      expect(audio.get().playing).toBe(false)
      expect(session.metadata).toBeNull()
      expect(handlers.play).toBeNull()
      expect(handlers.nexttrack).toBeNull()
      // a video's own metadata, cleared when it closes
      session.metadata = null
      audio.toggle()
      expect(audio.get().playing).toBe(true)
      expect((session.metadata as { title: string } | null)?.title).toBe('Трек 1')
      expect(typeof handlers.play).toBe('function')
      expect(typeof handlers.nexttrack).toBe('function')
    } finally {
      delete (navigator as unknown as { mediaSession?: unknown }).mediaSession
    }
  })

  it('«Играть» after an error stop fetches the track again', () => {
    audio.play(items(5), 0)
    for (let i = 0; i < 3; i++) fire(active(), 'error')
    expect(audio.get().playing).toBe(false)
    const src = vi.spyOn(HTMLMediaElement.prototype, 'src', 'set')
    audio.toggle()
    expect(src).toHaveBeenCalledWith(items(5)[2]!.src)
    expect(audio.get().playing).toBe(true)
    expect(audio.get().error).toBeUndefined()
  })

  it('a rejected play() is an error, not a silent «playing»', async () => {
    play.mockImplementationOnce(() => Promise.reject(new DOMException('no source', 'NotSupportedError')))
    audio.play(items(3), 0)
    await Promise.resolve()
    await Promise.resolve()
    expect(audio.get().index).toBe(1)
    expect(audioNotice.get()).toBe('Не удалось воспроизвести трек — включаю следующий')
  })

  it('a refused autoplay leaves the player paused', async () => {
    play.mockImplementationOnce(() => Promise.reject(new DOMException('gesture', 'NotAllowedError')))
    audio.play(items(3), 0)
    await Promise.resolve()
    await Promise.resolve()
    expect(audio.get().playing).toBe(false)
    expect(audio.get().index).toBe(0)
  })

  it('a seek before the metadata waits for it (a transcode reloads)', () => {
    audio.play(items(2), 0)
    audio.seek(90)
    media(active(), { currentTime: 0, duration: Infinity, seekable: [] })
    fire(active(), 'loadedmetadata')
    expect(active().getAttribute('src')).toBe('/api/tracks/1/stream?accept=flac&t=90')
  })

  it('skips to next on error and stops after three', () => {
    audio.play(items(5), 0)
    fire(active(), 'error')
    expect(audio.get().index).toBe(1)
    expect(audioNotice.get()).toBe('Не удалось воспроизвести трек — включаю следующий')
    fire(active(), 'error')
    expect(audio.get().index).toBe(2)
    fire(active(), 'error')
    expect(audio.get().playing).toBe(false)
    expect(audio.get().error).toBeTruthy()
  })

  describe('stream failures', () => {
    const failWith = (el: HTMLMediaElement, code: number) => {
      Object.defineProperty(el, 'error', { configurable: true, value: { code } })
      fire(el, 'error')
    }
    const at = (el: HTMLMediaElement, t: number) => {
      media(el, { currentTime: t })
      fire(el, 'timeupdate')
    }
    const flush = async () => {
      for (let i = 0; i < 5; i++) await Promise.resolve()
    }

    it('a network error while playing reloads the same track where it was', async () => {
      vi.useFakeTimers()
      audio.play(items(3), 0)
      const el = active()
      media(el, { currentTime: 0, duration: 200, seekable: [[0, 200]] })
      fire(el, 'loadedmetadata')
      at(el, 50)
      const src = vi.spyOn(HTMLMediaElement.prototype, 'src', 'set')
      failWith(el, 2)
      await flush()
      expect(audio.get().index).toBe(0)
      expect(audioNotice.get()).toBeNull()
      expect(src).not.toHaveBeenCalled()
      await vi.advanceTimersByTimeAsync(1000)
      expect(src).toHaveBeenCalledWith(items(3)[0]!.src)
      expect(audio.get().index).toBe(0)
      expect(audio.get().positionSec).toBe(50)
      // the metadata of the new load puts it back at 50 s
      media(active(), { currentTime: 0, duration: 200, seekable: [[0, 200]] })
      fire(active(), 'loadedmetadata')
      expect(active().currentTime).toBe(50)
    })

    it('a busy server (503) is retried after its Retry-After, a transcode from its position', async () => {
      vi.useFakeTimers()
      vi.mocked(fetch).mockImplementation(async () => new Response('{"error":"Сервер занят"}', { status: 503, headers: { 'Retry-After': '2' } }))
      audio.play(items(3), 0)
      const el = active()
      media(el, { currentTime: 0, duration: Infinity, seekable: [] })
      fire(el, 'loadedmetadata')
      at(el, 40)
      failWith(el, 4)
      await flush()
      expect(String(vi.mocked(fetch).mock.calls[0]![0])).toMatch(/\/api\/tracks\/1\/stream\?accept=flac$/)
      await vi.advanceTimersByTimeAsync(1999)
      expect(active().getAttribute('src')).toBe(items(3)[0]!.src)
      await vi.advanceTimersByTimeAsync(1)
      expect(active().getAttribute('src')).toBe('/api/tracks/1/stream?accept=flac&t=40')
      expect(audio.get().index).toBe(0)
      expect(audio.get().playing).toBe(true)
    })

    it('a track that keeps failing is skipped after three retries', async () => {
      vi.useFakeTimers()
      audio.play(items(3), 0)
      for (let i = 0; i < 3; i++) {
        failWith(active(), 2)
        await flush()
        await vi.advanceTimersByTimeAsync(5000)
        expect(audio.get().index).toBe(0)
      }
      failWith(active(), 2)
      await flush()
      expect(audio.get().index).toBe(1)
      expect(audioNotice.get()).toBe('Не удалось воспроизвести трек — включаю следующий')
    })

    it('a missing file (404) or an undecodable one is skipped at once', async () => {
      vi.mocked(fetch).mockImplementation(async () => new Response('{"error":"Файл пропал с диска"}', { status: 404 }))
      audio.play(items(3), 0)
      failWith(active(), 4)
      await flush()
      expect(audio.get().index).toBe(1)
      failWith(active(), 3)
      await flush()
      expect(audio.get().index).toBe(2)
    })

    // «Экранное время»: a kid's stream refused (or cut) at the limit stops
    // the listening with the kid's message; nothing skips through the queue
    it('a kid refused by screen time stops with the message', async () => {
      vi.mocked(fetch).mockImplementation(async (input: RequestInfo | URL) => {
        const url = String(input)
        const json = (b: unknown, status = 200) => new Response(JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } })
        if (url.endsWith('/api/login')) return json({ id: 3, name: 'Вася', avatar: 'grad-1', hasPassword: false, isAdmin: false, isKid: true, maxRating: '6+' })
        if (url.endsWith('/api/me/screen-time'))
          return json({
            limited: true,
            day: '2026-11-02',
            hours: null,
            video: { limitMin: null, bonusMin: 0, usedSec: 0, remainingSec: null, allowed: true },
            audio: { limitMin: 30, bonusMin: 0, usedSec: 1800, remainingSec: 0, allowed: false, reason: 'limit', message: 'Время на сегодня закончилось. Завтра можно снова!' },
          })
        return json({ error: 'Время на сегодня закончилось. Завтра можно снова!', code: 'screen_time', reason: 'limit' }, 403)
      })
      await session.login(3)
      audio.play(items(3), 0)
      failWith(active(), 4)
      for (let i = 0; i < 20; i++) await flush()
      expect(audio.get().index).toBe(0)
      expect(audio.get().playing).toBe(false)
      expect(audio.get().screenStop).toBe('Время на сегодня закончилось. Завтра можно снова!')
      session.reset()
    })

    it('a failure while paused keeps the track and its place; «Играть» fetches it again there', async () => {
      vi.useFakeTimers()
      audio.play(items(3), 0)
      const el = active()
      media(el, { currentTime: 0, duration: 200, seekable: [[0, 200]] })
      fire(el, 'loadedmetadata')
      fire(el, 'playing')
      at(el, 30)
      audio.pause()
      failWith(el, 2) // the server dropped the idle stream
      await flush()
      await vi.advanceTimersByTimeAsync(10_000)
      expect(audio.get().index).toBe(0)
      expect(audio.get().playing).toBe(false)
      expect(audio.get().positionSec).toBe(30)
      expect(audioNotice.get()).toBeNull()
      expect(play).toHaveBeenCalledTimes(1)
      const src = vi.spyOn(HTMLMediaElement.prototype, 'src', 'set')
      audio.toggle()
      expect(src).toHaveBeenCalledWith(items(3)[0]!.src)
      expect(audio.get().index).toBe(0)
      expect(audio.get().playing).toBe(true)
    })
  })

  it('single-element mode on iOS swaps src on ended', () => {
    vi.spyOn(navigator, 'userAgent', 'get').mockReturnValue(
      'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1',
    )
    touchPoints(5)
    expect(engineMode()).toBe('single')
    const list = items(3, { durationSec: 20 })
    audio.play(list, 0)
    expect(els()).toHaveLength(1)
    const el = els()[0]!
    media(el, { currentTime: 10, duration: 20 })
    fire(el, 'timeupdate')
    expect(els()).toHaveLength(1)
    expect(el.getAttribute('src')).toBe(list[0]!.src)
    fire(el, 'ended')
    expect(els()[0]).toBe(el)
    expect(el.getAttribute('src')).toBe(list[1]!.src)
    expect(audio.get().index).toBe(1)
  })

  it('wirelessKeepsOneElement', () => {
    const list = items(4, { durationSec: 20 })
    audio.play(list, 0)
    expect(els()).toHaveLength(2)
    const first = active()
    const wireless = (el: HTMLMediaElement, on: boolean) => {
      Object.defineProperty(el, 'webkitCurrentPlaybackTargetIsWireless', { configurable: true, value: on })
      fire(el, 'webkitcurrentplaybacktargetiswirelesschanged')
    }
    // routed to AirPlay: no preload into the other element, next() stays here
    wireless(first, true)
    media(first, { currentTime: 10, duration: 20 })
    fire(first, 'timeupdate')
    expect(other().getAttribute('src')).toBeNull()
    audio.next()
    expect(audio.get().index).toBe(1)
    expect(audio.element()).toBe(first)
    expect(first.getAttribute('src')).toBe(list[1]!.src)
    // and on `ended` too
    fire(first, 'ended')
    expect(audio.get().index).toBe(2)
    expect(audio.element()).toBe(first)
    expect(first.getAttribute('src')).toBe(list[2]!.src)
    // back on the Mac's speakers: the next change hands off again
    wireless(first, false)
    media(first, { currentTime: 10, duration: 20 })
    fire(first, 'timeupdate')
    const next = other()
    expect(next.getAttribute('src')).toBe(list[3]!.src)
    audio.next()
    expect(audio.get().index).toBe(3)
    expect(audio.element()).toBe(next)
  })

  it('going wireless drops a preload already in the other element', () => {
    const list = items(3, { durationSec: 20 })
    audio.play(list, 0)
    const first = active()
    media(first, { currentTime: 10, duration: 20 })
    fire(first, 'timeupdate')
    expect(other().getAttribute('src')).toBe(list[1]!.src)
    Object.defineProperty(first, 'webkitCurrentPlaybackTargetIsWireless', { configurable: true, value: true })
    fire(first, 'webkitcurrentplaybacktargetiswirelesschanged')
    expect(other().getAttribute('src')).toBeNull()
    fire(first, 'ended')
    expect(audio.element()).toBe(first)
    expect(first.getAttribute('src')).toBe(list[1]!.src)
  })

  it('dual mode on a Mac with a mouse', () => {
    vi.spyOn(navigator, 'userAgent', 'get').mockReturnValue('Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15')
    touchPoints(0)
    expect(engineMode()).toBe('dual')
  })
})

describe('queue edits (jumpTo, move, element)', () => {
  /** Counts src assignments on every audio element. */
  function countSrc() {
    const d = Object.getOwnPropertyDescriptor(HTMLMediaElement.prototype, 'src')!
    const seen: string[] = []
    vi.spyOn(HTMLMediaElement.prototype, 'src', 'set').mockImplementation(function (this: HTMLMediaElement, v: string) {
      seen.push(v)
      d.set!.call(this, v)
    })
    return seen
  }

  it('jumpTo starts the chosen track with a single load', () => {
    audio.play(items(6), 0)
    const seen = countSrc()
    audio.jumpTo(4)
    expect(audio.get().index).toBe(4)
    expect(audio.get().queue.map((q) => q.key)).toEqual(['t1', 't2', 't3', 't4', 't5', 't6'])
    expect(seen).toEqual(['/api/tracks/5/stream?accept=flac'])
    expect(audio.get().playing).toBe(true)
  })

  it('jumpTo keeps a paused player paused and ignores bad indexes', () => {
    audio.play(items(3), 0)
    audio.pause()
    audio.jumpTo(2)
    expect(audio.get()).toMatchObject({ index: 2, playing: false })
    audio.jumpTo(7)
    audio.jumpTo(-1)
    expect(audio.get().index).toBe(2)
  })

  it('move reorders in one update and keeps the current track', () => {
    audio.play(items(5), 1)
    let updates = 0
    const off = audio.subscribe(() => updates++)
    audio.move(4, 2)
    off()
    expect(updates).toBe(1)
    expect(audio.get().queue.map((q) => q.key)).toEqual(['t1', 't2', 't5', 't3', 't4'])
    expect(audio.get().queue[audio.get().index]!.key).toBe('t2')
    // moving the current track follows it
    audio.move(1, 3)
    expect(audio.get().queue.map((q) => q.key)).toEqual(['t1', 't5', 't3', 't2', 't4'])
    expect(audio.get().index).toBe(3)
    // a track from before the current one to after it
    audio.move(0, 4)
    expect(audio.get().queue.map((q) => q.key)).toEqual(['t5', 't3', 't2', 't4', 't1'])
    expect(audio.get().index).toBe(2)
  })

  it('move under shuffle leaves the original order for shuffle off', () => {
    audio.play(items(6), 0, { shuffle: true })
    const q = audio.get().queue.map((x) => x.key)
    audio.move(5, 1)
    expect(audio.get().queue.map((x) => x.key)).toEqual([q[0], q[5], q[1], q[2], q[3], q[4]])
    audio.setShuffle(false)
    expect(audio.get().queue.map((x) => x.key)).toEqual(['t1', 't2', 't3', 't4', 't5', 't6'])
    expect(audio.get().queue[audio.get().index]!.key).toBe(q[0])
  })

  it('element gives the playing <audio>', () => {
    expect(audio.element()).toBeNull()
    audio.play(items(2), 0)
    expect(audio.element()).toBe(active())
  })
})
