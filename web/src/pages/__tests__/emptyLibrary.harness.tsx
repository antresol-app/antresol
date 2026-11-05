// The six empty library pages over a server with nothing on it, for
// emptyLibrary.test.tsx (the core build) and emptyLibrary.full.test.tsx.
import { render } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router'
import { vi } from 'vitest'
import type { User } from '../../api/types'
import { session } from '../../state/session'
import { BooksGrid } from '../books/BooksGrid'
import { BooksHome } from '../books/BooksHome'
import { Browse } from '../Browse'
import { Home } from '../Home'
import { Albums } from '../music/Albums'
import { MusicHome } from '../music/MusicHome'

const json = (b: unknown, status = 200) => new Response(JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } })

export const admin: User = { id: 1, name: 'Админ', avatar: 'grad-1', hasPassword: true, isAdmin: true, isKid: false, maxRating: null }
export const adult: User = { ...admin, id: 2, name: 'Мама', isAdmin: false }

export const PAGES = {
  home: <Home />,
  movies: <Browse kind="movie" title="Фильмы" emptyTitle="Фильмов пока нет" />,
  music: <MusicHome />,
  albums: <Albums />,
  books: <BooksHome />,
  grid: (
    <Routes>
      <Route path="/" element={<BooksGrid />} />
      <Route path="/books/:kind" element={<BooksGrid />} />
    </Routes>
  ),
}
export type Page = keyof typeof PAGES

export async function renderEmpty(page: Page, user: User) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const u = String(input)
      if (u.endsWith('/api/login')) return json(user)
      if (u.includes('/api/home')) return json({ hero: [], shelves: [] })
      if (u.includes('/api/music/home') || u.includes('/api/books/home')) return json({ shelves: [] })
      if (u.includes('/api/items') || u.includes('/api/music/albums') || u.includes('/api/books')) return json({ items: [], total: 0 })
      return json({ error: 'нет' }, 404)
    }),
  )
  await session.login(user.id)
  return render(<MemoryRouter initialEntries={[page === 'grid' ? '/books/book' : '/']}>{PAGES[page]}</MemoryRouter>)
}
