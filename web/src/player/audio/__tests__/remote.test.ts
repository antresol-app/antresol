// «Воспроизвести на…»: a remote takes the listening (attachRemote). The
// browser falls silent, its controls go to the remote, the remote's
// reports move the player, and a release gives the listening back.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { forgetMediaKey } from '../../airplay'
import { audio, resetAudioForTests, type AudioRemote, type QueueItem, type RemoteSnapshot } from '../engine'

function songs(n: number): QueueItem[] {
  return Array.from({ length: n }, (_, i) => ({
    key: `t${i}`,
    trackId: 100 + i,
    title: `Song ${i}`,
    artist: 'Band',
    album: 'LP',
    cover: '',
    durationSec: 200,
    src: `/api/tracks/${100 + i}/stream?accept=mp3,aac`,
  }))
}

type Call = { url: string; init?: RequestInit }
let calls: Call[] = []

function fakeRemote() {
  const log: string[] = []
  const syncs: { s: RemoteSnapshot; jump: boolean }[] = []
  const r: AudioRemote = {
    sync: (s, jump) => {
      syncs.push({ s, jump })
      log.push(`sync:${jump ? 'jump' : 'queue'}:${s.index}`)
    },
    play: () => log.push('play'),
    pause: () => log.push('pause'),
    seek: (t) => log.push(`seek:${t}`),
    volume: (v) => log.push(`volume:${v}`),
    stop: () => log.push('stop'),
  }
  return { r, log, syncs }
}

/** Lets the remote's chained calls (each after the media key) run. */
const settle = () => new Promise((res) => setTimeout(res, 0))

beforeEach(() => {
  resetAudioForTests()
  forgetMediaKey()
  calls = []
  vi.spyOn(HTMLMediaElement.prototype, 'play').mockImplementation(() => Promise.resolve())
  vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {})
  vi.spyOn(HTMLMediaElement.prototype, 'load').mockImplementation(() => {})
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      calls.push({ url, init })
      if (url === '/api/auth/media-key') {
        return new Response(JSON.stringify({ key: 'KEY1', expiresAt: new Date(Date.now() + 4 * 3600_000).toISOString() }), { status: 200 })
      }
      return new Response(null, { status: 204 })
    }),
  )
})

afterEach(() => {
  resetAudioForTests()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('a remote output', () => {
  it('takes the listening with absolute stream URLs carrying the media key', async () => {
    audio.play(songs(3), 1)
    const pause = vi.mocked(HTMLMediaElement.prototype.pause)
    pause.mockClear()
    const { r, log, syncs } = fakeRemote()
    const h = audio.attachRemote(r, 'Living room')
    expect(h.active).toBe(true)
    expect(audio.remote()).toBe(true)
    expect(audio.get().output).toBe('Living room')
    expect(pause).toHaveBeenCalled() // the browser falls silent
    await settle()
    expect(log).toEqual(['sync:jump:1'])
    const s = syncs[0]!.s
    expect(s.playing).toBe(true)
    expect(s.items).toHaveLength(3)
    // the file as it is (no accept), at the page's address, with the key
    expect(s.items[1]!.url).toBe(`${window.location.origin}/api/tracks/101/stream?mk=KEY1`)
    expect(s.items[1]).toMatchObject({ key: 't1', title: 'Song 1', artist: 'Band', album: 'LP', durationSec: 200 })
  })

  it('sends the controls to the remote and follows its reports', async () => {
    audio.play(songs(4), 0)
    const { r, log, syncs } = fakeRemote()
    const h = audio.attachRemote(r, 'Living room')
    await settle()
    audio.toggle() // pause
    audio.toggle() // play
    audio.seek(42)
    audio.setVolume(0.3)
    await settle()
    expect(log.slice(1)).toEqual(['pause', 'play', 'seek:42', 'volume:0.3'])
    expect(audio.get().positionSec).toBe(42)

    // next: the remote jumps to the next item, from its start
    audio.next()
    await settle()
    expect(log.at(-1)).toBe('sync:jump:1')
    expect(syncs.at(-1)!.s.positionSec).toBe(0)

    // a track added: the queue follows, the current one plays on
    audio.addToQueue(songs(6).slice(4))
    await settle()
    expect(log.at(-1)).toBe('sync:queue:1')
    expect(syncs.at(-1)!.s.items).toHaveLength(6)

    // the remote moved on by itself: the player follows by key
    h.update({ key: 't2', positionSec: 3, durationSec: 180, playing: true })
    expect(audio.get().index).toBe(2)
    expect(audio.get().positionSec).toBe(3)
    expect(audio.get().durationSec).toBe(180)
    h.update({ playing: false })
    expect(audio.get().playing).toBe(false)
  })

  it('counts a listen heard on the remote', async () => {
    audio.play(songs(2), 0)
    const { r } = fakeRemote()
    const h = audio.attachRemote(r, 'Living room')
    await settle()
    for (let t = 1; t <= 31; t++) h.update({ positionSec: t, playing: true })
    await settle()
    expect(calls.filter((c) => c.url === '/api/music/plays')).toHaveLength(1)
  })

  it('gives the listening back where the remote was', async () => {
    audio.play(songs(3), 0)
    const { r, log } = fakeRemote()
    const h = audio.attachRemote(r, 'Living room')
    await settle()
    h.update({ key: 't1', positionSec: 50, playing: true })
    const play = vi.mocked(HTMLMediaElement.prototype.play)
    play.mockClear()
    h.release({ resume: true })
    expect(log.at(-1)).toBe('stop')
    expect(h.active).toBe(false)
    expect(audio.remote()).toBe(false)
    expect(audio.get().output).toBeUndefined()
    expect(audio.get().index).toBe(1)
    expect(audio.get().playing).toBe(true)
    expect(play).toHaveBeenCalled()
    // a stale handle does nothing
    h.update({ key: 't2' })
    expect(audio.get().index).toBe(1)
  })

  it('is stopped by stop and replaced by another remote', async () => {
    audio.play(songs(2), 0)
    const a = fakeRemote()
    const b = fakeRemote()
    const ha = audio.attachRemote(a.r, 'A')
    audio.attachRemote(b.r, 'B')
    expect(a.log.at(-1)).toBe('stop')
    expect(ha.active).toBe(false)
    expect(audio.get().output).toBe('B')
    audio.stop()
    expect(b.log.at(-1)).toBe('stop')
    expect(audio.get().queue).toHaveLength(0)
  })

  it('with nothing queued is let go at once', () => {
    const { r, log } = fakeRemote()
    const h = audio.attachRemote(r, 'Living room')
    expect(h.active).toBe(false)
    expect(log).toEqual([])
    expect(audio.get().output).toBeUndefined()
  })
})

describe('the volume', () => {
  it('shows the remote one while it plays and gives this device its own back', async () => {
    audio.play(songs(2), 0)
    audio.setVolume(0.8)
    const { r, log } = fakeRemote()
    const h = audio.attachRemote(r, 'Living room')
    await settle()
    expect(log).toEqual(['sync:jump:0']) // no volume sent on attach: the speakers keep theirs
    h.update({ volume: 0.3 })
    expect(audio.get().volume).toBe(0.3)
    audio.setVolume(0.4)
    await settle()
    expect(log.at(-1)).toBe('volume:0.4')
    h.release()
    expect(audio.get().volume).toBe(0.8)
  })
})
