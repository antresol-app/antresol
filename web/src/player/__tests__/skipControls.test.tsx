import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import type { MediaFile, PlaySegment } from '../../api/types'

vi.mock('hls.js', () => {
  class FakeHls {
    static Events = { ERROR: 'error' }
    static ErrorTypes = { NETWORK_ERROR: 'networkError' }
    static isSupported = () => true
    on() {}
    loadSource() {}
    attachMedia() {}
    destroy() {}
  }
  return { default: FakeHls }
})

import { VideoPlayer, type UpNext } from '../VideoPlayer'

const DUR = 1300
const json = (b: unknown, status = 200) => new Response(b === undefined ? null : JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } })

async function player(segments: PlaySegment[], opts: { next?: UpNext; onClose?: () => void; duration?: number } = {}) {
  const duration = opts.duration ?? DUR
  const start = vi.fn(async () => ({ mode: 'direct', url: '/api/stream/5', startSec: 0, durationMs: duration * 1000, audioIndex: 0, audio: [], subtitles: [], segments }))
  render(
    <VideoPlayer
      itemId={9}
      title="Офис"
      parts={[{ id: 5, name: 'office.mkv', durationMs: duration * 1000 } as MediaFile]}
      startSec={0}
      next={opts.next}
      onClose={opts.onClose ?? (() => {})}
      start={start as never}
    />,
  )
  await waitFor(() => expect(start).toHaveBeenCalled())
  const video = screen.getByTestId('player').querySelector('video')!
  // jsdom keeps no playback position: one the test moves
  let at = 0
  Object.defineProperty(video, 'currentTime', { configurable: true, get: () => at, set: (v: number) => void (at = v) })
  await act(async () => {
    fireEvent.play(video)
    fireEvent.playing(video)
  })
  const to = async (t: number) => {
    at = t
    await act(async () => {
      fireEvent.timeUpdate(video)
    })
  }
  return { video, to, now: () => at }
}

describe('VideoPlayer: skip segments', () => {
  beforeEach(() => {
    localStorage.clear()
    vi.stubGlobal('fetch', vi.fn(async () => json(undefined, 204)))
    vi.spyOn(HTMLMediaElement.prototype, 'play').mockResolvedValue(undefined)
    vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {})
  })
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it('offers «Пропустить вступление» inside the intro and seeks to its end', async () => {
    const p = await player([{ kind: 'intro', startSec: 60, endSec: 100 }])
    await p.to(30)
    expect(screen.queryByRole('button', { name: 'Пропустить вступление' })).toBeNull()
    await p.to(61)
    fireEvent.click(screen.getByRole('button', { name: 'Пропустить вступление' }))
    expect(p.now()).toBe(100)
    await p.to(100)
    expect(screen.queryByRole('button', { name: 'Пропустить вступление' })).toBeNull()
  })

  it('skips with Enter while the pill shows, and leaves Enter alone otherwise', async () => {
    const p = await player([{ kind: 'recap', startSec: 0, endSec: 45 }])
    await p.to(5)
    expect(screen.getByRole('button', { name: 'Пропустить пересказ' })).toBeInTheDocument()
    fireEvent.keyDown(window, { code: 'KeyS' })
    expect(p.now()).toBe(45)
    await p.to(50)
    fireEvent.keyDown(window, { code: 'KeyS' })
    expect(p.now()).toBe(50)
  })

  it('ignores segments under 3 s', async () => {
    const p = await player([{ kind: 'intro', startSec: 60, endSec: 62 }])
    await p.to(61)
    expect(screen.queryByRole('button', { name: 'Пропустить вступление' })).toBeNull()
  })

  it('«Пропускать сам» skips the intro with a note that takes it back', async () => {
    localStorage.setItem('vault.player.autoSkipIntro', '1')
    const p = await player([{ kind: 'intro', startSec: 60, endSec: 100 }])
    await p.to(60.5)
    expect(p.now()).toBe(100)
    await p.to(100)
    expect(screen.getByRole('status')).toHaveTextContent('Вступление пропущено')
    fireEvent.click(screen.getByRole('button', { name: 'Вернуть' }))
    expect(p.now()).toBe(60)
    // taken back: it plays (the pill is there, no second skip)
    await p.to(61)
    expect(p.now()).toBe(61)
    expect(screen.getByRole('button', { name: 'Пропустить вступление' })).toBeInTheDocument()
  })

  it('«Следующая серия» fills over 10 s at the credits, then plays the next episode', async () => {
    const play = vi.fn()
    const p = await player([{ kind: 'credits', startSec: 1250, endSec: DUR }], { next: { code: 'С1 Э2', title: 'Вторая', play } })
    await p.to(1251)
    expect(screen.getByRole('button', { name: 'Следующая серия' })).toBeInTheDocument()
    // the old card of the last 30 s is not shown with the credits known
    expect(screen.queryByRole('complementary', { name: 'Следующая серия' })).toBeNull()
    await p.to(1256)
    expect(play).not.toHaveBeenCalled()
    await p.to(1261.5)
    await waitFor(() => expect(play).toHaveBeenCalledTimes(1))
  })

  it('«Смотреть титры» dismisses it', async () => {
    const play = vi.fn()
    const p = await player([{ kind: 'credits', startSec: 1250, endSec: DUR }], { next: { code: 'С1 Э2', title: 'Вторая', play } })
    await p.to(1251)
    fireEvent.click(screen.getByRole('button', { name: 'Смотреть титры' }))
    await p.to(1270)
    expect(screen.queryByRole('button', { name: 'Следующая серия' })).toBeNull()
    expect(play).not.toHaveBeenCalled()
  })

  it('a scene after the credits: the button only, no autoplay', async () => {
    const play = vi.fn()
    const p = await player([{ kind: 'credits', startSec: 1200, endSec: 1250 }], { next: { code: 'С1 Э2', title: 'Вторая', play } })
    await p.to(1201)
    await p.to(1230)
    expect(play).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: 'Следующая серия' }))
    await waitFor(() => expect(play).toHaveBeenCalledTimes(1))
  })

  it('the last episode: «Пропустить титры» ends it', async () => {
    const onClose = vi.fn()
    const p = await player([{ kind: 'credits', startSec: 1250, endSec: DUR }], { onClose })
    await p.to(1251)
    fireEvent.click(screen.getByRole('button', { name: 'Пропустить титры' }))
    await waitFor(() => expect(onClose).toHaveBeenCalled())
  })

  it('after «Смотреть титры» the card of the last 30 s comes back (the end still plays the next one)', async () => {
    const p = await player([{ kind: 'credits', startSec: 1250, endSec: DUR }], { next: { code: 'С1 Э2', title: 'Вторая', play: () => {} } })
    await p.to(1251)
    expect(screen.queryByRole('complementary', { name: 'Следующая серия' })).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: 'Смотреть титры' }))
    await p.to(1280)
    expect(screen.getByRole('complementary', { name: 'Следующая серия' })).toBeInTheDocument()
  })

  it('a scene after the credits shows the card of the last 30 s', async () => {
    const p = await player([{ kind: 'credits', startSec: 1200, endSec: 1240 }], { next: { code: 'С1 Э2', title: 'Вторая', play: () => {} } })
    await p.to(1285)
    expect(screen.getByRole('complementary', { name: 'Следующая серия' })).toBeInTheDocument()
  })

  it('a note after an automatic skip leaves the next pill on screen', async () => {
    localStorage.setItem('vault.player.autoSkipRecap', '1')
    const p = await player([
      { kind: 'recap', startSec: 0, endSec: 40 },
      { kind: 'intro', startSec: 40, endSec: 80 },
    ])
    // skipped as playback starts at 0:00
    expect(p.now()).toBe(40)
    await p.to(40.5)
    expect(screen.getByRole('status')).toHaveTextContent('Пересказ пропущен')
    expect(screen.getByRole('button', { name: 'Пропустить вступление' })).toBeInTheDocument()
    // the pill takes the key
    fireEvent.keyDown(window, { code: 'KeyS' })
    expect(p.now()).toBe(80)
  })

  it('without credits the card of the last 30 s stays', async () => {
    const p = await player([], { next: { code: 'С1 Э2', title: 'Вторая', play: () => {} } })
    await p.to(DUR - 20)
    expect(screen.getByRole('complementary', { name: 'Следующая серия' })).toBeInTheDocument()
  })
})
