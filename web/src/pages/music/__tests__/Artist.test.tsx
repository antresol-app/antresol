import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router'
import type { ArtistDetail } from '../../../api/types'
import { audio, resetAudioForTests } from '../../../player/audio/engine'
import { Artist } from '../Artist'
import { json, song } from './fixtures'

const card = (id: number, title: string) => ({
  id,
  kind: 'album' as const,
  title,
  subtitle: 'Тестовый артист',
  poster: '',
  backdrop: '',
  logo: '',
  progress: 0,
  onServer: true,
})

let detail: ArtistDetail
let urls: string[]
const catalogue = [song(1), song(2), song(3), song(4, { album: 'Второй альбом', albumId: 2 })]

beforeEach(() => {
  resetAudioForTests()
  urls = []
  detail = {
    card: { ...card(8, 'Тестовый артист'), kind: 'artist' as const, subtitle: '' },
    albums: [card(1, 'Первый альбом'), card(2, 'Второй альбом')],
    topTracks: [song(1), song(2)],
    appearsOn: [],
  }
  vi.spyOn(HTMLMediaElement.prototype, 'play').mockImplementation(() => Promise.resolve())
  vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {})
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input)
      urls.push(url)
      if (url === '/api/music/artists/8') return json(detail)
      if (url.startsWith('/api/music/shuffle')) return json({ tracks: catalogue })
      return json({}, 404)
    }),
  )
})
afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
  resetAudioForTests()
})

function renderArtist() {
  return render(
    <MemoryRouter initialEntries={['/music/artist/8']}>
      <Routes>
        <Route path="/music/artist/:id" element={<Artist />} />
      </Routes>
    </MemoryRouter>,
  )
}

describe('Artist', () => {
  it('«Перемешать» shuffles the whole catalogue from the server', async () => {
    renderArtist()
    fireEvent.click(await screen.findByRole('button', { name: 'Перемешать' }))
    await waitFor(() => expect(audio.get().queue).toHaveLength(4))
    expect(urls).toContain('/api/music/shuffle?source=artist&artist=8')
    expect(new Set(audio.get().queue.map((q) => q.trackId))).toEqual(new Set(catalogue.map((t) => t.id)))
    expect(audio.get().shuffle).toBe(true)
  })

  it('«Слушать» plays the top songs without a request', async () => {
    renderArtist()
    fireEvent.click(await screen.findByRole('button', { name: 'Слушать' }))
    expect(audio.get().queue.map((q) => q.trackId)).toEqual([101, 102])
    expect(urls.some((u) => u.startsWith('/api/music/shuffle'))).toBe(false)
  })

  it('«Перемешать» is enabled with albums but no top songs', async () => {
    detail.topTracks = []
    renderArtist()
    expect(await screen.findByRole('button', { name: 'Перемешать' })).toBeEnabled()
    expect(screen.getByRole('button', { name: 'Слушать' })).toBeDisabled()
  })

  it('«Перемешать» is disabled with nothing to play', async () => {
    detail.topTracks = []
    detail.albums = []
    renderArtist()
    expect(await screen.findByRole('button', { name: 'Перемешать' })).toBeDisabled()
  })
})
