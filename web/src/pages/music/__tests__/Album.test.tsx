import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, within } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router'
import type { AlbumDetail, Song } from '../../../api/types'
import { audio, resetAudioForTests } from '../../../player/audio/engine'
import { Album } from '../Album'
import { session } from '../../../state/session'
import { setViewport } from '../../../test/setup'

function song(i: number, disc: number, no: number): Song {
  return {
    id: 100 + i,
    title: `Трек ${i}`,
    artist: 'Тестовый артист',
    artistId: 5,
    albumId: 1,
    album: 'Первый альбом',
    discNo: disc,
    trackNo: no,
    durationMs: 200_000,
    fileId: 200 + i,
    lossless: true,
    hasLyrics: false,
    cover: '/api/images/local/1/poster',
    starred: false,
  }
}

const detail: AlbumDetail = {
  card: {
    id: 1,
    kind: 'album',
    title: 'Первый альбом',
    subtitle: 'Тестовый артист',
    year: 1988,
    poster: '/api/images/local/1/poster',
    backdrop: '',
    logo: '',
    progress: 0,
    onServer: true,
  },
  artist: { id: 5, name: 'Тестовый артист' },
  year: 1988,
  genres: ['Рок'],
  durationMs: 800_000,
  trackCount: 4,
  quality: 'Lossless · FLAC',
  tracks: [song(1, 1, 1), song(2, 1, 2), song(3, 2, 1), song(4, 2, 2)],
  starred: false,
}

beforeEach(() => {
  resetAudioForTests()
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (url === '/api/music/albums/1') return new Response(JSON.stringify(detail), { status: 200 })
      return new Response('{}', { status: 404 })
    }),
  )
})
afterEach(() => {
  vi.unstubAllGlobals()
  resetAudioForTests()
})

function renderAlbum() {
  return render(
    <MemoryRouter initialEntries={['/music/album/1']}>
      <Routes>
        <Route path="/music/album/:id" element={<Album />} />
      </Routes>
    </MemoryRouter>,
  )
}

describe('Album', () => {
  it('renders disc headers for two discs', async () => {
    renderAlbum()
    expect(await screen.findByRole('heading', { level: 1, name: 'Первый альбом' })).toBeInTheDocument()
    expect(screen.getByText('Диск 1')).toBeInTheDocument()
    expect(screen.getByText('Диск 2')).toBeInTheDocument()
    expect(screen.getByText('Рок · 1988 · Lossless')).toBeInTheDocument()
    expect(screen.getByText('4 песни, 13 мин')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Тестовый артист' })).toHaveAttribute('href', '/music/artist/5')
  })

  it('clicking a track starts the queue at its index', async () => {
    const play = vi.spyOn(audio, 'play').mockImplementation(() => {})
    renderAlbum()
    fireEvent.click(await screen.findByRole('button', { name: /Трек 3/ }))
    expect(play).toHaveBeenCalledTimes(1)
    const [queue, start] = play.mock.calls[0]!
    expect(queue.map((q) => q.title)).toEqual(['Трек 1', 'Трек 2', 'Трек 3', 'Трек 4'])
    expect(start).toBe(2)
    expect(queue[2]!).toMatchObject({ trackId: 103, albumId: 1, durationSec: 200 })
    expect(queue[2]!.src).toMatch(/^\/api\/tracks\/103\/stream\?accept=/)
  })

  it('on a phone «Слушать» and «Перемешать» are two capsules; the rest, «Удалить…» last, is in «Ещё»', async () => {
    setViewport(390)
    const admin = { id: 1, name: 'Саша', avatar: 'grad-1', hasPassword: true, isAdmin: true, isKid: false, maxRating: null }
    const base = globalThis.fetch as unknown as (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) =>
        String(input).endsWith('/api/login') ? new Response(JSON.stringify(admin), { status: 200 }) : base(input, init),
      ),
    )
    await session.login(admin.id)
    try {
      renderAlbum()
      expect(await screen.findByRole('button', { name: 'Перемешать' })).toHaveTextContent('Перемешать')
      expect(screen.getByRole('button', { name: 'Слушать' })).toHaveTextContent('Слушать')
      expect(screen.queryByRole('button', { name: 'Удалить' })).toBeNull()
      fireEvent.click(screen.getByRole('button', { name: 'Действия: Первый альбом' }))
      const items = within(screen.getByRole('menu')).getAllByRole('menuitem')
      expect(items[items.length - 1]).toHaveTextContent('Удалить…')
    } finally {
      session.reset()
      setViewport(1440)
    }
  })

  it('on a wide screen the album keeps its round delete button', async () => {
    renderAlbum()
    await screen.findByRole('button', { name: 'Перемешать' })
    expect(screen.queryByRole('button', { name: 'Действия: Первый альбом' })).toBeNull()
  })
})
