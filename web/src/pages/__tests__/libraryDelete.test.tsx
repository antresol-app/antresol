// «Удалить» on a library item's page: admins only; after the delete the page
// is left (a series that loses one season stays and reloads).
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router'
import type { ItemDetail as Detail } from '../../api/types'
import { session } from '../../state/session'
import { ItemDetail } from '../ItemDetail'

const adult = { id: 2, name: 'Мама', avatar: 'grad-3', hasPassword: false, isAdmin: false, isKid: false, maxRating: null }
const admin = { ...adult, id: 1, name: 'Папа', isAdmin: true }
const kid = { ...adult, id: 3, name: 'Вася', isKid: true }

const json = (b: unknown) => new Response(JSON.stringify(b), { status: 200, headers: { 'Content-Type': 'application/json' } })

const movie = {
  id: 9, kind: 'movie', title: 'Дюна', subtitle: '', poster: '', backdrop: '', logo: '', progress: 0, onServer: true,
  overview: '', genres: [], cast: [], seasons: [], files: [{ id: 5, name: 'dune.mkv', size: 1 << 30, audio: [], subs: [] }],
  inWatchlist: false, actions: { play: true, toServer: false, toDevice: false, watchNow: false },
} as unknown as Detail

function stub(user: object) {
  const f = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input)
    if (url.endsWith('/api/login')) return json(user)
    if (url.endsWith('/api/items/9/delete') && init?.method === 'POST') return json({ items: [9], size: 1 << 30, trashed: 1, parentId: 0, parentShown: false })
    if (url.endsWith('/api/items/9/delete')) return json({ id: 9, kind: 'movie', title: 'Дюна', size: 1 << 30, files: 1, seasons: [], notes: [] })
    if (url.includes('/api/items/9')) return json(movie)
    return json({})
  })
  vi.stubGlobal('fetch', f)
  return f
}

async function open(user: { id: number }) {
  const f = stub(user)
  await session.login(user.id)
  render(
    <MemoryRouter initialEntries={['/movies', '/item/9']} initialIndex={1}>
      <Routes>
        <Route path="/movies" element={<p>movies page</p>} />
        <Route path="/item/:id" element={<ItemDetail />} />
      </Routes>
    </MemoryRouter>,
  )
  await screen.findByRole('heading', { name: 'Дюна' })
  return f
}

describe('«Удалить» on an item page', () => {
  beforeEach(() => session.reset())
  afterEach(() => {
    vi.unstubAllGlobals()
    session.reset()
  })

  it('is not there for other profiles or kids', async () => {
    await open(adult)
    expect(screen.queryByRole('button', { name: 'Удалить' })).toBeNull()
    session.reset()
    cleanup()
    await open(kid)
    expect(screen.queryByRole('button', { name: 'Удалить' })).toBeNull()
  })

  it('an admin deletes after the confirmation and is taken back', async () => {
    const f = await open(admin)
    await userEvent.click(screen.getByRole('button', { name: 'Удалить' }))
    const sheet = await screen.findByRole('alertdialog', { name: 'Удалить «Дюна»?' })
    await within(sheet).findByText('Освободится 1,0 ГБ, когда Корзину очистят.')
    await userEvent.click(within(sheet).getByRole('button', { name: 'Удалить' }))
    expect(await screen.findByText('movies page')).toBeInTheDocument()
    await waitFor(() => expect(f.mock.calls.some(([u, i]) => String(u).endsWith('/api/items/9/delete') && i?.method === 'POST')).toBe(true))
  })
})
