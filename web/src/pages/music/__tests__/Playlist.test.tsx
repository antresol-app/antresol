import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router'
import type { PlaylistDetail, PlaylistTrack } from '../../../api/types'
import { resetAudioForTests } from '../../../player/audio/engine'
import { Playlist } from '../Playlist'
import { PlaylistCover } from '../PlaylistCover'

function entry(i: number, idx: number): PlaylistTrack {
  return {
    id: 100 + i,
    title: `Трек ${i}`,
    artist: 'Тестовый артист',
    artistId: 5,
    albumId: 1,
    album: 'Первый альбом',
    discNo: 1,
    trackNo: i,
    durationMs: 200_000,
    fileId: 200 + i,
    lossless: true,
    hasLyrics: false,
    cover: `/api/images/local/${i}/poster`,
    starred: false,
    idx,
  }
}

const detail: PlaylistDetail = {
  playlist: {
    id: 7,
    name: 'Вечер',
    trackCount: 3,
    durationMs: 600_000,
    cover: '/api/images/local/1/poster',
    covers: ['/api/images/local/1/poster', '/api/images/local/2/poster'],
    updatedAt: '2026-10-25T10:00:00Z',
  },
  // stored position 1 is hidden from this profile
  tracks: [entry(1, 0), entry(2, 2), entry(3, 3)],
}

let calls: { method: string; url: string; body?: unknown }[] = []
/** Holds PATCH answers until released (serialization tests). */
let gate: Promise<void> | null = null
let patchStatus = 200
/** The playlist GET answers with (default: detail). */
let served: PlaylistDetail | null = null
/** The import's pending songs (null: the request fails). */
let pendingRows: { position: number; artist: string; title: string; state: string }[] | null = null

/** The row of a song in the list. */
const row = (title: string) => screen.getAllByRole('listitem').find((li) => li.textContent?.includes(title))!

beforeEach(() => {
  resetAudioForTests()
  calls = []
  gate = null
  patchStatus = 200
  served = null
  pendingRows = null
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      const method = init?.method ?? 'GET'
      calls.push({ method, url, body: init?.body ? JSON.parse(String(init.body)) : undefined })
      if (url === '/api/music/playlists/7' && method === 'GET') return new Response(JSON.stringify(served ?? detail), { status: 200 })
      if (url === '/api/import/playlists/7/pending' && pendingRows) return new Response(JSON.stringify({ pending: pendingRows }), { status: 200 })
      if (url === '/api/music/playlists/7' && method === 'PATCH') {
        if (gate) await gate
        if (patchStatus !== 200) return new Response(JSON.stringify({ error: 'Неверная позиция в плейлисте' }), { status: patchStatus })
        return new Response(JSON.stringify(detail.playlist), { status: 200 })
      }
      if (url === '/api/music/playlists/7' && method === 'DELETE') return new Response(null, { status: 204 })
      return new Response('{}', { status: 404 })
    }),
  )
})
afterEach(() => {
  vi.unstubAllGlobals()
  resetAudioForTests()
})

function renderPlaylist() {
  return render(
    <MemoryRouter initialEntries={['/music/playlist/7']}>
      <Routes>
        <Route path="/music/playlist/:id" element={<Playlist />} />
        <Route path="/music/playlists" element={<p>Все плейлисты</p>} />
      </Routes>
    </MemoryRouter>,
  )
}

describe('Playlist', () => {
  it('remove sends PATCH remove [idx]', async () => {
    renderPlaylist()
    await screen.findByRole('heading', { level: 1, name: 'Вечер' })
    fireEvent.click(within(row('Трек 2')).getByRole('button', { name: 'Ещё' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Убрать из плейлиста' }))
    await waitFor(() => expect(calls.some((c) => c.method === 'PATCH')).toBe(true))
    expect(calls.find((c) => c.method === 'PATCH')).toEqual({ method: 'PATCH', url: '/api/music/playlists/7', body: { remove: [2] } })
  })

  it('moving a track down sends the stored positions in order', async () => {
    renderPlaylist()
    await screen.findByRole('heading', { level: 1, name: 'Вечер' })
    fireEvent.click(within(row('Трек 1')).getByRole('button', { name: 'Ещё' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Переместить ниже' }))
    await waitFor(() => expect(calls.some((c) => c.method === 'PATCH')).toBe(true))
    expect(calls.find((c) => c.method === 'PATCH')!.body).toEqual({ order: [2, 0, 3] })
  })

  it('edits go one at a time with renumbered positions', async () => {
    let release!: () => void
    gate = new Promise((r) => (release = r))
    renderPlaylist()
    await screen.findByRole('heading', { level: 1, name: 'Вечер' })
    fireEvent.click(within(row('Трек 1')).getByRole('button', { name: 'Ещё' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Убрать из плейлиста' }))
    await waitFor(() => expect(calls.filter((c) => c.method === 'PATCH')).toHaveLength(1))
    // Трек 3 was stored at 3; after removing 0 it is at 2
    fireEvent.click(within(row('Трек 3')).getByRole('button', { name: 'Ещё' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Убрать из плейлиста' }))
    await new Promise((r) => setTimeout(r, 20))
    expect(calls.filter((c) => c.method === 'PATCH')).toHaveLength(1) // the second waits for the first
    release()
    await waitFor(() => expect(calls.filter((c) => c.method === 'PATCH')).toHaveLength(2))
    expect(calls.filter((c) => c.method === 'PATCH').map((c) => c.body)).toEqual([{ remove: [0] }, { remove: [2] }])
  })

  it('a failed edit shows a message and restores the server list', async () => {
    patchStatus = 400
    renderPlaylist()
    await screen.findByRole('heading', { level: 1, name: 'Вечер' })
    fireEvent.click(within(row('Трек 2')).getByRole('button', { name: 'Ещё' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Убрать из плейлиста' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Неверная позиция в плейлисте. Показан плейлист с сервера.')
    await waitFor(() => expect(row('Трек 2')).toBeTruthy())
  })

  it('delete asks for confirmation', async () => {
    renderPlaylist()
    await screen.findByRole('heading', { level: 1, name: 'Вечер' })
    fireEvent.click(screen.getByRole('button', { name: 'Удалить плейлист' }))
    const sheet = await screen.findByRole('alertdialog', { name: 'Удалить плейлист «Вечер»?' })
    expect(calls.some((c) => c.method === 'DELETE')).toBe(false)
    fireEvent.click(within(sheet).getByRole('button', { name: 'Отменить' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
    expect(calls.some((c) => c.method === 'DELETE')).toBe(false)
    fireEvent.click(screen.getByRole('button', { name: 'Удалить плейлист' }))
    fireEvent.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Удалить' }))
    await waitFor(() => expect(calls.some((c) => c.method === 'DELETE' && c.url === '/api/music/playlists/7')).toBe(true))
    expect(await screen.findByText('Все плейлисты')).toBeInTheDocument()
  })

  it('mosaic shows up to four covers', () => {
    const covers = [1, 2, 3, 4, 5].map((i) => `/api/images/local/${i}/poster`)
    const { container, rerender } = render(<PlaylistCover covers={covers} name="Вечер" />)
    expect(container.querySelectorAll('img')).toHaveLength(4)
    expect(container.firstElementChild).toHaveClass('mosaic')
    rerender(<PlaylistCover covers={covers.slice(0, 1)} name="Вечер" />)
    expect(container.querySelectorAll('img')).toHaveLength(1)
    expect(container.firstElementChild).not.toHaveClass('mosaic')
    rerender(<PlaylistCover covers={[]} name="Вечер" />)
    expect(container.querySelectorAll('img')).toHaveLength(0)
  })

  it('no pending rows when the request fails', async () => {
    served = { ...detail, tracks: [entry(1, 0), entry(2, 1)] }
    renderPlaylist()
    await screen.findByRole('heading', { level: 1, name: 'Вечер' })
    await waitFor(() => expect(calls.some((c) => c.url === '/api/import/playlists/7/pending')).toBe(true))
    expect(within(screen.getByRole('list', { name: 'Песни плейлиста «Вечер»' })).getAllByRole('listitem')).toHaveLength(2)
    expect(screen.queryByText('Ещё не скачано')).toBeNull()
  })

  it('no pending rows when the import has none', async () => {
    served = { ...detail, tracks: [entry(1, 0), entry(2, 1)] }
    pendingRows = []
    renderPlaylist()
    await screen.findByRole('heading', { level: 1, name: 'Вечер' })
    await waitFor(() => expect(calls.some((c) => c.url === '/api/import/playlists/7/pending')).toBe(true))
    expect(within(screen.getByRole('list', { name: 'Песни плейлиста «Вечер»' })).getAllByRole('listitem')).toHaveLength(2)
  })
})
