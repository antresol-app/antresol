// The item page and the core title page (/title/:kind/:tmdbId, GET
// /api/title/…): a TMDB title that may not be in the library, for every
// profile; a kid gets «Попросить» and nothing of an addon.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router'
import type { ItemDetail as Detail, KidRequest, TitleInfo, User } from '../../api/types'
import { registerAddon, resetAddonsForTests } from '../../addons/registry'
import { resetKidRequests } from '../../state/kidRequests'
import { session } from '../../state/session'
import { ItemDetail } from '../ItemDetail'

const adult: User = { id: 2, name: 'Мама', avatar: 'grad-3', hasPassword: false, isAdmin: false, isKid: false, maxRating: null }
const kid: User = { ...adult, id: 3, name: 'Вася', isKid: true, canRequest: true }

const json = (b: unknown, status = 200) => new Response(JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } })

const request = (o: Partial<KidRequest>): KidRequest => ({
  id: 1, key: 'tmdb:42', kind: 'movie', tmdbId: 42, extId: '', title: 'Дюна', subtitle: '', year: 2021, poster: '', aliases: [],
  rating: '12+', status: 'pending', reason: '', libraryItemId: null, createdAt: new Date().toISOString(), ...o,
})

const title = {
  tmdbId: 42, kind: 'movie', title: 'Дюна', year: 2021, poster: '', backdrop: '', overview: 'Пустынная планета.',
  inLibrary: false, libraryItemId: null, genres: ['Фантастика'], seasons: [],
}

function stub(user: User, answer: () => Response) {
  const f = vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input)
    if (url.endsWith('/api/login')) return json(user)
    if (url.includes('/api/title/movie/42')) return answer()
    if (url.endsWith('/api/requests/mine')) return json([])
    return json({})
  })
  vi.stubGlobal('fetch', f)
  return f
}

function renderTitle() {
  return render(
    <MemoryRouter initialEntries={['/title/movie/42']}>
      <Routes>
        <Route path="/title/:kind/:tmdbId" element={<ItemDetail />} />
        <Route path="/item/:id" element={<ItemDetail />} />
      </Routes>
    </MemoryRouter>,
  )
}

// what an addon would add to the page: a panel and a way of «Смотреть ▾»
function registerFake() {
  registerAddon('fake', {
    itemPanels: [{ element: () => <p>Панель дополнения</p> }],
    itemActions: [{ label: 'Способ дополнения', icon: 'play', watchWay: true, run: () => undefined }],
  })
}

describe('the core title page', () => {
  beforeEach(() => {
    session.reset()
    resetKidRequests()
    resetAddonsForTests()
  })
  afterEach(() => vi.unstubAllGlobals())

  it('a title page renders from /api/title for adults and kids', async () => {
    registerFake()
    const f = stub(adult, () => json(title))
    await session.login(adult.id)
    const page = renderTitle()
    expect(await screen.findByRole('heading', { name: 'Дюна' })).toBeInTheDocument()
    expect(screen.getByText('Пустынная планета.')).toBeInTheDocument()
    expect(f.mock.calls.some(([u]) => String(u).includes('/api/title/movie/42'))).toBe(true)
    expect(f.mock.calls.some(([u]) => String(u).includes('/api/discover/'))).toBe(false)
    page.unmount()

    // a kid: «Попросить», no addon panel, no addon way
    session.reset()
    stub(kid, () => json({ ...title, kidRequest: { key: 'tmdb:42', allowed: true } }))
    await session.login(kid.id)
    renderTitle()
    expect(await screen.findByRole('heading', { name: 'Дюна' })).toBeInTheDocument()
    expect(await screen.findByRole('button', { name: /Попросить/ })).toBeInTheDocument()
    expect(screen.queryByText('Панель дополнения')).toBeNull()
    expect(screen.queryByRole('button', { name: 'Способ дополнения' })).toBeNull()
    expect(screen.queryByRole('menuitemradio', { name: 'Способ дополнения' })).toBeNull()
  })

  it('a kid’s request in progress shows its state', async () => {
    stub(kid, () => json({ ...title, kidRequest: { key: 'tmdb:42', allowed: true, request: request({ status: 'approved' }) } }))
    await session.login(kid.id)
    renderTitle()
    expect(await screen.findByText('Одобрено — скоро появится')).toBeInTheDocument()
  })

  it('over a kid’s limit: «Недоступно для этого профиля» and nothing of the title', async () => {
    registerFake()
    stub(kid, () => json({ error: 'Недоступно для этого профиля' }, 403))
    await session.login(kid.id)
    renderTitle()
    expect(await screen.findByRole('heading', { name: 'Недоступно для этого профиля' })).toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: 'Дюна' })).toBeNull()
    expect(screen.queryByRole('button', { name: /Попросить/ })).toBeNull()
    expect(screen.queryByText('Панель дополнения')).toBeNull()
  })

  it('a title in the library opens its library page', async () => {
    stub(adult, () => json({ ...title, inLibrary: true, libraryItemId: 9 }))
    const f = vi.mocked(fetch)
    await session.login(adult.id)
    renderTitle()
    await vi.waitFor(() => expect(f.mock.calls.some(([u]) => String(u).includes('/api/items/9'))).toBe(true))
  })
})

const film = {
  id: 9, kind: 'movie', title: 'Интерстеллар', subtitle: '', poster: '', backdrop: '', logo: '', progress: 0, onServer: true,
  overview: '', genres: [], cast: [], seasons: [], files: [], inWatchlist: false, tmdbId: 157336,
  actions: { play: true, toServer: false, toDevice: false, watchNow: false },
} as unknown as Detail

function renderItem() {
  return render(
    <MemoryRouter initialEntries={['/item/9']}>
      <Routes>
        <Route path="/item/:id" element={<ItemDetail />} />
      </Routes>
    </MemoryRouter>,
  )
}

describe('the addons\' panels of an item and a title page', () => {
  beforeEach(() => {
    session.reset()
    resetAddonsForTests()
  })
  afterEach(() => vi.unstubAllGlobals())

  it('renders item panels and passes the title', async () => {
    const seen: { item: Detail; title?: TitleInfo }[] = []
    registerAddon('fake', {
      itemPanels: [
        {
          element: (p) => {
            seen.push(p)
            return <p>Панель: {p.item.title}</p>
          },
        },
      ],
    })
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input)
        if (url.endsWith('/api/login')) return json(adult)
        if (url.includes('/api/items/9')) return json(film)
        if (url.includes('/api/title/movie/42')) return json(title)
        return json({})
      }),
    )
    await session.login(adult.id)
    const page = renderItem()
    expect(await screen.findByText('Панель: Интерстеллар')).toBeInTheDocument()
    expect(seen.at(-1)!.item.id).toBe(9)
    expect(seen.at(-1)!.title).toBeUndefined()
    page.unmount()

    renderTitle()
    expect(await screen.findByText('Панель: Дюна')).toBeInTheDocument()
    expect(seen.at(-1)!.item.id).toBe(0)
    expect(seen.at(-1)!.title).toMatchObject({ tmdbId: 42, title: 'Дюна' })
  })

  it('a throwing panel leaves the page', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {})
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    registerAddon('broken', {
      itemPanels: [
        {
          element: () => {
            throw new Error('boom')
          },
        },
      ],
    })
    registerAddon('fine', { itemPanels: [{ element: () => <p>Другая панель</p> }] })
    stub(adult, () => json(title))
    await session.login(adult.id)
    renderTitle()
    expect(await screen.findByRole('heading', { name: 'Дюна' })).toBeInTheDocument()
    expect(await screen.findByText('Другая панель')).toBeInTheDocument()
    expect(screen.queryByText('Расширение не смогло показать этот раздел')).toBeNull()
    vi.restoreAllMocks()
  })
})
