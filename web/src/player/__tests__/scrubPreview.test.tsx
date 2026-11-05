import { afterEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import type { MediaFile, TrickplayIndex } from '../../api/types'
import { session } from '../../state/session'
import { Scrubber } from '../Scrubber'

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

const MIN = 60_000
const ix: TrickplayIndex = { version: 1, key: 'abc', interval: 10, width: 320, height: 180, columns: 10, rows: 10, count: 360, sheets: 4 }
const json = (b: unknown, status = 200) => new Response(b === undefined ? null : JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } })
const slider = () => screen.getByRole('slider', { name: 'Позиция' })

/** The track is 1000 px wide at x 0 (jsdom lays nothing out). */
function layOut() {
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({ left: 0, top: 0, width: 1000, height: 44, right: 1000, bottom: 44, x: 0, y: 0, toJSON() {} } as DOMRect)
}

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

describe('Scrubber preview', () => {
  const preview = { index: ix, sheetUrl: (n: number) => `/sheet/${n}.jpg` }

  it('shows the tile of the hovered time above the track, with the time', () => {
    layOut()
    render(<Scrubber time={0} duration={3600} buffered={0} onSeek={() => {}} preview={preview} />)
    // 1250 s: frame 125, sheet 1, column 5, row 2
    fireEvent.pointerMove(slider(), { clientX: (1250 / 3600) * 1000, pointerType: 'mouse' })
    const card = screen.getByTestId('scrub-preview')
    expect(card).toHaveTextContent('20:50')
    const frame = card.firstElementChild as HTMLElement
    expect(frame.style.backgroundImage).toBe('url("/sheet/1.jpg")')
    expect(frame.style.backgroundPosition).toBe('-960px -216px')
    fireEvent.pointerLeave(slider())
    expect(screen.queryByTestId('scrub-preview')).toBeNull()
  })

  it('follows a touch drag', () => {
    layOut()
    const onSeek = vi.fn()
    render(<Scrubber time={0} duration={3600} buffered={0} onSeek={onSeek} preview={preview} />)
    const s = slider()
    s.setPointerCapture = () => {}
    fireEvent.pointerDown(s, { clientX: 100, pointerType: 'touch', button: 0, pointerId: 1 })
    fireEvent.pointerMove(s, { clientX: 500, pointerType: 'touch', pointerId: 1 })
    const frame = screen.getByTestId('scrub-preview').firstElementChild as HTMLElement
    expect(frame.style.backgroundImage).toBe('url("/sheet/1.jpg")') // 1800 s: frame 180
    fireEvent.pointerUp(s, { clientX: 500, pointerType: 'touch', pointerId: 1 })
    expect(onSeek).toHaveBeenCalledWith(1800)
    expect(screen.queryByTestId('scrub-preview')).toBeNull()
  })

  it('is the time alone without previews', () => {
    layOut()
    render(<Scrubber time={0} duration={3600} buffered={0} onSeek={() => {}} />)
    fireEvent.pointerMove(slider(), { clientX: 500, pointerType: 'mouse' })
    expect(screen.queryByTestId('scrub-preview')).toBeNull()
    expect(screen.getByText('30:00')).toBeInTheDocument()
  })
})

describe('VideoPlayer previews', () => {
  const adult = { id: 1, name: 'Александр', avatar: 'grad-1', hasPassword: true, isAdmin: true, isKid: false, maxRating: null }

  async function play(trickplay: 'ready' | 'none') {
    const urls: string[] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input)
        urls.push(url)
        if (url.endsWith('/api/login')) return json(adult)
        if (url.endsWith('/api/files/5/trickplay')) return trickplay === 'ready' ? json(ix) : json({ error: 'Превью для перемотки ещё не готовы' }, 404)
        if (url.includes('/api/progress/')) return json(undefined, 204)
        return json({})
      }),
    )
    await session.login(adult.id)
    vi.spyOn(HTMLMediaElement.prototype, 'play').mockResolvedValue(undefined)
    vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {})
    const start = vi.fn(async () => ({ mode: 'direct', url: '/api/stream/5', startSec: 0, durationMs: 60 * MIN, audioIndex: 0, audio: [], subtitles: [] }))
    render(<VideoPlayer itemId={9} title="Дюна" parts={[{ id: 5, name: 'dune.mkv', durationMs: 60 * MIN } as MediaFile]} startSec={0} onClose={() => {}} start={start as never} />)
    await waitFor(() => expect(urls.some((u) => u.endsWith('/api/files/5/trickplay'))).toBe(true))
    return urls
  }

  it('asks for the file’s previews and shows them on hover', async () => {
    await play('ready')
    layOut()
    await waitFor(() => {
      fireEvent.pointerMove(slider(), { clientX: 500, pointerType: 'mouse' })
      const frame = screen.getByTestId('scrub-preview').firstElementChild as HTMLElement
      expect(frame.style.backgroundImage).toBe('url("/api/files/5/trickplay/1.jpg?v=abc")')
    })
  })

  it('shows the time alone until they are made', async () => {
    await play('none')
    layOut()
    fireEvent.pointerMove(slider(), { clientX: 500, pointerType: 'mouse' })
    expect(screen.queryByTestId('scrub-preview')).toBeNull()
  })
})
