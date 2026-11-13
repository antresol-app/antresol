// Final review, fix B (web minors): item links by kind, empty Music/Books for kids and adults,
// download nouns by kind, the passwordless note on the app-password sheet.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router'
import type { ItemDetail as Detail, User } from '../../api/types'
import { itemHref } from '../../components/Card'
import { session } from '../../state/session'
import { BooksHome } from '../books/BooksHome'
import { ItemDetail } from '../ItemDetail'
import { MusicHome } from '../music/MusicHome'

const json = (b: unknown, status = 200) => new Response(JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } })
function stubFetch(h: (url: string, init?: RequestInit) => Response | undefined) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => h(String(input), init) ?? json({ error: 'нет' }, 404)),
  )
}
const adult: User = { id: 2, name: 'Мама', avatar: 'grad-3', hasPassword: false, isAdmin: false, isKid: false, maxRating: null }
const kid: User = { ...adult, id: 3, name: 'Вася', isKid: true }

function Where() {
  return <span data-testid="where">{useLocation().pathname}</span>
}

beforeEach(() => session.reset())
afterEach(() => {
  vi.unstubAllGlobals()
  session.reset()
})

describe('F19: library links by kind', () => {
  it('itemHref sends each kind to its own page', () => {
    expect(itemHref('movie', 1)).toBe('/item/1')
    expect(itemHref('album', 2)).toBe('/music/album/2')
    expect(itemHref('audiobook', 3)).toBe('/books/audiobook/3')
    expect(itemHref('book', 4)).toBe('/books/4')
    expect(itemHref('comic_volume', 5)).toBe('/read/5')
  })

  it('an old /item link to an audiobook lands on the audiobook page', async () => {
    const book = { id: 9, kind: 'audiobook', title: 'Книга', subtitle: '', poster: '', backdrop: '', logo: '', progress: 0, onServer: true,
      overview: '', genres: [], cast: [], files: [], inWatchlist: false, seasons: [], actions: { play: true, toDevice: false, toServer: false, watchNow: false } } as unknown as Detail
    stubFetch((u) => (u.includes('/api/items/9') ? json(book) : undefined))
    render(
      <MemoryRouter initialEntries={['/item/9']}>
        <Routes>
          <Route path="/item/:id" element={<ItemDetail />} />
          <Route path="*" element={<Where />} />
        </Routes>
      </MemoryRouter>,
    )
    expect(await screen.findByTestId('where')).toHaveTextContent('/books/audiobook/9')
  })
})

// F21, F25 (the menu's «На этом устройстве», the passwordless note) moved
// with them to «Настройки»: profileSettings.test.tsx

describe('F24: empty Music and Books', () => {
  const empty = (u: string) => (u.includes('/api/music/home') || u.includes('/api/books/home') ? json({ shelves: [] }) : undefined)

  it('kids are told to ask an adult, with no server or search advice', async () => {
    stubFetch((u) => (u.endsWith('/api/login') ? json(kid) : empty(u)))
    await session.login(kid.id)
    render(
      <MemoryRouter>
        <MusicHome />
        <BooksHome />
      </MemoryRouter>,
    )
    expect(await screen.findByText('Попросите взрослого добавить музыку.')).toBeInTheDocument()
    expect(await screen.findByText('Попросите взрослого добавить книги.')).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: 'Найти' })).toBeNull()
    expect(screen.queryByText(/на сервере/)).toBeNull()
  })
})
