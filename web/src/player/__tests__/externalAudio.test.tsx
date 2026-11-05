import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { MediaFile, PlaybackPrefs, Track, TrackPrefs } from '../../api/types'
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
// the file's English track and «RUS Sound/Show.S01E02.LostFilm.ac3» beside it
const audio: Track[] = [
  { index: 1, codec: 'aac', lang: 'eng', title: '', channels: 2, default: true },
  { index: 2000, codec: 'ac3', lang: 'ru', title: 'LostFilm', channels: 6, default: false, external: true, src: '1a2b3c4d', studio: 'LostFilm' },
]
const prefs: PlaybackPrefs = { audioLangs: ['en'], subMode: 'off', subLangs: [], subSize: 'm', subBackground: 'none', custom: true }
const file: MediaFile = { id: 5, name: 'e02.mkv', size: 1, durationMs: 60000, audio, subs: [] }

describe('VideoPlayer: an audio file beside the video', () => {
  beforeEach(() => {
    session.reset()
    localStorage.clear()
    vi.spyOn(HTMLMediaElement.prototype, 'play').mockResolvedValue(undefined)
    vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {})
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input)
        if (url.endsWith('/api/login')) return json(mom)
        if (url.includes('/track-prefs') && init?.method === 'PUT') return json(undefined, 204)
        return json({})
      }),
    )
  })
  afterEach(() => vi.unstubAllGlobals())

  const play = async (tracks: TrackPrefs) => {
    await session.login(2)
    const start = vi.fn(async (_f: MediaFile, body: { audioIndex?: number }) => ({
      mode: body.audioIndex === 2000 ? 'remux' : 'direct', url: body.audioIndex === 2000 ? '/api/hls/s1/index.m3u8' : '/api/stream/5',
      startSec: 0, durationMs: 60000, audioIndex: body.audioIndex ?? 1, audio, subtitles: [],
    }))
    render(<VideoPlayer itemId={9} title="Сёгун" parts={[file]} startSec={0} onClose={() => {}} start={start as never} tracks={tracks} />)
    await waitFor(() => expect(start).toHaveBeenCalled())
    return start
  }

  it('is listed like the file’s own tracks, «Русский» with the studio and channels; choosing it sends its id', async () => {
    const start = await play({ prefs, series: null, offsets: {} })
    expect(start.mock.calls[0]![1]).not.toHaveProperty('audioSrc')
    await userEvent.click(screen.getByRole('button', { name: 'Аудио и субтитры' }))
    const menu = await screen.findByRole('menu', { name: 'Аудио и субтитры' })
    const item = within(menu).getByRole('menuitemradio', { name: /Русский/ })
    expect(item).toHaveTextContent('Русский')
    expect(item).toHaveTextContent('LostFilm · 5.1')
    expect(item).not.toHaveTextContent(/внешн|external/i)
    await userEvent.click(item)
    await waitFor(() => expect(start).toHaveBeenCalledTimes(2))
    expect(start.mock.calls[1]![1]).toMatchObject({ audioIndex: 2000, audioSrc: '1a2b3c4d' })
  })

  it('the voice remembered for the series picks the external track of the next episode', async () => {
    const start = await play({ prefs, series: { audio: { lang: 'ru', title: 'LostFilm' }, subtitle: null, subtitlesOff: false }, offsets: {} })
    expect(start.mock.calls[0]![1]).toMatchObject({ audioIndex: 2000, audioSrc: '1a2b3c4d' })
  })
})
