// «Попросить»: a kid asks where an adult would download (search results,
// title pages), sees the answer there and in «Мои просьбы»; an admin answers
// in «Управление → Просьбы» and sees the count on the avatar and in the menu.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router'
import type { KidRequest, User } from '../../api/types'
import { ProfileMenu } from '../../components/ProfileMenu'
import { resetKidRequests } from '../../state/kidRequests'
import { session, useSession } from '../../state/session'
import { MyRequests } from '../MyRequests'
import { Manage } from '../manage/Manage'
import { Requests } from '../manage/Requests'

const kid: User = { id: 3, name: 'Вася', avatar: 'grad-3', hasPassword: false, isAdmin: false, isKid: true, maxRating: null, canRequest: true }
const admin: User = { id: 1, name: 'Папа', avatar: 'grad-1', hasPassword: false, isAdmin: true, isKid: false, maxRating: null, requests: true, canAcquire: true }

const json = (b: unknown, status = 200) =>
  new Response(b === undefined ? null : JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } })

const req = (o: Partial<KidRequest>): KidRequest => ({
  id: 1, key: 'tmdb:1', kind: 'movie', tmdbId: 1, extId: '', title: 'Дюна', subtitle: '', year: 2021, poster: '', aliases: [],
  rating: '12+', status: 'pending', reason: '', libraryItemId: null, createdAt: new Date().toISOString(), ...o,
})

type Handler = (url: string, init?: RequestInit) => Response | undefined

function stub(user: User, handle: Handler = () => undefined) {
  const f = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input)
    if (url.endsWith('/api/login')) return json(user)
    const r = handle(url, init)
    if (r) return r
    if (url.includes('/api/search')) return json({ library: [], artists: [], albums: [], songs: [], books: [], audiobooks: [], comics: [] })
    if (url.endsWith('/api/requests/mine')) return json([])
    if (url.endsWith('/api/requests/count')) return json({ pending: 0 })
    return json({})
  })
  vi.stubGlobal('fetch', f)
  return f
}

describe('«Попросить» (kid)', () => {
  beforeEach(() => {
    session.reset()
    resetKidRequests()
  })
  afterEach(() => vi.unstubAllGlobals())

  it('«Мои просьбы» lists every answer, a refusal with its reason', async () => {
    stub(kid, (url) => {
      if (url.endsWith('/api/requests/mine'))
        return json([
          req({ id: 1, status: 'pending' }),
          req({ id: 2, key: 'ext:mb-1', kind: 'music', title: 'Группа крови', subtitle: 'Кино', status: 'declined', reason: 'Сначала уроки' }),
          req({ id: 3, key: 'tmdb:5', tmdbId: 5, title: 'Брат', status: 'in_library', libraryItemId: 9 }),
          // above the limit now (it was lowered): no title, no link
          req({ id: 4, key: 'tmdb:18', tmdbId: 18, title: '', hidden: true }),
        ])
    })
    await session.login(3)
    render(
      <MemoryRouter>
        <MyRequests />
      </MemoryRouter>,
    )
    expect(await screen.findAllByText('Ждёт ответа')).toHaveLength(2)
    expect(screen.getByText('Родитель отказал')).toBeInTheDocument()
    expect(screen.getByText('«Сначала уроки»')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Уже в медиатеке' })).toHaveAttribute('href', '/item/9')
    expect(screen.getByText('Недоступно для этого профиля')).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: 'Недоступно для этого профиля' })).toBeNull()
    expect(document.querySelector('a[href="/title/movie/18"]')).toBeNull()
  })

  it('the profile menu has «Мои просьбы»', async () => {
    stub(kid)
    await session.login(3)
    render(
      <MemoryRouter>
        <Menu />
      </MemoryRouter>,
    )
    await userEvent.click(screen.getByRole('button', { name: /Вася/ }))
    expect(screen.getByRole('menuitem', { name: 'Мои просьбы' })).toBeInTheDocument()
  })
})

function Menu() {
  const { user } = useSession()
  return user ? <ProfileMenu user={user} /> : null
}

describe('«Просьбы» (admin)', () => {
  beforeEach(() => {
    session.reset()
    resetKidRequests()
  })
  afterEach(() => vi.unstubAllGlobals())

  it('the avatar has a dot and «Управление» the count', async () => {
    stub(admin, (url) => (url.endsWith('/api/requests/count') ? json({ pending: 2 }) : undefined))
    await session.login(1)
    render(
      <MemoryRouter>
        <Menu />
      </MemoryRouter>,
    )
    const trigger = screen.getByRole('button', { name: /Папа/ })
    await waitFor(() => expect(trigger).toHaveTextContent('2 просьбы ждут ответа'))
    await userEvent.click(trigger)
    expect(screen.getByRole('menuitem', { name: /Управление/ })).toHaveTextContent('2')
  })
})

describe('«Просьбы» in both editions (core)', () => {
  beforeEach(() => {
    session.reset()
    resetKidRequests()
  })
  afterEach(() => vi.unstubAllGlobals())

  const renderManage = () =>
    render(
      <MemoryRouter initialEntries={['/manage/files']}>
        <Routes>
          <Route path="/manage" element={<Manage />}>
            <Route path="*" element={null} />
          </Route>
        </Routes>
      </MemoryRouter>,
    )

  it('core: «Просьбы» shows only with me.requests', async () => {
    stub({ ...admin, requests: undefined })
    await session.login(1)
    const { unmount } = renderManage()
    expect(screen.queryByRole('link', { name: /Просьбы/ })).toBeNull()
    unmount()
    session.reset()
    stub(admin, (url) => (url.endsWith('/api/requests/count') ? json({ pending: 0 }) : undefined))
    await session.login(1)
    renderManage()
    expect(screen.getByRole('link', { name: /Просьбы/ })).toHaveAttribute('href', '/manage/requests')
  })

  // canAcquire is false: what the core build has
  it('core build: without downloads «Одобрить» approves', async () => {
    const user: User = { ...admin, canAcquire: false }
    let approved = false
    const asker = { id: 3, name: 'Вася', avatar: 'grad-3' }
    const f = stub(user, (url, init) => {
      if (url.endsWith('/api/requests') && !init?.method?.match(/POST/)) return json([req({ id: 5, status: approved ? 'approved' : 'pending', user: asker })])
      if (url.endsWith('/api/requests/5/approve')) {
        approved = true
        return json(req({ id: 5, status: 'approved', user: asker }))
      }
      if (url.endsWith('/api/requests/count')) return json({ pending: approved ? 0 : 1 })
    })
    await session.login(1)
    render(
      <MemoryRouter>
        <Requests />
      </MemoryRouter>,
    )
    await userEvent.click(await screen.findByRole('button', { name: 'Одобрить «Дюна»' }))
    await waitFor(() => expect(f.mock.calls.some(([u, i]) => String(u).endsWith('/api/requests/5/approve') && i?.method === 'POST')).toBe(true))
    const decided = await screen.findByRole('list', { name: 'Отвеченные за 30 дней' })
    expect(within(decided).getByText('Одобрено')).toBeInTheDocument()
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(f.mock.calls.some(([u]) => String(u).includes('/api/releases'))).toBe(false)
  })
})
