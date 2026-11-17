// VideoPlayer's `saveProgress`: the position goes there (an addon's input),
// never to the library's /api/progress.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, waitFor } from '@testing-library/react'
import type { MediaFile } from '../../api/types'
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

const mom = { id: 2, name: 'Мама', avatar: 'grad-1', hasPassword: false, isAdmin: false, isKid: false, maxRating: null }
const json = (b: unknown, status = 200) => new Response(b === undefined ? null : JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } })
const file: MediaFile = { id: 0, name: 'film.mkv', size: 0, durationMs: 60_000, audio: [], subs: [] }
let urls: string[]

describe('VideoPlayer saveProgress', () => {
  beforeEach(() => {
    session.reset()
    urls = []
    vi.spyOn(HTMLMediaElement.prototype, 'play').mockResolvedValue(undefined)
    vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {})
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => (urls.push(String(input)), String(input).endsWith('/api/login') ? json(mom) : json({}))))
  })
  afterEach(() => vi.unstubAllGlobals())

  it('saves through saveProgress, even with an item, not /api/progress', async () => {
    await session.login(2)
    const start = vi.fn(async () => ({ mode: 'direct', url: '/x', startSec: 30, durationMs: 60_000, audioIndex: 0, audio: [], subtitles: [] }))
    const saveProgress = vi.fn(() => Promise.resolve())
    const { unmount } = render(
      <VideoPlayer itemId={9} title="Фильм" parts={[file]} startSec={30} onClose={() => {}} start={start as never} saveProgress={saveProgress} />,
    )
    await waitFor(() => expect(start).toHaveBeenCalled())
    unmount()
    expect(saveProgress).toHaveBeenCalledWith(30_000, 60_000, true)
    expect(urls.some((u) => u.includes('/api/progress'))).toBe(false)
  })
})
