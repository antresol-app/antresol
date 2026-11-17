import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, fireEvent, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import type { Card, Song } from '../../api/types'
import { registerAddon, resetAddonsForTests, setAddonUser } from '../../addons/registry'
import type { SearchScope } from '../../app/sections'
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
    if (url.includes('/api/requests/search'))
      return json([{ kind: 'movie', tmdbId: 1, extId: '', title: 'Дюна', subtitle: '', year: 2021, poster: '', explicit: false, aliases: [], kidRequest: { key: 'tmdb:1', allowed: true, request: null } }])
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

  it('a kid with requests open gets the request search', async () => {
    const f = setup(user({ isKid: true, canRequest: true }))
    await session.login(2)
    renderSearch()
    await userEvent.type(screen.getByRole('searchbox'), 'Дюна')
    expect(await screen.findByRole('heading', { name: 'Найти и попросить' }, { timeout: 3000 })).toBeInTheDocument()
    await new Promise((r) => setTimeout(r, 700)) // past the catalog debounce
    const urls = f.mock.calls.map(([u]) => String(u))
    expect(urls.some((u) => u.includes('/api/requests/search') && u.includes('kind=video'))).toBe(true)
    expect(urls.some((u) => u.includes('/api/discover'))).toBe(false)
    expect(screen.queryByText('Найти и добавить')).not.toBeInTheDocument()
  })

  it('without an addon: no «can be added» hint, no catalog search', async () => {
    const f = setup(user({}))
    await session.login(2)
    renderSearch()
    expect(screen.queryByText(/can be added|можно добавить/)).not.toBeInTheDocument()
    await userEvent.type(screen.getByRole('searchbox'), 'Дюна')
    expect(await screen.findByText('Ничего не найдено в медиатеке', {}, { timeout: 3000 })).toBeInTheDocument()
    await new Promise((r) => setTimeout(r, 700)) // past the discover debounce
    expect(f.mock.calls.some(([u]) => String(u).includes('/api/discover'))).toBe(false)
    expect(screen.queryByRole('button', { name: 'На сервер' })).not.toBeInTheDocument()
  })

  it('while the addons still load, neither hint shows (no «no» first, then a flip)', async () => {
    setup(user({ addons: true }))
    await session.login(2)
    renderSearch()
    expect(screen.queryByText(/можно добавить/)).not.toBeInTheDocument()
    expect(screen.queryByText('Ищите фильмы, музыку и книги в медиатеке.')).not.toBeInTheDocument()
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

describe('search blocks from addons', () => {
  type Seen = { q: string; scope: SearchScope; text: string; nothing: boolean }
  let seen: Seen[] = []
  const block = {
    element: (p: Seen) => {
      seen.push(p)
      return <p>блок: {p.q}</p>
    },
  }
  beforeEach(() => {
    session.reset()
    seen = []
  })
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
    resetAddonsForTests()
  })

  it('renders search blocks for adults with q ≥ 2', async () => {
    setup(user({}))
    await session.login(2)
    registerAddon('dl', { searchBlocks: [block] })
    renderSearch('/search?scope=music')
    expect(screen.getByText(/можно добавить/)).toBeInTheDocument()
    await userEvent.type(screen.getByRole('searchbox'), 'Д')
    await new Promise((r) => setTimeout(r, 400)) // past the query debounce
    expect(screen.queryByText(/^блок/)).not.toBeInTheDocument()
    await userEvent.type(screen.getByRole('searchbox'), 'ю')
    expect(await screen.findByText('блок: Дю', {}, { timeout: 3000 })).toBeInTheDocument()
    expect(seen.at(-1)).toMatchObject({ q: 'Дю', text: 'Дю', scope: 'music' })
    await screen.findByText('Ничего не найдено в медиатеке')
    expect(seen.at(-1)?.nothing).toBe(true)
  })

  it('a kid never gets a block', async () => {
    setup(user({ isKid: true }))
    await session.login(2)
    registerAddon('dl', { searchBlocks: [block] })
    renderSearch('/search?q=Дюна')
    expect(await screen.findByText('Ничего не найдено в медиатеке', {}, { timeout: 3000 })).toBeInTheDocument()
    expect(screen.queryByText(/^блок/)).not.toBeInTheDocument()
    expect(seen).toEqual([])
  })

  it('an admin-only block is the admin\'s', async () => {
    setup(user({ id: 1, isAdmin: true }))
    await session.login(1)
    act(() => setAddonUser({ isAdmin: true }))
    registerAddon('dl', { searchBlocks: [{ ...block, admin: true }] })
    renderSearch('/search?q=Дюна')
    expect(await screen.findByText('блок: Дюна', {}, { timeout: 3000 })).toBeInTheDocument()
  })

  it('a throwing block leaves the results', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {})
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    setup(user({}), { library: [card(1, 'movie', 'Дюна')] })
    await session.login(2)
    registerAddon('bad', {
      searchBlocks: [
        {
          element: () => {
            throw new Error('boom')
          },
        },
      ],
    })
    registerAddon('dl', { searchBlocks: [block] })
    renderSearch('/search?q=Дюна')
    expect(await screen.findByText('блок: Дюна', {}, { timeout: 3000 })).toBeInTheDocument()
    expect(await screen.findByRole('region', { name: 'Лучшее совпадение' })).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})
