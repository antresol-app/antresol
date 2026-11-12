// The live edge rules of the channel player (liveEdge.ts): «В эфир» more
// than 10 s behind, a resume at the edge once the pause outlasted what the
// stream holds (a minute on our channels, the seekable range on an
// external one), nothing of it for a stream that is not live, and which
// controls show.
import { describe, expect, it } from 'vitest'
import { BEHIND_LIVE_S, OWN_WINDOW_S, behindLive, chromeShown, liveEdge, liveWindow, readClock, resumeTarget, showGoLive, type LiveClock } from '../liveEdge'

const clock = (position: number, edge = 1000, start = edge - 60): LiveClock => ({ live: true, position, edge, start })

describe('the live edge', () => {
  it('is hls.js’s sync position when it has one, never past the stream’s end', () => {
    expect(liveEdge({ syncPosition: 982, seekableEnd: 1000, startOffset: 0 })).toBe(982)
    expect(liveEdge({ syncPosition: 1010, seekableEnd: 1000, startOffset: 0 })).toBe(1000)
  })
  it('is the end less where playback started for the browser’s own HLS', () => {
    expect(liveEdge({ syncPosition: null, seekableEnd: 1000, startOffset: 18 })).toBe(982)
    expect(liveEdge({ seekableEnd: 10, startOffset: 18 })).toBe(0)
  })
})

describe('«В эфир»', () => {
  it('shows only more than 10 s behind the edge', () => {
    expect(showGoLive(clock(1000))).toBe(false)
    expect(showGoLive(clock(1000 - BEHIND_LIVE_S))).toBe(false)
    expect(showGoLive(clock(1000 - BEHIND_LIVE_S - 0.5))).toBe(true)
    expect(behindLive(clock(970))).toBe(30)
  })
  it('a little ahead of the edge (hls.js catching up) is live', () => {
    expect(behindLive(clock(1003))).toBe(0)
    expect(showGoLive(clock(1003))).toBe(false)
  })
  it('never shows for a stream that is not live', () => {
    expect(showGoLive({ live: false, position: 5, edge: 0, start: 0 })).toBe(false)
    expect(behindLive({ live: false, position: 5, edge: 900, start: 0 })).toBe(0)
  })
})

describe('a resume after a pause', () => {
  it('our channel: goes on from the stopped picture within the minute', () => {
    expect(resumeTarget({ own: true, pausedMs: 20_000, behindAtPause: 0, clock: clock(980, 1000) })).toBeNull()
    expect(resumeTarget({ own: true, pausedMs: 45_000, behindAtPause: 10, clock: clock(945, 1000) })).toBeNull()
  })
  it('our channel: at the edge once the pause (plus how far behind it was) outlasts the minute', () => {
    expect(resumeTarget({ own: true, pausedMs: OWN_WINDOW_S * 1000, behindAtPause: 0, clock: clock(940, 1000) })).toBe(1000)
    expect(resumeTarget({ own: true, pausedMs: 45_000, behindAtPause: 20, clock: clock(935, 1000) })).toBe(1000)
    expect(resumeTarget({ own: true, pausedMs: 5 * 60_000, behindAtPause: 0, clock: clock(700, 1000) })).toBe(1000)
  })
  it('an external channel: its seekable range is the window', () => {
    // a provider keeping two hours (a catch-up window): a ten-minute pause comes back where it stopped
    const dvr = clock(400, 1000, -6200)
    expect(liveWindow(false, dvr)).toBe(7200)
    expect(resumeTarget({ own: false, pausedMs: 10 * 60_000, behindAtPause: 0, clock: dvr })).toBeNull()
    // a provider keeping 30 s: a 40 s pause resumes at the edge
    const short = clock(960, 1000, 970)
    expect(resumeTarget({ own: false, pausedMs: 40_000, behindAtPause: 0, clock: short })).toBe(1000)
  })
  it('at the edge when the stopped picture fell out of the stream', () => {
    expect(resumeTarget({ own: false, pausedMs: 1000, behindAtPause: 0, clock: clock(900, 1000, 950) })).toBe(1000)
  })
  it('a stream that is not live goes on from where it stopped', () => {
    expect(resumeTarget({ own: false, pausedMs: 3_600_000, behindAtPause: 0, clock: { live: false, position: 50, edge: 0, start: 0 } })).toBeNull()
  })
})

describe('the clock', () => {
  const ranges = (start: number, end: number) => ({ length: 1, start: () => start, end: () => end })
  it('hls.js: live by the playlist, the edge its sync position', () => {
    const c = readClock({ video: { currentTime: 950, duration: 1010, seekable: ranges(940, 1000) }, hls: { liveSyncPosition: 982, latestLevelDetails: { live: true } }, startOffset: 0 })
    expect(c).toEqual({ live: true, position: 950, edge: 982, start: 940 })
  })
  it('hls.js: a playlist with an end is not live', () => {
    expect(readClock({ video: { currentTime: 12, duration: 600, seekable: ranges(0, 600) }, hls: { liveSyncPosition: null, latestLevelDetails: { live: false } }, startOffset: 0 }).live).toBe(false)
  })
  it('the browser’s HLS: live is an infinite duration', () => {
    expect(readClock({ video: { currentTime: 70, duration: Infinity, seekable: ranges(30, 90) }, startOffset: 18 })).toEqual({ live: true, position: 70, edge: 72, start: 30 })
    expect(readClock({ video: { currentTime: 70, duration: 600, seekable: ranges(0, 600) }, startOffset: 0 }).live).toBe(false)
  })
  it('nothing seekable yet: not live', () => {
    expect(readClock({ video: { currentTime: 0, duration: Infinity, seekable: { length: 0, start: () => 0, end: () => 0 } }, startOffset: 0 }).live).toBe(false)
  })
})

describe('what shows', () => {
  const base = { poked: false, paused: false, playing: true, upright: false, info: false, panel: false, menuOpen: false }
  it('a tap or the pointer brings the bars and the centre play/pause; a quiet player hides both', () => {
    expect(chromeShown({ ...base, poked: true })).toEqual({ bars: true, center: true })
    expect(chromeShown(base)).toEqual({ bars: false, center: false })
  })
  it('paused: both stay', () => {
    expect(chromeShown({ ...base, paused: true })).toEqual({ bars: true, center: true })
  })
  it('an upright phone keeps the bars but the centre still hides', () => {
    expect(chromeShown({ ...base, upright: true })).toEqual({ bars: true, center: false })
    expect(chromeShown({ ...base, upright: true, poked: true })).toEqual({ bars: true, center: true })
  })
  it('no centre button before the channel plays (the spinner is there)', () => {
    expect(chromeShown({ ...base, playing: false, poked: true })).toEqual({ bars: true, center: false })
  })
  it('the channel list over the picture hides the centre button', () => {
    expect(chromeShown({ ...base, poked: true, panel: true })).toEqual({ bars: true, center: false })
  })
})

describe('the number column', () => {
  it('is as wide as the longest number in the list', async () => {
    const { numDigits } = await import('../model')
    expect(numDigits([{ number: 1 }, { number: 12 }])).toBe(2)
    expect(numDigits([{ number: 5 }, { number: 1299 }])).toBe(4)
    expect(numDigits([])).toBe(1)
  })
})
