// The always-mounted player shells follow the track and what the bar shows, not every tick of the position.
import { act, render } from '@testing-library/react'
import { Profiler } from 'react'
import { MemoryRouter } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { MiniPlayer } from '../MiniPlayer'
import { NowPlaying } from '../NowPlaying'
import { audio, resetAudioForTests, type QueueItem } from '../audio/engine'

const track: QueueItem = {
  key: 't1',
  trackId: 1,
  title: 'Трек',
  artist: 'Артист',
  album: 'Альбом',
  cover: '/api/images/local/1/poster',
  durationSec: 200,
  src: '/api/tracks/1/stream?accept=flac',
}

/** jsdom has no media pipeline: give the element the numbers a browser would. */
function at(el: HTMLMediaElement, currentTime: number) {
  Object.defineProperty(el, 'currentTime', { configurable: true, writable: true, value: currentTime })
  Object.defineProperty(el, 'duration', { configurable: true, writable: true, value: 200 })
  act(() => void el.dispatchEvent(new Event('timeupdate')))
}

beforeEach(() => {
  resetAudioForTests()
  vi.spyOn(HTMLMediaElement.prototype, 'play').mockImplementation(() => Promise.resolve())
  vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {})
  vi.spyOn(HTMLMediaElement.prototype, 'load').mockImplementation(() => {})
})
afterEach(() => resetAudioForTests())

describe('player shells', () => {
  it('the closed «Сейчас играет» does not render with the position; the mini bar only when its progress moves a step', () => {
    const commits = { now: 0, mini: 0 }
    render(
      <MemoryRouter>
        <Profiler id="now" onRender={() => commits.now++}>
          <NowPlaying open={false} onClose={() => {}} />
        </Profiler>
        <Profiler id="mini" onRender={() => commits.mini++}>
          <MiniPlayer />
        </Profiler>
      </MemoryRouter>,
    )
    act(() => audio.play([track], 0))
    const el = audio.element()!
    at(el, 5)
    expect(audio.get().positionSec).toBe(5)
    const before = { ...commits }
    for (const t of [5.05, 5.1, 5.2]) at(el, t) // the same quarter of a percent of 200 s
    expect(audio.get().positionSec).toBeCloseTo(5.2)
    expect(commits).toEqual(before)
    at(el, 100)
    expect(commits.mini).toBeGreaterThan(before.mini)
    expect(commits.now).toBe(before.now)
  })
})
