import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import type { Card } from '../../api/types'
import { scopeOf, searchHref } from '../../app/sections'
import { Badges } from '../../ui/Badge'
import { session } from '../../state/session'
import { Search, inScope, topResult } from '../Search'
import { setLanguagePref } from '../../i18n'

const json = (b: unknown) => new Response(JSON.stringify(b), { status: 200, headers: { 'Content-Type': 'application/json' } })
const card = (id: number, kind: string, title: string): Card => ({ id, kind, title, subtitle: '', poster: '', backdrop: '', logo: '', progress: 0, onServer: true })
const EMPTY = { library: [], artists: [], albums: [], songs: [], books: [], audiobooks: [], comics: [] }

afterEach(() => vi.unstubAllGlobals())

describe('search scope', () => {
  it('follows the section the search was opened from', () => {
    expect(scopeOf('/movies')).toBe('movies')
    expect(scopeOf('/series/4')).toBe('series')
    expect(scopeOf('/music/album/3')).toBe('music')
    // the Books section holds audiobooks and manga too: its search keeps every kind
    expect(scopeOf('/books/audiobook/9')).toBe('all')
    expect(scopeOf('/')).toBe('all')
    expect(searchHref('/movies')).toBe('/search?scope=movies')
    expect(searchHref('/')).toBe('/search')
    expect(searchHref('/search')).toBe('/search')
  })

  it('narrows a library answer to the scope', () => {
    const r = {
      ...EMPTY,
      library: [card(1, 'movie', 'Дюна'), card(2, 'series', 'Дюна: Пророчество')],
      albums: [card(3, 'album', 'Дюна')],
      audiobooks: [card(4, 'audiobook', 'Дюна')],
    }
    expect(inScope(r, 'movies').library.map((c) => c.id)).toEqual([1])
    expect(inScope(r, 'series').library.map((c) => c.id)).toEqual([2])
    expect(inScope(r, 'music').albums).toHaveLength(1)
    expect(inScope(r, 'music').library).toHaveLength(0)
    // books, audiobooks and manga are a filter each, as in «Найти и добавить»
    expect(inScope(r, 'books').audiobooks).toHaveLength(0)
    expect(inScope(r, 'audiobooks').audiobooks).toHaveLength(1)
    expect(inScope(r, 'comics').audiobooks).toHaveLength(0)
    expect(inScope(r, 'all')).toBe(r)
    expect(topResult(inScope(r, 'music'), 'дюна')?.id).toBe(3)
  })

  it('a kind with one result that is the best match is not shown twice', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input)
        if (url.endsWith('/api/login')) return json({ id: 2, name: 'М', avatar: 'grad-3', hasPassword: false, isAdmin: false, isKid: true, maxRating: null })
        if (url.includes('/api/search')) return json({ ...EMPTY, library: [card(2, 'series', 'Дюна: Пророчество')], books: [card(5, 'book', 'Дюна')] })
        return json([])
      }),
    )
    await session.login(2)
    render(
      <MemoryRouter initialEntries={['/search?q=дюна: пророчество']}>
        <Search />
      </MemoryRouter>,
    )
    await screen.findByRole('region', { name: 'Лучшее совпадение' }, { timeout: 3000 })
    expect(screen.queryByRole('region', { name: 'Фильмы и сериалы' })).not.toBeInTheDocument()
    expect(screen.getByRole('region', { name: 'Книги' })).toBeInTheDocument()
  })

  it('without songs the best match sits beside «Ещё совпадения»; kinds with a few items share a row', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input)
        if (url.endsWith('/api/login')) return json({ id: 2, name: 'М', avatar: 'grad-3', hasPassword: false, isAdmin: false, isKid: true, maxRating: null })
        if (url.includes('/api/search'))
          return json({ ...EMPTY, library: [card(1, 'movie', 'Дюна'), card(2, 'movie', 'Дюна: Часть вторая')], books: [card(5, 'book', 'Мессия Дюны')], audiobooks: [card(6, 'audiobook', 'Дети Дюны')] })
        return json([])
      }),
    )
    await session.login(2)
    render(
      <MemoryRouter initialEntries={['/search?q=дюна']}>
        <Search />
      </MemoryRouter>,
    )
    const more = await screen.findByRole('region', { name: 'Ещё совпадения' }, { timeout: 3000 })
    expect(more.parentElement).toBe(screen.getByRole('region', { name: 'Лучшее совпадение' }).parentElement)
    expect(within(more).getAllByRole('link').map((a) => a.textContent)).toEqual([
      expect.stringContaining('Дюна: Часть вторая'),
      expect.stringContaining('Мессия Дюны'),
      expect.stringContaining('Дети Дюны'),
    ])
    expect(screen.getByRole('region', { name: 'Книги' }).parentElement).toBe(screen.getByRole('region', { name: 'Аудиокниги' }).parentElement)
  })

  it('opens in the scope of ?scope= and only shows that kind', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input)
        if (url.endsWith('/api/login')) return json({ id: 2, name: 'М', avatar: 'grad-3', hasPassword: false, isAdmin: false, isKid: true, maxRating: null })
        if (url.includes('/api/search')) return json({ ...EMPTY, library: [card(1, 'movie', 'Дюна'), card(4, 'movie', 'Дюна: Часть вторая'), card(2, 'series', 'Дюна: Пророчество')], albums: [card(3, 'album', 'Дюна')] })
        return json({})
      }),
    )
    await session.login(2)
    render(
      <MemoryRouter initialEntries={['/search?q=дюна&scope=movies']}>
        <Search />
      </MemoryRouter>,
    )
    const shelf = await screen.findByRole('region', { name: 'Фильмы' }, { timeout: 3000 })
    await waitFor(() => expect(within(shelf).queryByText('Дюна: Пророчество')).not.toBeInTheDocument())
    expect(screen.queryByRole('region', { name: 'Альбомы' })).not.toBeInTheDocument()
    expect(screen.getByRole('radio', { name: 'Фильмы' })).toBeChecked()
  })

  it('speaks English when English is picked', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input)
        if (url.endsWith('/api/login')) return json({ id: 2, name: 'М', avatar: 'grad-3', hasPassword: false, isAdmin: false, isKid: true, maxRating: null })
        if (url.includes('/api/search')) return json({ ...EMPTY, library: [card(1, 'movie', 'Dune'), card(2, 'movie', 'Dune: Part Two')] })
        return json({})
      }),
    )
    setLanguagePref('en')
    await session.login(2)
    render(
      <MemoryRouter initialEntries={['/search?q=dune']}>
        <Search />
      </MemoryRouter>,
    )
    expect(screen.getByRole('heading', { level: 1, name: 'Search' })).toBeInTheDocument()
    expect(screen.getByRole('radio', { name: 'TV Shows' })).toBeInTheDocument()
    expect(await screen.findByRole('region', { name: 'Movies & TV Shows' }, { timeout: 3000 })).toBeInTheDocument()
    expect(screen.getByText('Top Result')).toBeInTheDocument()
  })

  it('renders badges', () => {
    render(<Badges items={['FLAC 24/96', 'RU']} />)
    expect(screen.getByRole('list', { name: 'Характеристики' })).toHaveTextContent('FLAC 24/96')
  })
})
