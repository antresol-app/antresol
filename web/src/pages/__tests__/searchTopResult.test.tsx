// «Лучшее совпадение» as a compact card with one primary action, the songs
// beside it (five, then «Показать все») and the library's loading state.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router'
import type { Card, Song } from '../../api/types'
import { audio } from '../../player/audio/engine'
import { session } from '../../state/session'
import { Search } from '../Search'

const json = (b: unknown) => new Response(JSON.stringify(b), { status: 200, headers: { 'Content-Type': 'application/json' } })
const card = (id: number, kind: string, title: string, subtitle = ''): Card => ({ id, kind, title, subtitle, poster: '', backdrop: '', logo: '', progress: 0, onServer: true })
const song = (id: number, title: string): Song => ({
  id,
  title,
  artist: 'Кино',
  artistId: 5,
  albumId: 10,
  album: 'Группа крови',
  discNo: 1,
  trackNo: id,
  durationMs: 245_000,
  fileId: 100 + id,
  lossless: true,
  hasLyrics: false,
  cover: '',
  starred: false,
})
const EMPTY = { library: [], artists: [], albums: [], songs: [], books: [], audiobooks: [], comics: [] }
// a kid: no «Найти и добавить» under the results (it is not what these tests are about)
const KID = { id: 2, name: 'М', avatar: 'grad-3', hasPassword: false, isAdmin: false, isKid: true, maxRating: null }

function setup(found: object, extra: (url: string) => Response | Promise<Response> | undefined = () => undefined) {
  const f = vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input)
    if (url.endsWith('/api/login')) return json(KID)
    const r = extra(url)
    if (r) return r
    if (url.includes('/api/search')) return json({ ...EMPTY, ...found })
    return json({})
  })
  vi.stubGlobal('fetch', f)
  return f
}

// the item pages are stand-ins: a test sees where «Слушать» or a link went
const renderAt = (path: string) =>
  render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/search" element={<Search />} />
        <Route path="/music/album/:id" element={<p>страница альбома</p>} />
        <Route path="/music/artist/:id" element={<p>страница артиста</p>} />
      </Routes>
    </MemoryRouter>,
  )

const best = () => screen.findByRole('region', { name: 'Лучшее совпадение' }, { timeout: 3000 })

describe('«Лучшее совпадение»', () => {
  beforeEach(() => session.reset())
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it('an album plays from its first song', async () => {
    const play = vi.spyOn(audio, 'play').mockImplementation(() => {})
    const f = setup({ albums: [card(10, 'album', 'Группа крови', 'Кино')], songs: [song(1, 'Группа крови')] }, (url) =>
      url.includes('/api/music/albums/10') ? json({ card: card(10, 'album', 'Группа крови'), tracks: [song(1, 'Группа крови'), song(2, 'Закрой за мной дверь')] }) : undefined,
    )
    await session.login(2)
    renderAt('/search?q=группа крови')
    const region = await best()
    // the name opens the album; the one action plays it
    expect(within(region).getByRole('link', { name: /Группа крови/ })).toHaveAttribute('href', '/music/album/10')
    // nothing is fetched for it until it is pressed
    expect(f.mock.calls.some(([u]) => String(u).includes('/api/music/albums/10'))).toBe(false)
    await userEvent.click(within(region).getByRole('button', { name: 'Слушать' }))
    await waitFor(() => expect(play).toHaveBeenCalledTimes(1))
    expect(f.mock.calls.some(([u]) => String(u).includes('/api/music/albums/10'))).toBe(true)
    const [queue, start] = play.mock.calls[0]!
    expect(queue.map((q) => q.trackId)).toEqual([1, 2])
    expect(start).toBe(0)
  })

  it('an artist is round and plays its top songs', async () => {
    const play = vi.spyOn(audio, 'play').mockImplementation(() => {})
    setup({ artists: [card(5, 'artist', 'Кино')] }, (url) =>
      url.includes('/api/music/artists/5') ? json({ card: card(5, 'artist', 'Кино'), albums: [], topTracks: [song(3, 'Кукушка'), song(4, 'Звезда')], appearsOn: [] }) : undefined,
    )
    await session.login(2)
    renderAt('/search?q=кино')
    const region = await best()
    expect(region.querySelector('[data-shape="round"]')).not.toBeNull()
    expect(within(region).getByText('Артист')).toBeInTheDocument()
    await userEvent.click(within(region).getByRole('button', { name: 'Слушать' }))
    await waitFor(() => expect(play).toHaveBeenCalledTimes(1))
    expect(play.mock.calls[0]![0].map((q) => q.trackId)).toEqual([3, 4])
  })

  it('«Слушать» opens the page instead when there is nothing to play or the server fails', async () => {
    const play = vi.spyOn(audio, 'play').mockImplementation(() => {})
    setup({ artists: [card(5, 'artist', 'Кино')] }, (url) =>
      url.includes('/api/music/artists/5') ? json({ card: card(5, 'artist', 'Кино'), albums: [], topTracks: [], appearsOn: [] }) : undefined,
    )
    await session.login(2)
    const { unmount } = renderAt('/search?q=кино')
    await userEvent.click(within(await best()).getByRole('button', { name: 'Слушать' }))
    expect(await screen.findByText('страница артиста')).toBeInTheDocument()
    unmount()
    setup({ albums: [card(10, 'album', 'Группа крови', 'Кино')] }, (url) =>
      url.includes('/api/music/albums/10') ? new Response('{}', { status: 500, headers: { 'Content-Type': 'application/json' } }) : undefined,
    )
    renderAt('/search?q=группа крови')
    await userEvent.click(within(await best()).getByRole('button', { name: 'Слушать' }))
    expect(await screen.findByText('страница альбома')).toBeInTheDocument()
    expect(play).not.toHaveBeenCalled()
  })

  it('a film is watched from the card; its art keeps the poster shape', async () => {
    setup({ library: [card(41, 'movie', 'Дюна')] })
    await session.login(2)
    renderAt('/search?q=дюна')
    const region = await best()
    expect(within(region).getByRole('link', { name: 'Смотреть' })).toHaveAttribute('href', '/watch/41')
    expect(region.querySelector('[data-shape="poster"]')).not.toBeNull()
  })

  it('a series is watched from the card too (the player picks the episode)', async () => {
    setup({ library: [card(43, 'series', 'Дюна: Пророчество')] })
    await session.login(2)
    renderAt('/search?q=дюна: пророчество')
    expect(within(await best()).getByRole('link', { name: 'Смотреть' })).toHaveAttribute('href', '/watch/43')
  })

  it('a title still downloading, or not on the server, is opened, not played', async () => {
    setup({ library: [{ ...card(44, 'movie', 'Дюна'), pending: { key: 'k', status: 'active', done: 1, size: 10 } }] })
    await session.login(2)
    const { unmount } = renderAt('/search?q=дюна')
    let region = await best()
    expect(within(region).queryByRole('link', { name: 'Смотреть' })).not.toBeInTheDocument()
    expect(within(region).getByRole('link', { name: 'Открыть' })).toHaveAttribute('href', '/item/44')
    unmount()
    setup({ library: [{ ...card(45, 'series', 'Дюна'), onServer: false }] })
    renderAt('/search?q=дюна')
    region = await best()
    expect(within(region).queryByRole('link', { name: 'Смотреть' })).not.toBeInTheDocument()
    expect(within(region).getByRole('link', { name: 'Открыть' })).toBeInTheDocument()
  })

  it('the top result is not repeated in the section of its kind below', async () => {
    setup({ library: [card(41, 'movie', 'Дюна'), card(42, 'movie', 'Дюна: Часть вторая'), card(43, 'series', 'Дюна: Пророчество'), card(44, 'movie', 'Дюна 1984')] })
    await session.login(2)
    renderAt('/search?q=дюна')
    await best()
    const section = screen.getByRole('region', { name: 'Фильмы и сериалы' })
    expect(within(section).queryByRole('link', { name: /^Дюна$|^Дюна\b(?!:| 1984)/ })).not.toBeInTheDocument()
    expect(section.querySelector('a[href="/item/41"]')).toBeNull()
    expect(section.querySelector('a[href="/item/42"]')).not.toBeNull()
  })

  it('a book opens its page', async () => {
    setup({ books: [card(20, 'book', 'Мастер и Маргарита', 'Михаил Булгаков')] })
    await session.login(2)
    renderAt('/search?q=мастер и маргарита')
    const region = await best()
    expect(within(region).getByRole('link', { name: 'Открыть' })).toHaveAttribute('href', '/books/20')
  })

  it('shows five songs beside it, then all of them on «Показать все»', async () => {
    const songs = Array.from({ length: 8 }, (_, i) => song(i + 1, `Песня ${i + 1}`))
    setup({ artists: [card(5, 'artist', 'Кино')], songs })
    await session.login(2)
    renderAt('/search?q=кино')
    const list = await screen.findByRole('region', { name: 'Песни' }, { timeout: 3000 })
    expect(within(list).getAllByRole('button', { name: /Песня \d/ })).toHaveLength(5)
    await userEvent.click(within(list).getByRole('button', { name: 'Показать все' }))
    expect(within(list).getAllByRole('button', { name: /Песня \d/ })).toHaveLength(8)
    await userEvent.click(within(list).getByRole('button', { name: 'Свернуть' }))
    expect(within(list).getAllByRole('button', { name: /Песня \d/ })).toHaveLength(5)
  })

  it('five songs or fewer: no «Показать все»', async () => {
    setup({ artists: [card(5, 'artist', 'Кино')], songs: [song(1, 'Песня 1'), song(2, 'Песня 2')] })
    await session.login(2)
    renderAt('/search?q=кино')
    const list = await screen.findByRole('region', { name: 'Песни' }, { timeout: 3000 })
    expect(within(list).queryByRole('button', { name: 'Показать все' })).not.toBeInTheDocument()
  })

  it('says it is searching while the library answers, in words a screen reader reads, and stops when it has', async () => {
    let answer: (r: Response) => void = () => {}
    setup({}, (url) => (url.includes('/api/search') ? new Promise<Response>((r) => (answer = r)) : undefined))
    await session.login(2)
    renderAt('/search?q=дюна')
    const busy = await screen.findByRole('status', { busy: true }, { timeout: 3000 })
    // the live region's text, not an aria-label many readers skip
    expect(busy).toHaveTextContent('Ищем в медиатеке')
    expect(busy).not.toHaveAttribute('aria-label')
    answer(json({ ...EMPTY, library: [card(41, 'movie', 'Дюна')] }))
    await best()
    expect(screen.queryByRole('status', { busy: true })).not.toBeInTheDocument()
  })

  it('a new query drops the old card: its pending «Слушать» neither plays nor navigates', async () => {
    const play = vi.spyOn(audio, 'play').mockImplementation(() => {})
    let answerAlbum: (r: Response) => void = () => {}
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input)
        if (url.endsWith('/api/login')) return json(KID)
        if (url.includes('/api/music/albums/10')) return new Promise<Response>((r) => (answerAlbum = r))
        if (url.includes('/api/search') && url.includes(encodeURIComponent('кино'))) return json({ ...EMPTY, artists: [card(5, 'artist', 'Кино')] })
        if (url.includes('/api/search')) return json({ ...EMPTY, albums: [card(10, 'album', 'Группа крови', 'Кино')] })
        return json({})
      }),
    )
    await session.login(2)
    renderAt('/search?q=группа крови')
    await userEvent.click(within(await best()).getByRole('button', { name: 'Слушать' }))
    const box = screen.getByRole('searchbox')
    await userEvent.clear(box)
    await userEvent.type(box, 'кино')
    await waitFor(() => expect(within(screen.getByRole('region', { name: 'Лучшее совпадение' })).getByRole('link', { name: 'Кино' })).toBeInTheDocument(), { timeout: 3000 })
    // the new card is not busy with the old one's request
    expect(within(screen.getByRole('region', { name: 'Лучшее совпадение' })).getByRole('button', { name: 'Слушать' })).toBeEnabled()
    answerAlbum(json({ card: card(10, 'album', 'Группа крови'), tracks: [song(1, 'Группа крови')] }))
    await new Promise((r) => setTimeout(r, 50))
    expect(play).not.toHaveBeenCalled()
    expect(screen.queryByText('страница альбома')).not.toBeInTheDocument()
  })
})
