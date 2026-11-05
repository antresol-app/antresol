import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
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

const adult = { id: 2, name: 'Мама', avatar: 'grad-1', hasPassword: false, isAdmin: false, isKid: false, maxRating: null }
const kid = { ...adult, id: 3, name: 'Вася', isKid: true }
const json = (b: unknown, status = 200) => new Response(JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } })

async function open(user: typeof adult) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (url.endsWith('/api/login')) return json(user)
      if (url.endsWith('/api/items/40/voiceovers')) return json({ candidates: [], videoReleases: [], failed: [] })
      if (url.endsWith('/api/items/40/voiceovers/downloads')) return json([])
      return json({})
    }),
  )
  await session.login(user.id)
  const start = vi.fn(async () => ({
    mode: 'direct', url: '/api/stream/5', startSec: 0, durationMs: 60000, audioIndex: 1,
    audio: [{ index: 1, codec: 'aac', lang: 'eng', title: '', channels: 2, default: true }], subtitles: [],
  }))
  render(
    <VideoPlayer itemId={41} title="Миллиарды" parts={[{ id: 5, name: 'e1.mkv', durationMs: 60000 } as MediaFile]} startSec={0}
      onClose={() => {}} start={start as never} voiceOverItem={{ id: 40, title: 'Миллиарды' }} />,
  )
  await waitFor(() => expect(start).toHaveBeenCalled())
  await userEvent.click(screen.getByRole('button', { name: 'Аудио и субтитры' }))
  return screen.findByRole('menu', { name: 'Аудио и субтитры' })
}

describe('VideoPlayer: «Найти озвучку…»', () => {
  beforeEach(() => {
    session.reset()
    localStorage.clear()
    vi.spyOn(HTMLMediaElement.prototype, 'play').mockResolvedValue(undefined)
    vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {})
  })
  afterEach(() => vi.unstubAllGlobals())

  it('a kid profile has no such row', async () => {
    const menu = await open(kid)
    expect(within(menu).queryByRole('menuitem', { name: 'Найти озвучку…' })).not.toBeInTheDocument()
  })
})
