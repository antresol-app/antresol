// Final review, fix B (web minors): item links by kind, «На этом
// устройстве» in the profile menu, empty Music/Books for kids and adults,
// download nouns by kind, the passwordless note on the app-password sheet.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router'
import type { ItemDetail as Detail, User } from '../../api/types'
import { itemHref } from '../../components/Card'
import { ProfileMenu } from '../../components/ProfileMenu'
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

describe('F21, F25: the profile menu', () => {
  it('leads to «На этом устройстве» and warns a passwordless profile that apps work only at home', async () => {
    vi.stubGlobal('caches', { open: vi.fn() })
    stubFetch((u) => (u.endsWith('/api/me/app-password') ? json({ exists: false, createdAt: null }) : undefined))
    render(
      <MemoryRouter>
        <ProfileMenu user={adult} />
        <Routes>
          <Route path="*" element={<Where />} />
        </Routes>
      </MemoryRouter>,
    )
    fireEvent.click(screen.getByRole('button', { name: /Профиль/ }))
    fireEvent.click(screen.getByRole('menuitem', { name: 'На этом устройстве' }))
    expect(screen.getByTestId('where')).toHaveTextContent('/offline')

    fireEvent.click(screen.getByRole('button', { name: /Профиль/ }))
    fireEvent.click(screen.getByRole('menuitem', { name: 'Пароль для приложений…' }))
    expect(await screen.findByTestId('app-password-home-only')).toHaveTextContent('Вне дома приложения не подключатся, пока у профиля нет пароля')
  })

  it('no home-only note for a profile with a password', async () => {
    stubFetch((u) => (u.endsWith('/api/me/app-password') ? json({ exists: false, createdAt: null }) : undefined))
    render(
      <MemoryRouter>
        <ProfileMenu user={{ ...adult, hasPassword: true }} />
      </MemoryRouter>,
    )
    fireEvent.click(screen.getByRole('button', { name: /Профиль/ }))
    fireEvent.click(screen.getByRole('menuitem', { name: 'Пароль для приложений…' }))
    expect(await screen.findByRole('dialog', { name: 'Пароль для приложений' })).toBeInTheDocument()
    expect(screen.queryByTestId('app-password-home-only')).toBeNull()
  })
})

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
