import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { audio, normalizeSupported, resetAudioForTests, type QueueItem } from '../engine'
import { itemGain, linearGain, loadNormalize, PREAMP_DB, resolveMode, saveNormalize, type TrackGain } from '../normalize'

const db = (lin: number) => 20 * Math.log10(lin)

function item(i: number, albumId: number, gain?: TrackGain): QueueItem {
  return { key: `t${i}`, trackId: i, title: `T${i}`, artist: 'A', albumId, cover: '', durationSec: 200, src: `/api/tracks/${i}/stream`, gain }
}

describe('gain math', () => {
  it('track gain plus the pre-amp, as a linear factor', () => {
    expect(db(linearGain({ trackDb: -10 }, 'track'))).toBeCloseTo(-10 + PREAMP_DB, 6)
  })

  it('an unknown peak never boosts (it might clip)', () => {
    expect(linearGain({ trackDb: 2 }, 'track')).toBe(1)
    expect(linearGain({ trackDb: -1, albumDb: 3 }, 'album')).toBe(1)
    // attenuation still applies
    expect(db(linearGain({ trackDb: -10 }, 'track'))).toBeCloseTo(-6, 6)
  })

  it('never louder than the peak allows (gain × peak ≤ 1)', () => {
    // -1 dB + 4 = +3 dB wanted, a peak of 0.9 allows only 1/0.9
    expect(linearGain({ trackDb: -1, trackPeak: 0.9 }, 'track')).toBeCloseTo(1 / 0.9, 9)
    // well below: the peak does not matter
    expect(db(linearGain({ trackDb: -10, trackPeak: 0.9 }, 'track'))).toBeCloseTo(-6, 6)
  })

  it('album mode takes the album gain and peak, else the track one', () => {
    const g: TrackGain = { trackDb: -10, trackPeak: 0.5, albumDb: -8, albumPeak: 1 }
    expect(db(linearGain(g, 'album'))).toBeCloseTo(-4, 6)
    expect(db(linearGain({ trackDb: -10 }, 'album'))).toBeCloseTo(-6, 6)
  })

  it('off, unknown or junk: unity', () => {
    expect(linearGain({ trackDb: -10 }, 'off')).toBe(1)
    expect(linearGain({ trackDb: null, albumDb: null }, 'album')).toBe(1)
    expect(linearGain(undefined, 'track')).toBe(1)
    expect(linearGain({ trackDb: Number.NaN }, 'track')).toBe(1)
  })

  it('a wild tag is clamped (−24 … +12 dB)', () => {
    expect(db(linearGain({ trackDb: 40, trackPeak: 0.01 }, 'track'))).toBeCloseTo(12, 6)
    expect(db(linearGain({ trackDb: -50 }, 'track'))).toBeCloseTo(-24, 6)
  })
})

describe('auto mode', () => {
  const album = [item(1, 7), item(2, 7), item(3, 7)]
  const mixed = [item(1, 7), item(2, 8), item(3, 9)]

  it('an album in order: by album; a mix or shuffle: by track', () => {
    expect(resolveMode('auto', album, 1, false)).toBe('album')
    expect(resolveMode('auto', album, 0, false)).toBe('album')
    expect(resolveMode('auto', album, 1, true)).toBe('track')
    expect(resolveMode('auto', mixed, 1, false)).toBe('track')
    expect(resolveMode('auto', [item(1, 7)], 0, false)).toBe('track')
    expect(resolveMode('track', album, 1, false)).toBe('track')
  })

  it('a book is never levelled', () => {
    const book: QueueItem = { key: 'b1f1', fileId: 1, title: 'B', artist: 'A', cover: '', durationSec: 1, src: '/x', gain: { trackDb: -10 } }
    expect(itemGain('track', [book], 0, false)).toBe(1)
  })
})

describe('the setting', () => {
  beforeEach(() => localStorage.clear())
  it('defaults to Авто and is remembered', () => {
    expect(loadNormalize()).toBe('auto')
    saveNormalize('off')
    expect(loadNormalize()).toBe('off')
    localStorage.setItem('vault.music.normalize', 'nonsense')
    expect(loadNormalize()).toBe('auto')
  })
})

// --- engine wiring ---

const els = () => [...document.querySelectorAll<HTMLAudioElement>('audio[data-vault-audio]')]
const active = () => els().find((e) => e.hasAttribute('data-active'))!
const other = () => els().find((e) => !e.hasAttribute('data-active'))!
const fire = (el: HTMLMediaElement, type: string) => el.dispatchEvent(new Event(type))
function media(el: HTMLMediaElement, currentTime: number, duration: number) {
  Object.defineProperty(el, 'currentTime', { configurable: true, writable: true, value: currentTime })
  Object.defineProperty(el, 'duration', { configurable: true, writable: true, value: duration })
}

interface FakeParam {
  value: number
  setValueAtTime: ReturnType<typeof vi.fn>
  setTargetAtTime: ReturnType<typeof vi.fn>
  cancelScheduledValues: ReturnType<typeof vi.fn>
}

let sources: HTMLMediaElement[]
let gains: FakeParam[]

let contexts: FakeContext[]

class FakeContext extends EventTarget {
  state = 'running'
  currentTime = 0
  constructor() {
    super()
    contexts.push(this)
  }
  /** Tests: the browser moves the context to `s`. */
  become(s: string) {
    this.state = s
    this.dispatchEvent(new Event('statechange'))
  }
  destination = {}
  createMediaElementSource(el: HTMLMediaElement) {
    if (sources.includes(el)) throw new DOMException('already connected', 'InvalidStateError')
    sources.push(el)
    return { connect: (n: unknown) => n }
  }
  createGain() {
    const gain: FakeParam = {
      value: 1,
      setValueAtTime: vi.fn((v: number) => (gain.value = v)),
      setTargetAtTime: vi.fn(),
      cancelScheduledValues: vi.fn(),
    }
    gains.push(gain)
    return { gain, connect: (n: unknown) => n }
  }
  resume() {
    return Promise.resolve()
  }
}

describe('engine levelling', () => {
  beforeEach(() => {
    localStorage.clear()
    resetAudioForTests()
    sources = []
    gains = []
    contexts = []
    vi.spyOn(HTMLMediaElement.prototype, 'play').mockImplementation(() => Promise.resolve())
    vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {})
    vi.spyOn(HTMLMediaElement.prototype, 'load').mockImplementation(() => {})
    vi.stubGlobal('fetch', vi.fn(async () => new Response(null, { status: 204 })))
  })
  afterEach(() => {
    resetAudioForTests()
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it('attenuation is the element volume, set before the track sounds; the preloaded one has its own', () => {
    vi.stubGlobal('AudioContext', FakeContext)
    saveNormalize('track')
    resetAudioForTests() // the engine reads the setting
    const q = [item(1, 7, { trackDb: -10 }), item(2, 8, { trackDb: -14 })]
    audio.play(q, 0)
    expect(db(active().volume)).toBeCloseTo(-6, 4)
    audio.setVolume(0.5)
    expect(active().volume).toBeCloseTo(0.5 * Math.pow(10, -6 / 20), 6)
    // 15 s before the end the idle element loads track 2 at its own level
    media(active(), 190, 200)
    fire(active(), 'timeupdate')
    expect(other().getAttribute('src')).toBe(q[1]!.src)
    expect(other().volume).toBeCloseTo(0.5 * Math.pow(10, -10 / 20), 6)
    // no boost needed: no Web Audio at all
    expect(sources).toHaveLength(0)
  })

  it('a boost goes through one GainNode per element, made once', () => {
    vi.stubGlobal('AudioContext', FakeContext)
    saveNormalize('track')
    resetAudioForTests()
    const q = [item(1, 7, { trackDb: 2, trackPeak: 0.5 }), item(2, 8, { trackDb: 1, trackPeak: 0.5 })]
    audio.play(q, 0)
    expect(sources).toEqual([active()])
    expect(db(gains[0]!.value)).toBeCloseTo(6, 4)
    expect(active().volume).toBe(1)
    audio.next()
    audio.previous()
    audio.next()
    // each element got its source once, whatever was loaded into it
    expect(new Set(sources).size).toBe(sources.length)
    expect(sources.length).toBeLessThanOrEqual(2)
  })

  it('Safari (AirPlay) never routes an element through Web Audio: the gain stops at 1', () => {
    vi.stubGlobal('AudioContext', FakeContext)
    vi.stubGlobal('WebKitPlaybackTargetAvailabilityEvent', class {})
    saveNormalize('track')
    resetAudioForTests()
    audio.play([item(1, 7, { trackDb: 2, trackPeak: 0.5 })], 0)
    expect(sources).toHaveLength(0)
    expect(active().volume).toBe(1)
  })

  it('changing the setting levels the playing track at once, smoothly when it has a GainNode', () => {
    vi.stubGlobal('AudioContext', FakeContext)
    saveNormalize('off')
    resetAudioForTests()
    audio.play([item(1, 7, { trackDb: 2, trackPeak: 0.25 })], 0)
    expect(active().volume).toBe(1)
    expect(sources).toHaveLength(0)
    audio.setNormalize('track')
    expect(loadNormalize()).toBe('track')
    expect(sources).toHaveLength(1)
    expect(gains[0]!.setTargetAtTime).toHaveBeenCalledWith(Math.pow(10, 6 / 20), 0, 0.05)
    audio.setNormalize('off')
    expect(gains[0]!.setTargetAtTime).toHaveBeenLastCalledWith(1, 0, 0.05)
  })

  it('a context that stops moves the element off the graph (never silent), and a run brings the boost back', () => {
    vi.stubGlobal('AudioContext', FakeContext)
    saveNormalize('track')
    resetAudioForTests()
    const q = [item(1, 7, { trackDb: 2, trackPeak: 0.5 })]
    audio.play(q, 0)
    const routed = active()
    expect(sources).toEqual([routed])
    const plays = (HTMLMediaElement.prototype.play as unknown as ReturnType<typeof vi.fn>).mock.calls.length
    contexts[0]!.become('suspended')
    // a fresh element plays the track on its own, at most at the file's level
    expect(active()).not.toBe(routed)
    expect(routed.isConnected).toBe(false)
    expect(active().getAttribute('src')).toBe(q[0]!.src)
    expect(active().volume).toBe(1)
    expect((HTMLMediaElement.prototype.play as unknown as ReturnType<typeof vi.fn>).mock.calls.length).toBe(plays + 1)
    expect(els()).toHaveLength(2)
    // the old element's events reach nothing
    fire(routed, 'pause')
    expect(audio.get().playing).toBe(true)
    contexts[0]!.become('running')
    expect(sources).toEqual([routed, active()])
  })

  it('a change of the setting ramps the element volume (no click)', () => {
    vi.useFakeTimers()
    saveNormalize('off')
    resetAudioForTests()
    audio.play([item(1, 7, { trackDb: -10 })], 0)
    expect(active().volume).toBe(1)
    audio.setNormalize('track')
    expect(active().volume).toBe(1) // not a step
    vi.advanceTimersByTime(60)
    const mid = active().volume
    expect(mid).toBeLessThan(1)
    expect(mid).toBeGreaterThan(Math.pow(10, -6 / 20))
    vi.advanceTimersByTime(200)
    expect(db(active().volume)).toBeCloseTo(-6, 4)
    vi.useRealTimers()
  })

  it('the preloaded track follows a shuffle (Авто: album in order → track)', () => {
    saveNormalize('auto')
    resetAudioForTests()
    const q = [item(1, 7, { trackDb: -10, albumDb: -14 }), item(2, 7, { trackDb: -6, albumDb: -14 }), item(3, 7, { trackDb: -2, albumDb: -14 })]
    audio.play(q, 0)
    media(active(), 190, 200)
    fire(active(), 'timeupdate')
    expect(other().getAttribute('src')).toBe(q[1]!.src)
    expect(db(other().volume)).toBeCloseTo(-10, 4) // album gain
    audio.setShuffle(true)
    const preloaded = audio.get().queue.find((x) => other().getAttribute('src') === x.src)!
    expect(db(other().volume)).toBeCloseTo((preloaded.gain!.trackDb as number) + 4, 4) // its track gain now
  })

  it('iPhone and iPad (single element): no levelling, Safari ignores the volume', () => {
    Object.defineProperty(navigator, 'maxTouchPoints', { configurable: true, value: 5 })
    vi.spyOn(navigator, 'userAgent', 'get').mockReturnValue('Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15')
    try {
      expect(normalizeSupported()).toBe(false)
      saveNormalize('track')
      resetAudioForTests()
      audio.play([item(1, 7, { trackDb: -10 })], 0)
      expect(active().volume).toBe(1)
    } finally {
      delete (navigator as unknown as { maxTouchPoints?: number }).maxTouchPoints
    }
  })

  it('an audiobook keeps its level', () => {
    saveNormalize('track')
    resetAudioForTests()
    audio.play([item(1, 7, { trackDb: -10 })], 0, { kind: 'audiobook', bookId: 5 })
    expect(active().volume).toBe(1)
  })
})
