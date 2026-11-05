import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import type { Playlist, Song } from '../../../api/types'
import { audio, resetAudioForTests } from '../../../player/audio/engine'
import { SongMenu } from '../SongMenu'

const song: Song = {
  id: 101,
  title: 'Трек 1',
  artist: 'Тестовый артист',
  artistId: 5,
  albumId: 1,
  album: 'Первый альбом',
  discNo: 1,
  trackNo: 1,
  durationMs: 200_000,
  fileId: 301,
  lossless: true,
  hasLyrics: false,
  cover: '/api/images/local/1/poster',
  starred: false,
}

const lists: Playlist[] = [
  { id: 7, name: 'Вечер', trackCount: 2, durationMs: 1, cover: '', covers: [], updatedAt: '2026-10-25T10:00:00Z' },
]

let calls: { method: string; url: string; body?: unknown }[] = []
let starFails = false

beforeEach(() => {
  resetAudioForTests()
  calls = []
  starFails = false
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      const method = init?.method ?? 'GET'
      calls.push({ method, url, body: init?.body ? JSON.parse(String(init.body)) : undefined })
      if (url === '/api/music/playlists' && method === 'GET') return new Response(JSON.stringify({ items: lists }), { status: 200 })
      if (url === '/api/music/playlists' && method === 'POST')
        return new Response(JSON.stringify({ ...lists[0], id: 8, name: 'Новый' }), { status: 200 })
      if (url === '/api/music/playlists/7') return new Response(JSON.stringify(lists[0]), { status: 200 })
      if (url.startsWith('/api/music/star/') && starFails)
        return new Response(JSON.stringify({ error: 'В избранное добавляются только песни, альбомы и артисты' }), { status: 400 })
      return new Response(null, { status: 204 })
    }),
  )
})
afterEach(() => {
  vi.unstubAllGlobals()
  resetAudioForTests()
})

const renderMenu = () =>
  render(
    <MemoryRouter>
      <SongMenu song={song} />
    </MemoryRouter>,
  )

describe('SongMenu', () => {
  it('a failed star is reverted with a message', async () => {
    starFails = true
    renderMenu()
    fireEvent.click(screen.getByRole('button', { name: 'Ещё' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Нравится' }))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('В избранное добавляются только песни, альбомы и артисты')
    // the design system's Toast in its error tone
    expect(alert).toHaveAttribute('data-testid', 'toast-region')
    fireEvent.click(screen.getByRole('button', { name: 'Ещё' }))
    expect(await screen.findByRole('menuitem', { name: 'Нравится' })).toBeInTheDocument()
  })

  it('a CUE slice offers the album file instead', async () => {
    render(
      <MemoryRouter>
        <SongMenu song={{ ...song, cue: true }} />
      </MemoryRouter>,
    )
    fireEvent.click(screen.getByRole('button', { name: 'Ещё' }))
    expect(await screen.findByRole('menuitem', { name: /Скачать файл альбома/ })).toHaveAttribute('href', '/api/download/301')
    expect(screen.queryByRole('menuitem', { name: /Скачать песню/ })).not.toBeInTheDocument()
  })

  it('add to playlist opens sheet with Новый плейлист', async () => {
    renderMenu()
    fireEvent.click(screen.getByRole('button', { name: 'Ещё' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Добавить в плейлист…' }))
    const sheet = await screen.findByRole('dialog', { name: 'Добавить в плейлист' })
    expect(await within(sheet).findByRole('button', { name: /Вечер/ })).toBeInTheDocument()
    fireEvent.click(within(sheet).getByRole('button', { name: 'Новый плейлист…' }))
    fireEvent.change(within(sheet).getByLabelText('Название'), { target: { value: 'Новый' } })
    fireEvent.click(within(sheet).getByRole('button', { name: 'Создать' }))
    await waitFor(() => expect(calls.some((c) => c.method === 'POST')).toBe(true))
    expect(calls.find((c) => c.method === 'POST')!.body).toEqual({ name: 'Новый', trackIds: [101] })
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })

  it('adds to an existing playlist with append', async () => {
    renderMenu()
    fireEvent.click(screen.getByRole('button', { name: 'Ещё' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Добавить в плейлист…' }))
    fireEvent.click(await screen.findByRole('button', { name: /Вечер/ }))
    await waitFor(() => expect(calls.some((c) => c.method === 'PATCH')).toBe(true))
    expect(calls.find((c) => c.method === 'PATCH')).toMatchObject({ url: '/api/music/playlists/7', body: { append: [101] } })
  })

  it('says where the song went and tells the page (the open playlist reloads)', async () => {
    const onAdded = vi.fn()
    render(
      <MemoryRouter>
        <SongMenu song={song} onAdded={onAdded} />
      </MemoryRouter>,
    )
    fireEvent.click(screen.getByRole('button', { name: 'Ещё' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Добавить в плейлист…' }))
    fireEvent.click(await screen.findByRole('button', { name: /Вечер/ }))
    const status = await screen.findByRole('status')
    expect(status).toHaveTextContent('Добавлено в «Вечер»')
    expect(status).toHaveAttribute('data-testid', 'toast-region')
    expect(onAdded).toHaveBeenCalledWith(expect.objectContaining({ id: 7 }))
  })

  it('Скачать песню links to download url', async () => {
    renderMenu()
    fireEvent.click(screen.getByRole('button', { name: 'Ещё' }))
    const link = await screen.findByRole('menuitem', { name: /Скачать песню/ })
    expect(link).toHaveAttribute('href', '/api/download/301')
    expect(link).toHaveAttribute('download')
  })

  it('queues next and stars', async () => {
    const playNext = vi.spyOn(audio, 'playNext').mockImplementation(() => {})
    renderMenu()
    fireEvent.click(screen.getByRole('button', { name: 'Ещё' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Воспроизвести следующим' }))
    expect(playNext.mock.calls[0]![0][0]).toMatchObject({ trackId: 101 })
    fireEvent.click(screen.getByRole('button', { name: 'Ещё' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Нравится' }))
    await waitFor(() => expect(calls.some((c) => c.method === 'POST' && c.url === '/api/music/star/101')).toBe(true))
    fireEvent.click(screen.getByRole('button', { name: 'Ещё' }))
    expect(await screen.findByRole('menuitem', { name: 'Больше не нравится' })).toBeInTheDocument()
  })
})
