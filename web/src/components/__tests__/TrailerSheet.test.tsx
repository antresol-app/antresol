// «Трейлер» plays inside the app: from the server when it lists `play`
// (a spinner while it is prepared, a busy server asked again, an error with
// «Повторить»), else in the official embedded player.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ItemDetail, Trailer } from '../../api/types'
import { ActionBar } from '../ActionBar'
import { TrailerSheet } from '../TrailerSheet'

const trailer = (over: Partial<Trailer> = {}): Trailer => ({
  name: 'Официальный трейлер', site: 'YouTube', key: 'trailer0001', type: 'Trailer', lang: 'ru', official: true,
  url: 'https://www.youtube.com/watch?v=trailer0001', play: '/api/trailers/trailer0001/play', ...over,
})

const json = (status: number, body: unknown, headers: Record<string, string> = {}) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json', ...headers } })

afterEach(() => {
  vi.unstubAllGlobals()
  vi.useRealTimers()
})

describe('TrailerSheet', () => {
  it('shows the spinner, asks a busy server again, then plays the server’s file', async () => {
    const fetch = vi.fn()
      .mockResolvedValueOnce(json(503, { error: 'busy' }, { 'Retry-After': '1' }))
      .mockResolvedValueOnce(json(200, { url: '/api/trailers/trailer0001.mp4?mk=k', durationMs: 120000 }))
    vi.stubGlobal('fetch', fetch)
    const { container } = render(<TrailerSheet trailer={trailer()} onClose={() => {}} />)
    expect(screen.getByRole('dialog', { name: 'Официальный трейлер' })).toBeInTheDocument()
    expect(screen.getByText('Готовим трейлер…')).toBeInTheDocument()
    await waitFor(() => expect(container.ownerDocument.querySelector('video')).not.toBeNull(), { timeout: 3000 })
    const video = container.ownerDocument.querySelector('video')!
    expect(video).toHaveAttribute('src', '/api/trailers/trailer0001.mp4?mk=k')
    expect(video).toHaveAttribute('controls')
    expect(fetch).toHaveBeenCalledTimes(2)
    expect(fetch.mock.calls[0]![0]).toBe('/api/trailers/trailer0001/play')
    expect(fetch.mock.calls[0]![1]).toMatchObject({ method: 'POST' })
  })

  it('a failure shows the server’s words and «Повторить» asks again', async () => {
    const fetch = vi.fn()
      .mockResolvedValueOnce(json(502, { error: 'Не удалось загрузить трейлер' }))
      .mockResolvedValueOnce(json(200, { url: '/api/trailers/trailer0001.mp4', durationMs: 1 }))
    vi.stubGlobal('fetch', fetch)
    render(<TrailerSheet trailer={trailer()} onClose={() => {}} />)
    expect(await screen.findByRole('alert')).toHaveTextContent('Не удалось загрузить трейлер')
    await userEvent.click(screen.getByRole('button', { name: 'Повторить' }))
    await waitFor(() => expect(document.querySelector('video')).not.toBeNull())
  })

  it('without play: the official embedded player, no request to the server', () => {
    const fetch = vi.fn()
    vi.stubGlobal('fetch', fetch)
    render(<TrailerSheet trailer={trailer({ play: undefined })} onClose={() => {}} />)
    const frame = document.querySelector('iframe')!
    expect(frame).toHaveAttribute('src', 'https://www.youtube-nocookie.com/embed/trailer0001?autoplay=1&playsinline=1&rel=0')
    expect(fetch).not.toHaveBeenCalled()
  })
})

describe('ActionBar «Трейлер»', () => {
  it('opens the trailer inside the app, not in a new tab', async () => {
    const open = vi.spyOn(window, 'open').mockImplementation(() => null)
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(json(200, { url: '/api/trailers/trailer0001.mp4', durationMs: 1 })))
    const item = {
      id: 9, kind: 'movie', title: 'Дюна', subtitle: '', poster: '', backdrop: '', logo: '', progress: 0, onServer: true,
      overview: '', genres: [], cast: [], seasons: [], files: [{ id: 5, name: 'dune.mkv', size: 1, audio: [], subs: [] }], inWatchlist: false,
      actions: { play: true, toServer: false, toDevice: true, watchNow: false },
    } as unknown as ItemDetail
    render(<ActionBar item={item} onPlay={() => {}} trailers={[trailer()]} />)
    await userEvent.click(screen.getByRole('button', { name: 'Ещё' }))
    await userEvent.click(screen.getByRole('menuitem', { name: /Трейлер/ }))
    expect(await screen.findByRole('dialog', { name: 'Официальный трейлер' })).toBeInTheDocument()
    expect(open).not.toHaveBeenCalled()
    open.mockRestore()
  })
})
