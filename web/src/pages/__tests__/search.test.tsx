import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import type { Card, Song } from '../../api/types'
import { audio } from '../../player/audio/engine'
import { session } from '../../state/session'
import { Search } from '../Search'

const user = (o: object) => ({ id: 2, name: 'Маша', avatar: 'grad-3', hasPassword: false, isAdmin: false, isKid: false, maxRating: null, ...o })
const json = (b: unknown, status = 200) => new Response(JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } })

const card = (id: number, kind: string, title: string, subtitle = ''): Card => ({
  id,
  kind,
  title,
  subtitle,
  year: 2020,
  poster: '',
  backdrop: '',
  logo: '',
  progress: 0,
  onServer: true,
})

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

function setup(u: object, found: object = EMPTY) {
  const f = vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input)
    if (url.endsWith('/api/login')) return json(u)
    if (url.includes('/api/search')) return json({ ...EMPTY, ...found })
    if (url.includes('/api/discover') && url.includes('kind=music'))
      return json([{ tmdbId: 0, extId: 'mb-1', kind: 'music', title: 'Группа крови', subtitle: 'Кино', year: 1988, poster: '', overview: '', inLibrary: false, libraryItemId: null }])
    if (url.includes('/api/discover'))
      return json([{ tmdbId: 1, kind: 'movie', title: 'Дюна', year: 2021, poster: '', overview: '', inLibrary: false, libraryItemId: null }])
    return json({})
  })
  vi.stubGlobal('fetch', f)
  return f
}

const renderSearch = (path = '/search') =>
  render(
    <MemoryRouter initialEntries={[path]}>
      <Search />
    </MemoryRouter>,
  )

describe('Поиск', () => {
  beforeEach(() => session.reset())
  afterEach(() => vi.unstubAllGlobals())

  it('kid does not see Найти и добавить', async () => {
    const f = setup(user({ isKid: true }))
    await session.login(2)
    renderSearch()
    await userEvent.type(screen.getByRole('searchbox'), 'Дюна')
    expect(await screen.findByText('Ничего не найдено в медиатеке', {}, { timeout: 3000 })).toBeInTheDocument()
    await new Promise((r) => setTimeout(r, 700)) // past the discover debounce
    expect(f.mock.calls.some(([u]) => String(u).includes('/api/discover'))).toBe(false)
    expect(screen.queryByText('Найти и добавить')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'На сервер' })).not.toBeInTheDocument()
  })

  it('a library-only server: no «can be added» hint, no catalog search', async () => {
    const f = setup(user({ libraryOnly: true }))
    await session.login(2)
    renderSearch()
    expect(screen.queryByText(/can be added|можно добавить/)).not.toBeInTheDocument()
    await userEvent.type(screen.getByRole('searchbox'), 'Дюна')
    expect(await screen.findByText('Ничего не найдено в медиатеке', {}, { timeout: 3000 })).toBeInTheDocument()
    await new Promise((r) => setTimeout(r, 700)) // past the discover debounce
    expect(f.mock.calls.some(([u]) => String(u).includes('/api/discover'))).toBe(false)
    expect(screen.queryByRole('button', { name: 'На сервер' })).not.toBeInTheDocument()
  })

  it('renders music and book sections from search result', async () => {
    setup(user({ isKid: true }), {
      artists: [card(5, 'artist', 'Кино')],
      albums: [card(10, 'album', 'Группа крови', 'Кино')],
      songs: [song(1, 'Группа крови'), song(2, 'Закрой за мной дверь')],
      books: [card(20, 'book', 'Мастер и Маргарита', 'Михаил Булгаков')],
      audiobooks: [card(21, 'audiobook', 'Пикник на обочине', 'Стругацкие')],
      comics: [card(22, 'comic', 'Берсерк')],
    })
    await session.login(2)
    renderSearch('/search?q=к')
    // the top row (the best match, the songs), then a rail per kind
    // (the one artist is the top result: not shown again as its own section)
    const order = ['Песни', 'Альбомы', 'Книги', 'Аудиокниги', 'Манга и комиксы']
    for (const name of order) expect(await screen.findByRole('region', { name }, { timeout: 3000 })).toBeInTheDocument()
    expect(screen.queryByRole('region', { name: 'Артисты' })).not.toBeInTheDocument()
    // empty sections are not shown
    expect(screen.queryByRole('region', { name: 'Фильмы и сериалы' })).not.toBeInTheDocument()
    // no exact title: the top result is the first hit
    expect(within(screen.getByRole('region', { name: 'Лучшее совпадение' })).getByText('Кино')).toBeInTheDocument()
    expect(screen.getByRole('region', { name: 'Лучшее совпадение' }).parentElement).toBe(screen.getByRole('region', { name: 'Песни' }).parentElement)
    const headings = screen.getAllByRole('heading', { level: 2 }).map((h) => h.textContent)
    expect(headings.filter((h) => order.includes(h ?? ''))).toEqual(order)
    expect(within(screen.getByRole('region', { name: 'Альбомы' })).getByRole('link', { name: /Группа крови/ })).toHaveAttribute('href', '/music/album/10')
    expect(within(screen.getByRole('region', { name: 'Книги' })).getByRole('link', { name: /Мастер и Маргарита/ })).toHaveAttribute('href', '/books/20')
    const songs = screen.getByRole('region', { name: 'Песни' })
    expect(within(songs).getAllByText('Кино · Группа крови')).toHaveLength(2)
    expect(within(songs).getAllByText('4:05')).toHaveLength(2)
    expect(within(songs).getAllByRole('button', { name: 'Ещё' })).toHaveLength(2)
  })

  it('clicking a song starts audio queue', async () => {
    const play = vi.spyOn(audio, 'play').mockImplementation(() => {})
    setup(user({}), { songs: [song(1, 'Группа крови'), song(2, 'Закрой за мной дверь')] })
    await session.login(2)
    renderSearch('/search?q=крови')
    fireEvent.click(await screen.findByRole('button', { name: /Закрой за мной дверь/ }, { timeout: 3000 }))
    expect(play).toHaveBeenCalledTimes(1)
    const [queue, start] = play.mock.calls[0]!
    expect(queue.map((q) => q.trackId)).toEqual([1, 2])
    expect(start).toBe(1)
  })

  it('best match picks exact album title over partial movie', async () => {
    setup(user({}), {
      library: [card(1, 'movie', 'Дюна: Часть вторая')],
      albums: [card(10, 'album', 'Дюна', 'Эдуард Артемьев')],
    })
    await session.login(2)
    renderSearch('/search?q=дюна')
    const best = await screen.findByRole('region', { name: 'Лучшее совпадение' }, { timeout: 3000 })
    expect(within(best).getByRole('link')).toHaveAttribute('href', '/music/album/10')
    expect(screen.getByRole('region', { name: 'Фильмы и сериалы' })).toBeInTheDocument()
  })
})
