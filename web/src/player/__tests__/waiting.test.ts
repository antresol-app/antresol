import { describe, expect, it } from 'vitest'
import { audioForPart, endedEarly, mediaErrorReason, recover, waitVerdict, type WaitContext } from '../recovery'
import { langKey } from '../timeline'

const base: WaitContext = { phase: 'start', mode: 'remux', slow: false, progressed: false, idleChecks: 0, elapsedSec: 31 }

describe('a slow torrent is not a device problem (F14, F16)', () => {
  it('keeps waiting while the torrent brings data, however long', () => {
    for (const phase of ['start', 'stall'] as const) {
      for (const mode of ['direct', 'remux', 'transcode']) {
        expect(waitVerdict({ ...base, phase, mode, slow: true, progressed: true, idleChecks: 0, elapsedSec: 900 })).toBe('wait')
      }
    }
  })

  it('calls it stalled — never «media» — after minutes without any data', () => {
    const c = { ...base, slow: true, progressed: false }
    expect(waitVerdict({ ...c, idleChecks: 0 })).toBe('wait')
    expect(waitVerdict({ ...c, idleChecks: 1 })).toBe('wait')
    expect(waitVerdict({ ...c, idleChecks: 2 })).toBe('stalled')
    expect(waitVerdict({ ...c, mode: 'direct', idleChecks: 2 })).toBe('stalled')
  })

  it('restarts a stalled torrent, then shows its own error — no transcode fallback', () => {
    expect(recover({ restarts: 0, fallbackUsed: false }, 'stalled')).toEqual({ action: 'restart' })
    expect(recover({ restarts: 2, fallbackUsed: false }, 'stalled')).toEqual({ action: 'error' })
  })

  it('a network error on a torrent is the swarm', () => {
    expect(mediaErrorReason('direct', 2, true)).toBe('stalled')
    expect(mediaErrorReason('remux', 2, true)).toBe('stalled')
    expect(mediaErrorReason('direct', 4, true)).toBe('media') // a real decode failure still falls back
  })
})

describe('library timers', () => {
  it('waits for a slow first segment (4K transcode) instead of failing at 20–30 s', () => {
    expect(waitVerdict({ ...base, mode: 'transcode', hls: { gone: false, running: true, segments: 0 } })).toBe('wait')
    expect(waitVerdict({ ...base, mode: 'transcode', elapsedSec: 200, hls: { gone: false, running: true, segments: 0 } })).toBe('media')
  })

  it('a stream that is there but never starts is a codec problem', () => {
    expect(waitVerdict({ ...base, hls: { gone: false, running: true, segments: 4 } })).toBe('media')
    expect(waitVerdict({ ...base, mode: 'direct' })).toBe('media')
  })

  it('a session that went away is restarted', () => {
    expect(waitVerdict({ ...base, phase: 'stall', hls: { gone: true, running: false, segments: 0 } })).toBe('expired')
    expect(waitVerdict({ ...base, slow: true, progressed: true, hls: { gone: true, running: false, segments: 0 } })).toBe('expired')
  })

  it('mid-play, a transcode that still writes segments catches up', () => {
    expect(waitVerdict({ ...base, phase: 'stall', mode: 'transcode', progressed: true, hls: { gone: false, running: true, segments: 9 } })).toBe('wait')
    expect(waitVerdict({ ...base, phase: 'stall', mode: 'transcode', progressed: false, hls: { gone: false, running: true, segments: 9 } })).toBe('expired')
  })
})

describe('multi-part movies (F25)', () => {
  const cd1 = [
    { index: 1, lang: 'rus', default: true },
    { index: 2, lang: 'eng' },
  ]
  it('carries the language, not the stream index', () => {
    const cd2 = [
      { index: 1, lang: 'eng', default: true },
      { index: 3, lang: 'rus' },
    ]
    expect(audioForPart(cd1, 1, cd2, langKey)).toBe(3)
    expect(audioForPart(cd1, 2, cd2, langKey)).toBeUndefined() // English is cd2's default: let it play direct
  })
  it('falls back to the default when the language is missing', () => {
    expect(audioForPart(cd1, 2, [{ index: 1, lang: 'rus', default: true }], langKey)).toBeUndefined()
    expect(audioForPart(cd1, 9, [{ index: 1, lang: 'rus' }], langKey)).toBeUndefined()
  })
})

describe('a stream that broke off is not the end (F24)', () => {
  it('only an end near the duration finishes the movie', () => {
    expect(endedEarly(7200, 3600)).toBe(true)
    expect(endedEarly(7200, 7195)).toBe(false)
    expect(endedEarly(0, 10)).toBe(false)
  })
})
