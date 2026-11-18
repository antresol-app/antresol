// An addon's full-window route (`fullWindow: true`): drawn like the core's
// players, outside the shell (no navigation, no mini player), with the
// route's own params; its other routes stay pages inside the shell; kids
// never get one.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, render, screen, waitFor } from '@testing-library/react'
import { useEffect } from 'react'
import { RouterProvider, createMemoryRouter } from 'react-router'
import { session } from '../../state/session'
import { registerAddon, resetAddonsForTests } from '../../addons/registry'
import { importer, resetLoaderForTests } from '../../addons/loader'
import { routes } from '../routes'

const adult = { id: 2, name: 'Мама', avatar: 'grad-3', hasPassword: false, isAdmin: false, isKid: false, maxRating: null }
const admin = { ...adult, id: 1, name: 'Папа', isAdmin: true, addons: true }
const kid = { ...adult, id: 3, name: 'Вася', isKid: true }

const json = (b: unknown) => new Response(JSON.stringify(b), { status: 200, headers: { 'Content-Type': 'application/json' } })

async function signIn(user: { id: number }) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (url.endsWith('/api/login')) return json(user)
      if (url.endsWith('/api/addons/web')) return json([{ id: 'hello', entry: '/addons/hello/ui/index.js' }])
      return json({})
    }),
  )
  await session.login(user.id)
}

// mounts of the nav badge's counting component (a remount would refetch its count)
let badgeMounts = 0
function useCount() {
  useEffect(() => {
    badgeMounts++
  }, [])
  return 3
}

function register() {
  registerAddon('hello', {
    navBadges: [{ to: 'page', icon: 'puzzle', label: 'Ждут: {count}', useCount }],
    routes: [
      {
        path: 'play/:kind/:tmdbId',
        title: 'Плеер',
        fullWindow: true,
        element: ({ params }) => (
          <h1>
            Плеер {params.kind} {params.tmdbId}
          </h1>
        ),
      },
      { path: 'page', title: 'Страница', element: () => <h1>Страница привета</h1> },
    ],
  })
}

function addon(gate?: Promise<void>) {
  return vi.spyOn(importer, 'load').mockImplementation(async () => {
    await gate
    register()
    return {}
  })
}

const KEY = 'antresol.addonFullWindow.hello'

describe('addon routes', () => {
  beforeEach(() => {
    badgeMounts = 0
    localStorage.clear()
    session.reset()
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    vi.spyOn(console, 'error').mockImplementation(() => {})
  })
  afterEach(() => {
    cleanup()
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
    session.reset()
    resetAddonsForTests()
    resetLoaderForTests()
    globalThis.__antresol = undefined
  })

  it('a fullWindow addon route mounts outside the shell, never for kids', async () => {
    await signIn(admin)
    addon()
    const router = createMemoryRouter(routes, { initialEntries: ['/addons/hello/play/movie/77'] })
    render(<RouterProvider router={router} />)
    expect(await screen.findByRole('heading', { name: 'Плеер movie 77' })).toBeInTheDocument()
    // the whole window: none of the shell around it
    expect(document.getElementById('main')).toBeNull()
    expect(screen.queryByRole('navigation')).toBeNull()

    // the addon's other route is a page in the shell
    await router.navigate('/addons/hello/page')
    expect(await screen.findByRole('heading', { name: 'Страница привета' })).toBeInTheDocument()
    expect(document.getElementById('main')).not.toBeNull()
    cleanup()
    session.reset()
    resetAddonsForTests()
    resetLoaderForTests()
    globalThis.__antresol = undefined

    // a kid: sent home, nothing of the addon drawn
    await signIn(kid)
    addon()
    const kids = createMemoryRouter(routes, { initialEntries: ['/addons/hello/play/movie/77'] })
    render(<RouterProvider router={kids} />)
    await waitFor(() => expect(kids.state.location.pathname).toBe('/'))
    expect(screen.queryByRole('heading', { name: /Плеер/ })).toBeNull()
  })

  it('an adult the addon is listed for gets the full-window route too', async () => {
    await signIn(adult)
    addon()
    const router = createMemoryRouter(routes, { initialEntries: ['/addons/hello/play/series/5'] })
    render(<RouterProvider router={router} />)
    expect(await screen.findByRole('heading', { name: 'Плеер series 5' })).toBeInTheDocument()
    expect(document.getElementById('main')).toBeNull()
  })

  it('the shell stays mounted from a core page to an addon page: a nav badge keeps its instance', async () => {
    await signIn(admin)
    addon()
    const router = createMemoryRouter(routes, { initialEntries: ['/movies'] })
    render(<RouterProvider router={router} />)
    await waitFor(() => expect(badgeMounts).toBe(1))
    await act(() => router.navigate('/addons/hello/page'))
    expect(await screen.findByRole('heading', { name: 'Страница привета' })).toBeInTheDocument()
    await act(() => router.navigate('/movies'))
    await act(() => router.navigate('/addons/hello/page'))
    expect(await screen.findByRole('heading', { name: 'Страница привета' })).toBeInTheDocument()
    expect(badgeMounts).toBe(1)
  })

  it('a registration remembers its fullWindow patterns on this device', async () => {
    register()
    expect(JSON.parse(localStorage.getItem(KEY)!)).toEqual(['play/:kind/:tmdbId'])
    registerAddon('hello', { routes: [{ path: 'page', title: 'p', element: () => <p /> }] })
    expect(localStorage.getItem(KEY)).toBeNull()
  })

  it('a cold load of a remembered fullWindow route draws the black fallback, not the shell, until the bundle comes', async () => {
    localStorage.setItem(KEY, JSON.stringify(['play/:kind/:tmdbId']))
    await signIn(admin)
    let open!: () => void
    addon(new Promise<void>((r) => (open = r)))
    const router = createMemoryRouter(routes, { initialEntries: ['/addons/hello/play/movie/77'] })
    const { container } = render(<RouterProvider router={router} />)
    await waitFor(() => expect(container.querySelector('.playerFallback')).not.toBeNull())
    expect(document.getElementById('main')).toBeNull()
    expect(screen.queryByRole('navigation')).toBeNull()
    act(() => open())
    expect(await screen.findByRole('heading', { name: 'Плеер movie 77' })).toBeInTheDocument()
    expect(container.querySelector('.playerFallback')).toBeNull()
    expect(document.getElementById('main')).toBeNull()
  })

  it('without storage (or an addon never seen) a cold load keeps the shell until the bundle comes', async () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('denied')
    })
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('denied')
    })
    await signIn(admin)
    let open!: () => void
    addon(new Promise<void>((r) => (open = r)))
    const router = createMemoryRouter(routes, { initialEntries: ['/addons/hello/play/movie/77'] })
    const { container } = render(<RouterProvider router={router} />)
    await waitFor(() => expect(document.getElementById('main')).not.toBeNull())
    expect(container.querySelector('.playerFallback')).toBeNull()
    act(() => open())
    expect(await screen.findByRole('heading', { name: 'Плеер movie 77' })).toBeInTheDocument()
    expect(document.getElementById('main')).toBeNull()
  })

  it('a remembered addon the profile no longer has: the shell once the addons settled', async () => {
    localStorage.setItem('antresol.addonFullWindow.gone', JSON.stringify(['play/:id']))
    await signIn(admin)
    addon()
    const router = createMemoryRouter(routes, { initialEntries: ['/addons/gone/play/1'] })
    const { container } = render(<RouterProvider router={router} />)
    await waitFor(() => expect(document.getElementById('main')).not.toBeNull())
    expect(await screen.findByText('Такого раздела у расширения нет')).toBeInTheDocument()
    expect(container.querySelector('.playerFallback')).toBeNull()
  })
  // the app draws an addon page's frame: «Управление» around the pages its
  // tiles open (and the screens under them), the plain page frame elsewhere;
  // the addon draws none (Sources lost its frame when it left the core)
  it('a page a tile of «Управление» opens is drawn in «Управление»; another page in the plain frame', async () => {
    await signIn(admin)
    vi.spyOn(importer, 'load').mockImplementation(async () => {
      registerAddon('hello', {
        manageTiles: [{ label: 'tile', icon: 'puzzle', to: 'sources', admin: true }],
        routes: [
          { path: 'sources', title: 'Источники', element: () => <h1>Источники привета</h1> },
          { path: 'sources/:id/edit', title: 'Источник', element: () => <h1>Правка источника</h1> },
          { path: 'mine', title: 'Мои', element: () => <h1>Мои слежения</h1> },
        ],
      })
      return {}
    })
    const router = createMemoryRouter(routes, { initialEntries: ['/addons/hello/sources'] })
    render(<RouterProvider router={router} />)
    const main = () => document.getElementById('main')!
    expect(await screen.findByRole('heading', { name: 'Источники привета' })).toBeInTheDocument()
    expect(main().textContent).toContain('Управление')
    // the tile is in the section list, and it is the current section
    expect(main().querySelector('a[href="/addons/hello/sources"]')).not.toBeNull()

    await act(() => router.navigate('/addons/hello/sources/5/edit'))
    expect(await screen.findByRole('heading', { name: 'Правка источника' })).toBeInTheDocument()
    expect(main().querySelector('a[href="/addons/hello/sources"]')).not.toBeNull()

    await act(() => router.navigate('/addons/hello/mine'))
    const h = await screen.findByRole('heading', { name: 'Мои слежения' })
    expect(main().textContent).not.toContain('Управление')
    // still in a frame of the app's, not straight in <main>
    expect(h.parentElement).not.toBe(main())
  })
})
