import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import type { MediaFile, ScreenStatus } from '../../api/types'
import { session } from '../../state/session'

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

import { VideoPlayer } from '../VideoPlayer'

const json = (b: unknown) => new Response(JSON.stringify(b), { status: 200, headers: { 'Content-Type': 'application/json' } })

const over: ScreenStatus = {
  limited: true,
  day: '2026-11-02',
  hours: { from: '08:00', to: '20:30' },
  video: { limitMin: null, bonusMin: 0, usedSec: 0, remainingSec: 0, allowed: false, reason: 'hours', message: 'Сейчас нельзя смотреть: с 20:30 до 08:00' },
  audio: null,
  videoLimited: true,
  audioLimited: false,
}

function player() {
  const start = vi.fn(async () => ({ mode: 'direct', url: '/api/stream/5', startSec: 0, durationMs: 3_600_000, audioIndex: 0, audio: [], subtitles: [] }))
  render(
    <VideoPlayer
      itemId={9}
      title="Смешарики"
      parts={[{ id: 5, name: 'smeshariki.mkv', durationMs: 3_600_000 } as MediaFile]}
      startSec={0}
      onClose={() => {}}
      start={start as never}
    />,
  )
}

// A kid's player outside the allowed hours: the friendly stop, not an error.
describe('VideoPlayer: screen time', () => {
  let pause: ReturnType<typeof vi.spyOn>
  beforeEach(() => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input)
        if (url.endsWith('/api/login')) return json({ id: 3, name: 'Вася', avatar: 'grad-1', hasPassword: false, isAdmin: false, isKid: true, maxRating: '6+' })
        if (url.endsWith('/api/me/screen-time')) return json(over)
        return json({})
      }),
    )
    vi.spyOn(HTMLMediaElement.prototype, 'play').mockResolvedValue(undefined)
    pause = vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {})
  })
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
    session.reset()
  })

  it('stops a kid with the message', async () => {
    await session.login(3)
    player()
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('Пора отдохнуть')
    expect(alert).toHaveTextContent('Сейчас нельзя смотреть: с 20:30 до 08:00')
    expect(pause).toHaveBeenCalled()
    // nothing plays on: a media key's play is paused again, keys are off
    const calls = pause.mock.calls.length
    const video = document.querySelector('video')!
    video.dispatchEvent(new Event('play'))
    expect(pause.mock.calls.length).toBeGreaterThan(calls)
  })

  it('never asks for an adult', async () => {
    player()
    await new Promise((r) => setTimeout(r, 20))
    const calls = (fetch as unknown as { mock: { calls: unknown[][] } }).mock.calls.map((c) => String(c[0]))
    expect(calls.filter((u) => u.endsWith('/api/me/screen-time'))).toEqual([])
    expect(screen.queryByText('Пора отдохнуть')).toBeNull()
  })
})
