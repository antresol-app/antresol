// «ТВ» comes back to where this device left it: /tv without ?f and ?view
// takes the kept category and view, and the scroll once the list loads; an
// address that says where to go wins; no storage, no harm.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes, useLocation, useNavigate } from 'react-router'
import { session } from '../../../state/session'
import { loadTvPlace, saveTvPlace } from '../../../state/tvPlace'
import { Tv } from '../Tv'

const ADMIN = { id: 1, name: 'Админ', avatar: 'grad-1', hasPassword: true, isAdmin: true, isKid: false, maxRating: null }
const ch = (id: number, categories: string[]) => ({ id, number: id, name: `Канал ${id}`, category: '', categories, enabled: true, external: true })
const LIST = {
  channels: Array.from({ length: 40 }, (_, i) => ch(i + 1, [i % 2 ? 'ai:news' : 'ai:movies'])),
  lastChannel: null,
  canManage: true,
  canCreate: true,
  favoritesFirst: false,
  categories: [
    { id: 'ai:news', name: 'Новости', kind: 'ai', count: 20 },
    { id: 'ai:movies', name: 'Кино', kind: 'ai', count: 20 },
  ],
}
const json = (body: unknown, status = 200) => Promise.resolve(new Response(body === null ? null : JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

function Where() {
  const l = useLocation()
  const go = useNavigate()
  return (
    <>
      <output data-testid="where">{l.search}</output>
      <button onClick={() => go('/tv?f=k%3Aai%3Amovies')}>to movies</button>
    </>
  )
}

const open = (path: string) =>
  render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route
          path="/tv"
          element={
            <>
              <Tv />
              <Where />
            </>
          }
        />
      </Routes>
    </MemoryRouter>,
  )

let scrolls: unknown[][] = []
beforeEach(async () => {
  localStorage.clear()
  scrolls = []
  vi.spyOn(window, 'scrollTo').mockImplementation((...a: unknown[]) => void scrolls.push(a))
  vi.spyOn(globalThis, 'fetch').mockImplementation((input: RequestInfo | URL) => {
    const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url
    if (url.endsWith('/api/login')) return json(ADMIN)
    if (url.startsWith('/api/channels?')) return json(LIST)
    if (url.startsWith('/api/channels/epg')) return json({ from: new Date().toISOString(), to: new Date(Date.now() + 3_600_000).toISOString(), channels: [] })
    return json(null, 204)
  })
  await session.login(ADMIN.id)
})
afterEach(() => {
  session.reset()
  vi.restoreAllMocks()
  localStorage.clear()
})

describe('«ТВ» keeps its place on the device', () => {
  it('opens the kept category and view and scrolls back once loaded', async () => {
    saveTvPlace({ f: 'k:ai:news', view: 'guide', scrollY: 640 })
    open('/tv')
    await waitFor(() => expect(screen.getByTestId('where').textContent).toBe('?f=k%3Aai%3Anews&view=guide'))
    await waitFor(() => expect(scrolls).toContainEqual([0, 640]))
  })

  it('takes the kept place once: the viewer’s next choice stays', async () => {
    saveTvPlace({ f: 'k:ai:news' })
    open('/tv')
    await waitFor(() => expect(screen.getByTestId('where').textContent).toBe('?f=k%3Aai%3Anews'))
    fireEvent.click(screen.getByRole('button', { name: 'to movies' }))
    await waitFor(() => expect(loadTvPlace().f).toBe('k:ai:movies'))
    await new Promise((r) => setTimeout(r, 30))
    expect(screen.getByTestId('where').textContent).toBe('?f=k%3Aai%3Amovies')
  })

  it('lets the address win, and keeps what it says', async () => {
    saveTvPlace({ f: 'k:ai:news', scrollY: 640 })
    open('/tv?f=k%3Aai%3Amovies')
    await waitFor(() => expect(loadTvPlace().f).toBe('k:ai:movies'))
    expect(screen.getByTestId('where').textContent).toBe('?f=k%3Aai%3Amovies')
    expect(scrolls).not.toContainEqual([0, 640])
  })

  it('reads a broken or missing store as nothing kept', () => {
    localStorage.setItem('vault.tvPlace', '{oops')
    expect(loadTvPlace()).toEqual({})
    const get = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('private window')
    })
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('private window')
    })
    expect(loadTvPlace()).toEqual({})
    expect(() => saveTvPlace({ f: 'all' })).not.toThrow()
    get.mockRestore()
  })
})
