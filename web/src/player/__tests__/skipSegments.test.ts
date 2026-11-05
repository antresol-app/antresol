import { describe, expect, it } from 'vitest'
import { countdown, creditsToEnd, pillVisible, postCreditsScene, segmentAt, usableSegments } from '../skipSegments'

describe('skip segments', () => {
  const segs = usableSegments([
    { kind: 'credits', startSec: 1250, endSec: 1300 },
    { kind: 'intro', startSec: 89.3, endSec: 120 },
    { kind: 'recap', startSec: 0, endSec: 2 }, // under 3 s
    { kind: 'preview', startSec: 10, endSec: 30 }, // unknown kind
  ])

  it('keeps known kinds of 3 s and more, in time order', () => {
    expect(segs.map((s) => s.kind)).toEqual(['intro', 'credits'])
    expect(usableSegments(undefined)).toEqual([])
  })

  it('finds the segment playback is in', () => {
    expect(segmentAt(segs, 89)).toBeUndefined()
    expect(segmentAt(segs, 89.3)?.kind).toBe('intro')
    expect(segmentAt(segs, 119.9)).toBeUndefined() // a seek to the end lands just before it
    expect(segmentAt(segs, 1260)?.kind).toBe('credits')
  })

  it('tells a scene after the credits', () => {
    expect(postCreditsScene({ kind: 'credits', startSec: 1200, endSec: 1250 }, 1300)).toBe(true)
    expect(postCreditsScene({ kind: 'credits', startSec: 1200, endSec: 1290 }, 1300)).toBe(false)
    expect(creditsToEnd({ kind: 'credits', startSec: 1200, endSec: 1300 }, 1300)).toBe(true)
    expect(creditsToEnd({ kind: 'credits', startSec: 1200, endSec: 1250 }, 1300)).toBe(false)
  })

  it('fills the countdown over 10 s of playback', () => {
    expect(countdown(1250, 1250)).toBe(0)
    expect(countdown(1250, 1255)).toBe(0.5)
    expect(countdown(1250, 1290)).toBe(1)
  })

  it('shows a pill 8 s alone, then with the controls', () => {
    expect(pillVisible(0, 7999, false)).toBe(true)
    expect(pillVisible(0, 8000, false)).toBe(false)
    expect(pillVisible(0, 60_000, true)).toBe(true)
  })
})
