// The addon slots in the core UI: «Управление» tiles, the item page's
// actions and the addon's own pages at /addons/<id>/<path> — for the
// profiles the server lists the addon for (never kids; `admin: true`
// entries for admins only), and an addon that throws never takes the page
// down.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { MemoryRouter, Outlet, Route, RouterProvider, Routes, createMemoryRouter } from 'react-router'
import type { ItemDetail as Detail } from '../../api/types'
import { session } from '../../state/session'
import { registerAddon, resetAddonsForTests, setActiveAddons, setAddonUser } from '../../addons/registry'
import { importer, resetLoaderForTests } from '../../addons/loader'
import { installShared } from '../../addons/shared'
import { Manage } from '../manage/Manage'
import { ItemDetail } from '../ItemDetail'
import { ActionBar } from '../../components/ActionBar'
import { routes } from '../../app/routes'
import { setLanguagePref } from '../../i18n'

const adult = { id: 2, name: 'Мама', avatar: 'grad-3', hasPassword: false, isAdmin: false, isKid: false, maxRating: null }
const admin = { ...adult, id: 1, name: 'Папа', isAdmin: true, addons: true }
const kid = { ...adult, id: 3, name: 'Вася', isKid: true }

const json = (b: unknown) => new Response(JSON.stringify(b), { status: 200, headers: { 'Content-Type': 'application/json' } })

const movie = {
  id: 9, kind: 'movie', title: 'Дюна', subtitle: '', poster: '', backdrop: '', logo: '', progress: 0, onServer: true,
  overview: '', genres: [], cast: [], seasons: [], files: [{ id: 5, name: 'dune.mkv', size: 1 << 30, audio: [], subs: [] }],
  inWatchlist: false, actions: { play: true, toServer: false, toDevice: false, watchNow: false },
} as unknown as Detail

function stub(user: object, web: unknown[] = []) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (url.endsWith('/api/login')) return json(user)
      if (url.endsWith('/api/addons/web')) return json(web)
      if (url.includes('/api/items/9')) return json(movie)
      return json({})
    }),
  )
}

async function signIn(user: { id: number; addons?: boolean }, web?: unknown[]) {
  stub(user, web)
  await session.login(user.id)
}

function renderManage() {
  render(
    <MemoryRouter initialEntries={['/manage/files']}>
      <Routes>
        <Route path="/manage" element={<Manage />}>
          <Route path="files" element={<p>files section</p>} />
        </Route>
        <Route path="/addons/*" element={<Outlet />} />
      </Routes>
    </MemoryRouter>,
  )
}

function renderItem() {
  render(
    <MemoryRouter initialEntries={['/item/9']}>
      <Routes>
        <Route path="/item/:id" element={<ItemDetail />} />
      </Routes>
    </MemoryRouter>,
  )
}

describe('addon slots', () => {
  beforeEach(() => {
    session.reset()
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    vi.spyOn(console, 'error').mockImplementation(() => {})
  })
  afterEach(() => {
    vi.unstubAllGlobals()
    session.reset()
    resetAddonsForTests()
    resetLoaderForTests()
    globalThis.__antresol = undefined
  })

  it('«Управление» has «Расширения» and the addon\'s tile for an admin', async () => {
    await signIn(admin)
    registerAddon('hello', { manageTiles: [{ label: 'Привет-панель', icon: 'star', to: '/addons/hello/panel' }] })
    setActiveAddons(['hello'])
    renderManage()
    expect(await screen.findByRole('link', { name: 'Расширения' })).toHaveAttribute('href', '/manage/addons')
    expect(screen.getByRole('link', { name: 'Привет-панель' })).toHaveAttribute('href', '/addons/hello/panel')
  })

  it('a server with addons off has no «Расширения»', async () => {
    await signIn({ ...admin, addons: false })
    renderManage()
    await screen.findByText('files section')
    expect(screen.queryByRole('link', { name: 'Расширения' })).toBeNull()
  })

  it('a kid gets no addon tile', async () => {
    await signIn(kid)
    registerAddon('hello', { manageTiles: [{ label: 'Привет-панель', icon: 'star', to: '/addons/hello/panel' }] })
    setActiveAddons(['hello'])
    renderManage()
    await screen.findByText('files section')
    expect(screen.queryByRole('link', { name: 'Привет-панель' })).toBeNull()
  })

  it('a tile that throws leaves a notice in its place, the rest stays', async () => {
    await signIn(admin)
    // an object is no React child: drawing the label throws
    registerAddon('bad', { manageTiles: [{ label: { boom: 1 } as unknown as string, icon: 'star', to: '/addons/bad/' }] })
    registerAddon('hello', { manageTiles: [{ label: 'Привет-панель', icon: 'star', to: '/addons/hello/panel' }] })
    setActiveAddons(['bad', 'hello'])
    renderManage()
    expect(await screen.findByRole('link', { name: 'Привет-панель' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Расширения' })).toBeInTheDocument()
    expect(screen.getByText('Расширение не смогло показать этот раздел')).toBeInTheDocument()
  })

  it('an item action shows on the item page and runs with the item', async () => {
    await signIn(admin)
    const run = vi.fn()
    registerAddon('hello', { itemActions: [{ label: 'Сказать привет', icon: 'star', run }] })
    setActiveAddons(['hello'])
    renderItem()
    await screen.findByRole('heading', { name: 'Дюна' })
    await userEvent.click(screen.getByRole('button', { name: 'Сказать привет' }))
    expect(run).toHaveBeenCalledWith(expect.objectContaining({ id: 9, title: 'Дюна' }), expect.any(Function))
  })

  it('the text an item action returns shows in a notice under the row', async () => {
    await signIn(admin)
    registerAddon('hello', { itemActions: [{ label: 'Сказать привет', icon: 'star', run: async (it) => `Привет: ${it.title}` }] })
    setActiveAddons(['hello'])
    renderItem()
    await userEvent.click(await screen.findByRole('button', { name: 'Сказать привет' }))
    expect(await screen.findByRole('status')).toHaveTextContent('Привет: Дюна')
  })

  it('the notice and the problem of an action go when the item changes', async () => {
    await signIn(admin)
    registerAddon('hello', {
      itemActions: [
        { label: 'Сказать привет', icon: 'star', run: async (it) => `Привет: ${it.title}` },
        { label: 'Сломаться', icon: 'star', run: async () => { throw new Error('сломалось') } },
      ],
    })
    setActiveAddons(['hello'])
    const { rerender } = render(<ActionBar item={movie} onPlay={() => {}} />)
    await userEvent.click(await screen.findByRole('button', { name: 'Сказать привет' }))
    expect(await screen.findByText('Привет: Дюна')).toBeInTheDocument()
    rerender(<ActionBar item={{ ...movie, id: 10, title: 'Солярис' }} onPlay={() => {}} />)
    await waitFor(() => expect(screen.queryByText('Привет: Дюна')).toBeNull())
    await userEvent.click(screen.getByRole('button', { name: 'Сломаться' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Расширение не смогло выполнить действие')
    rerender(<ActionBar item={{ ...movie, id: 11, title: 'Сталкер' }} onPlay={() => {}} />)
    await waitFor(() => expect(screen.queryByRole('alert')).toBeNull())
  })

  it('an item action is hidden when its visible() says no, and for kids', async () => {
    await signIn(admin)
    registerAddon('hello', {
      itemActions: [
        { label: 'Только сериалы', icon: 'star', visible: (it) => it.kind === 'series', run: () => {} },
        { label: 'Сказать привет', icon: 'star', run: () => {} },
      ],
    })
    setActiveAddons(['hello'])
    renderItem()
    await screen.findByRole('button', { name: 'Сказать привет' })
    expect(screen.queryByRole('button', { name: 'Только сериалы' })).toBeNull()
    cleanup()
    session.reset()
    await signIn(kid)
    renderItem()
    await screen.findByRole('heading', { name: 'Дюна' })
    expect(screen.queryByRole('button', { name: 'Сказать привет' })).toBeNull()
  })

  it('an adult gets the actions of their addons, except the admin: true ones', async () => {
    await signIn(adult)
    setAddonUser({ isAdmin: false })
    registerAddon('fam', {
      itemActions: [
        { label: 'Сказать привет', icon: 'star', run: () => {} },
        { label: 'Админское', icon: 'star', admin: true, run: () => {} },
      ],
    })
    setActiveAddons(['fam'])
    renderItem()
    expect(await screen.findByRole('button', { name: 'Сказать привет' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Админское' })).toBeNull()
  })

  it('an item action that throws does not break the item page', async () => {
    await signIn(admin)
    registerAddon('bad', {
      itemActions: [
        {
          label: 'Сломано',
          icon: 'star',
          visible: () => {
            throw new Error('boom')
          },
          run: () => {},
        },
      ],
    })
    registerAddon('hello', { itemActions: [{ label: 'Сказать привет', icon: 'star', run: () => {} }] })
    setActiveAddons(['bad', 'hello'])
    renderItem()
    expect(await screen.findByRole('heading', { name: 'Дюна' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Сказать привет' })).toBeInTheDocument()
    expect(screen.getByText('Расширение не смогло показать этот раздел')).toBeInTheDocument()
  })

  it('an element an item action returns is drawn as its sheet until close()', async () => {
    await signIn(admin)
    registerAddon('hello', {
      itemActions: [
        {
          label: 'Открыть лист',
          icon: 'star',
          run: (it, close) => (
            <div role="dialog" aria-label="Лист расширения">
              <p>Про {it.title}</p>
              <button onClick={close}>Закрыть лист</button>
            </div>
          ),
        },
      ],
    })
    setActiveAddons(['hello'])
    const { rerender } = render(<ActionBar item={movie} onPlay={() => {}} />)
    await userEvent.click(await screen.findByRole('button', { name: 'Открыть лист' }))
    const sheet = screen.getByRole('dialog', { name: 'Лист расширения' })
    expect(sheet).toHaveTextContent('Про Дюна')
    expect(screen.queryByRole('status')).toBeNull()
    await userEvent.click(screen.getByRole('button', { name: 'Закрыть лист' }))
    expect(screen.queryByRole('dialog', { name: 'Лист расширения' })).toBeNull()
    // open again, then another item: the sheet goes with its item
    await userEvent.click(screen.getByRole('button', { name: 'Открыть лист' }))
    expect(screen.getByRole('dialog', { name: 'Лист расширения' })).toBeInTheDocument()
    rerender(<ActionBar item={{ ...movie, id: 10, title: 'Солярис' }} onPlay={() => {}} />)
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Лист расширения' })).toBeNull())
  })

  it('a second sheet replaces the first, and the first one\'s close() does nothing', async () => {
    await signIn(admin)
    let closeFirst = () => {}
    registerAddon('hello', {
      itemActions: [
        {
          label: 'Первый лист',
          icon: 'star',
          run: (_it, close) => {
            closeFirst = close
            return <div role="dialog" aria-label="Первый" />
          },
        },
        { label: 'Второй лист', icon: 'star', run: () => <div role="dialog" aria-label="Второй" /> },
      ],
    })
    setActiveAddons(['hello'])
    render(<ActionBar item={movie} onPlay={() => {}} />)
    await userEvent.click(await screen.findByRole('button', { name: 'Первый лист' }))
    expect(screen.getByRole('dialog', { name: 'Первый' })).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Второй лист' }))
    expect(screen.queryByRole('dialog', { name: 'Первый' })).toBeNull()
    expect(screen.getByRole('dialog', { name: 'Второй' })).toBeInTheDocument()
    act(() => closeFirst())
    expect(screen.getByRole('dialog', { name: 'Второй' })).toBeInTheDocument()
  })

  it('a sheet that throws goes, and the row says the action failed', async () => {
    await signIn(admin)
    function Broken(): never {
      throw new Error('boom')
    }
    registerAddon('bad', { itemActions: [{ label: 'Сломанный лист', icon: 'star', run: () => <Broken /> }] })
    setActiveAddons(['bad'])
    render(<ActionBar item={movie} onPlay={() => {}} />)
    await userEvent.click(await screen.findByRole('button', { name: 'Сломанный лист' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Расширение не смогло выполнить действие')
    // the button stays: the next press opens it again
    expect(screen.getByRole('button', { name: 'Сломанный лист' })).toBeEnabled()
  })

  it('a watchWay entry is no round button (it is a way of «Смотреть»)', async () => {
    await signIn(admin)
    registerAddon('hello', {
      itemActions: [
        { label: 'Смотреть так', icon: 'play', watchWay: true, run: () => {} },
        { label: 'Сказать привет', icon: 'star', run: () => {} },
      ],
    })
    setActiveAddons(['hello'])
    render(<ActionBar item={movie} onPlay={() => {}} />)
    expect(await screen.findByRole('button', { name: 'Сказать привет' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Смотреть так' })).toBeNull()
  })

  it('/addons/hello/page renders the addon\'s route in the app', async () => {
    await signIn(admin, [{ id: 'hello', entry: '/addons/hello/ui/index.js' }])
    vi.spyOn(importer, 'load').mockImplementation(async () => {
      registerAddon('hello', { routes: [{ path: 'page', title: 'Привет', element: () => <h1>Страница привета</h1> }] })
      return {}
    })
    const router = createMemoryRouter(routes, { initialEntries: ['/addons/hello/page'] })
    render(<RouterProvider router={router} />)
    expect(await screen.findByRole('heading', { name: 'Страница привета' })).toBeInTheDocument()
  })

  it('a route that throws shows a notice instead of the page', async () => {
    await signIn(admin, [{ id: 'hello', entry: '/addons/hello/ui/index.js' }])
    vi.spyOn(importer, 'load').mockImplementation(async () => {
      registerAddon('hello', {
        routes: [
          {
            path: 'page',
            title: 'Привет',
            element: () => {
              throw new Error('boom')
            },
          },
        ],
      })
      return {}
    })
    const router = createMemoryRouter(routes, { initialEntries: ['/addons/hello/page'] })
    render(<RouterProvider router={router} />)
    expect(await screen.findByText('Расширение не смогло показать этот раздел')).toBeInTheDocument()
  })

  it('/addons/fam/page renders for an adult the server lists the addon for', async () => {
    await signIn(adult, [{ id: 'fam', entry: '/addons/fam/ui/index.js' }])
    vi.spyOn(importer, 'load').mockImplementation(async () => {
      registerAddon('fam', { routes: [{ path: 'page', title: 'Семья', element: () => <h1>Семейная страница</h1> }] })
      return {}
    })
    const router = createMemoryRouter(routes, { initialEntries: ['/addons/fam/page'] })
    render(<RouterProvider router={router} />)
    expect(await screen.findByRole('heading', { name: 'Семейная страница' })).toBeInTheDocument()
  })

  it('an admin: true route is not there for an adult', async () => {
    await signIn(adult, [{ id: 'fam', entry: '/addons/fam/ui/index.js' }])
    const load = vi.spyOn(importer, 'load').mockImplementation(async () => {
      registerAddon('fam', { routes: [{ path: 'admin', title: 'Админка', admin: true, element: () => <h1>Админка</h1> }] })
      return {}
    })
    const router = createMemoryRouter(routes, { initialEntries: ['/addons/fam/admin'] })
    render(<RouterProvider router={router} />)
    await waitFor(() => expect(load).toHaveBeenCalled())
    await waitFor(() => expect(screen.getByText('Такого раздела у расширения нет')).toBeInTheDocument())
    expect(screen.queryByRole('heading', { name: 'Админка' })).toBeNull()
  })

  it('a kid is sent away from an addon page', async () => {
    await signIn(kid, [{ id: 'hello', entry: '/addons/hello/ui/index.js' }])
    vi.spyOn(importer, 'load').mockImplementation(async () => {
      registerAddon('hello', { routes: [{ path: 'page', title: 'Привет', element: () => <h1>Страница привета</h1> }] })
      return {}
    })
    const router = createMemoryRouter(routes, { initialEntries: ['/addons/hello/page'] })
    render(<RouterProvider router={router} />)
    await waitFor(() => expect(router.state.location.pathname).toBe('/'))
    expect(screen.queryByRole('heading', { name: 'Страница привета' })).toBeNull()
  })
})

describe('addon labels are keys of the addon\'s strings', () => {
  const strings = { ru: { tile: 'Привет-панель', act: 'Сказать привет', page: 'Привет' }, en: { tile: 'Hello panel', act: 'Say hello', page: 'Hello' } }
  beforeEach(() => {
    session.reset()
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    vi.spyOn(console, 'error').mockImplementation(() => {})
  })
  afterEach(() => {
    setLanguagePref('system')
    vi.unstubAllGlobals()
    session.reset()
    resetAddonsForTests()
    resetLoaderForTests()
    globalThis.__antresol = undefined
  })

  it('Manage: a label is resolved from the addon\'s strings and follows a language switch', async () => {
    await signIn(admin)
    registerAddon('hello', { manageTiles: [{ label: 'tile', icon: 'star', to: 'panel' }], strings })
    setActiveAddons(['hello'])
    renderManage()
    expect(await screen.findByRole('link', { name: 'Привет-панель' })).toHaveAttribute('href', '/addons/hello/panel')
    await act(async () => setLanguagePref('en'))
    expect(await screen.findByRole('link', { name: 'Hello panel' })).toBeInTheDocument()
  })

  it('Manage: an unknown key shows as itself', async () => {
    await signIn(admin)
    registerAddon('hello', { manageTiles: [{ label: 'Просто текст', icon: 'star', to: 'panel' }], strings })
    setActiveAddons(['hello'])
    renderManage()
    expect(await screen.findByRole('link', { name: 'Просто текст' })).toBeInTheDocument()
  })

  it('AddonItemActions: a label is resolved from the addon\'s strings and follows a language switch', async () => {
    await signIn(admin)
    registerAddon('hello', { itemActions: [{ label: 'act', icon: 'star', run: () => {} }], strings })
    setActiveAddons(['hello'])
    render(<ActionBar item={movie} onPlay={() => {}} />)
    expect(await screen.findByRole('button', { name: 'Сказать привет' })).toBeInTheDocument()
    await act(async () => setLanguagePref('en'))
    expect(await screen.findByRole('button', { name: 'Say hello' })).toBeInTheDocument()
  })

  it('AddonItemActions: an unknown key shows as itself', async () => {
    await signIn(admin)
    registerAddon('hello', { itemActions: [{ label: 'Без ключа', icon: 'star', run: () => {} }], strings })
    setActiveAddons(['hello'])
    render(<ActionBar item={movie} onPlay={() => {}} />)
    expect(await screen.findByRole('button', { name: 'Без ключа' })).toBeInTheDocument()
  })

  async function renderPage(title: string) {
    await signIn(admin, [{ id: 'hello', entry: '/addons/hello/ui/index.js' }])
    vi.spyOn(importer, 'load').mockImplementation(async () => {
      registerAddon('hello', { routes: [{ path: 'page', title, element: () => <h1>Страница привета</h1> }], strings })
      return {}
    })
    const router = createMemoryRouter(routes, { initialEntries: ['/addons/hello/page'] })
    render(<RouterProvider router={router} />)
    await screen.findByRole('heading', { name: 'Страница привета' })
  }

  it('AddonPage: a label is resolved from the addon\'s strings and follows a language switch', async () => {
    await renderPage('page')
    await waitFor(() => expect(document.title).toMatch(/^Привет — /))
    await act(async () => setLanguagePref('en'))
    await waitFor(() => expect(document.title).toMatch(/^Hello — /))
  })

  it('AddonPage: an unknown key shows as itself', async () => {
    await renderPage('Своя страница')
    await waitFor(() => expect(document.title).toMatch(/^Своя страница — /))
  })
})

describe('an addon page with :name segments in its path', () => {
  // what an addon bundle gets: the app's own react-router, through the shared map
  const sharedUseParams = () => (globalThis.__antresol!.shared['react-router'] as { useParams: () => Record<string, string | undefined> }).useParams

  beforeEach(() => {
    session.reset()
    installShared()
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    vi.spyOn(console, 'error').mockImplementation(() => {})
  })
  afterEach(() => {
    vi.unstubAllGlobals()
    session.reset()
    resetAddonsForTests()
    resetLoaderForTests()
    globalThis.__antresol = undefined
  })

  // how an addon written before `params` existed reads its values: useParams() of the shared react-router, no props
  function ParamsPage({ params }: { params?: Readonly<Record<string, string>> }) {
    const useParams = sharedUseParams()
    const p = useParams()
    return (
      <>
        <h1>Страница с параметрами</h1>
        <p data-testid="hook">{JSON.stringify(p)}</p>
        <p data-testid="prop">{JSON.stringify(params)}</p>
      </>
    )
  }

  async function open(path: string, adminOnly = true) {
    await signIn(admin, [{ id: 'hello', entry: '/addons/hello/ui/index.js' }])
    vi.spyOn(importer, 'load').mockImplementation(async () => {
      registerAddon('hello', {
        routes: [
          { path: 'sources/add', admin: adminOnly, title: 'Привет', element: () => <h1>Каталог</h1> },
          { path: 'sources/add/:id', admin: adminOnly, title: 'Привет', element: (props) => <ParamsPage {...props} /> },
          { path: 'sources/:sourceId/edit', admin: adminOnly, title: 'Привет', element: (props) => <ParamsPage {...props} /> },
        ],
      })
      return {}
    })
    const router = createMemoryRouter(routes, { initialEntries: [path] })
    render(<RouterProvider router={router} />)
    return router
  }

  it('/addons/hello/sources/add/alpha draws the page; the hook and the prop both give id', async () => {
    await open('/addons/hello/sources/add/alpha')
    expect(await screen.findByRole('heading', { name: 'Страница с параметрами' })).toBeInTheDocument()
    expect(JSON.parse(screen.getByTestId('hook').textContent!)).toMatchObject({ id: 'alpha' })
    expect(JSON.parse(screen.getByTestId('prop').textContent!)).toEqual({ id: 'alpha' })
  })

  it('/addons/hello/sources/7/edit: sourceId comes through useParams() unchanged', async () => {
    await open('/addons/hello/sources/7/edit')
    expect(await screen.findByRole('heading', { name: 'Страница с параметрами' })).toBeInTheDocument()
    expect(JSON.parse(screen.getByTestId('hook').textContent!)).toMatchObject({ sourceId: '7' })
    expect(JSON.parse(screen.getByTestId('prop').textContent!)).toEqual({ sourceId: '7' })
  })

  it('the core route\'s own params stay beside the page\'s', async () => {
    await open('/addons/hello/sources/add/alpha')
    await screen.findByRole('heading', { name: 'Страница с параметрами' })
    expect(JSON.parse(screen.getByTestId('hook').textContent!)).toEqual({ addonId: 'hello', '*': 'sources/add/alpha', id: 'alpha' })
  })

  it('a plain route of the same addon still draws, with no params in its prop', async () => {
    await open('/addons/hello/sources/add')
    expect(await screen.findByRole('heading', { name: 'Каталог' })).toBeInTheDocument()
  })

  it('a path that fits no route is «not here»', async () => {
    await open('/addons/hello/sources/add/alpha/extra')
    await waitFor(() => expect(screen.getByText('Такого раздела у расширения нет')).toBeInTheDocument())
    expect(screen.queryByRole('heading', { name: 'Страница с параметрами' })).toBeNull()
  })

  it('an admin: true param route is not there for an adult', async () => {
    await signIn(adult, [{ id: 'hello', entry: '/addons/hello/ui/index.js' }])
    vi.spyOn(importer, 'load').mockImplementation(async () => {
      registerAddon('hello', { routes: [{ path: 'sources/add/:id', admin: true, title: 'Привет', element: (props) => <ParamsPage {...props} /> }] })
      return {}
    })
    const router = createMemoryRouter(routes, { initialEntries: ['/addons/hello/sources/add/alpha'] })
    render(<RouterProvider router={router} />)
    await waitFor(() => expect(screen.getByText('Такого раздела у расширения нет')).toBeInTheDocument())
    expect(screen.queryByRole('heading', { name: 'Страница с параметрами' })).toBeNull()
  })

  it('going from one value to another (a link in the page) gives the page the new one', async () => {
    const router = await open('/addons/hello/sources/add/alpha')
    await screen.findByRole('heading', { name: 'Страница с параметрами' })
    await act(async () => {
      await router.navigate('/addons/hello/sources/add/beta')
    })
    await waitFor(() => expect(JSON.parse(screen.getByTestId('hook').textContent!)).toMatchObject({ id: 'beta' }))
    expect(JSON.parse(screen.getByTestId('prop').textContent!)).toEqual({ id: 'beta' })
    // and to the other route's shape: no leftover id
    await act(async () => {
      await router.navigate('/addons/hello/sources/9/edit')
    })
    await waitFor(() => expect(JSON.parse(screen.getByTestId('hook').textContent!)).toMatchObject({ sourceId: '9' }))
    expect(JSON.parse(screen.getByTestId('hook').textContent!)).not.toHaveProperty('id')
  })

  it('/Addons/... (the app\'s own route to addon pages is case-insensitive) draws the same page', async () => {
    await open('/Addons/hello/sources/add/alpha')
    expect(await screen.findByRole('heading', { name: 'Страница с параметрами' })).toBeInTheDocument()
    expect(JSON.parse(screen.getByTestId('prop').textContent!)).toEqual({ id: 'alpha' })
    expect(JSON.parse(screen.getByTestId('hook').textContent!)).toMatchObject({ id: 'alpha' })
  })

  it('an addon\'s own * (a splat route) is what its useParams() gives, over the app route\'s', async () => {
    await signIn(admin, [{ id: 'hello', entry: '/addons/hello/ui/index.js' }])
    vi.spyOn(importer, 'load').mockImplementation(async () => {
      registerAddon('hello', { routes: [{ path: 'files/*', title: 'Привет', element: (props) => <ParamsPage {...props} /> }] })
      return {}
    })
    render(<RouterProvider router={createMemoryRouter(routes, { initialEntries: ['/addons/hello/files/x/y'] })} />)
    await screen.findByRole('heading', { name: 'Страница с параметрами' })
    // the app's own * would be files/x/y
    expect(JSON.parse(screen.getByTestId('hook').textContent!)).toEqual({ addonId: 'hello', '*': 'x/y' })
    expect(JSON.parse(screen.getByTestId('prop').textContent!)).toEqual({ '*': 'x/y' })
  })

  it('another value of the same route starts the page afresh: its local state resets', async () => {
    function Counter({ params }: { params: Readonly<Record<string, string>> }) {
      const [n, setN] = useState(0)
      return (
        <>
          <p>для {params.id}</p>
          <button onClick={() => setN(n + 1)}>нажато {n}</button>
        </>
      )
    }
    await signIn(admin, [{ id: 'hello', entry: '/addons/hello/ui/index.js' }])
    vi.spyOn(importer, 'load').mockImplementation(async () => {
      registerAddon('hello', { routes: [{ path: 'sources/add/:id', title: 'Привет', element: (props) => <Counter {...props} /> }] })
      return {}
    })
    const router = createMemoryRouter(routes, { initialEntries: ['/addons/hello/sources/add/1'] })
    render(<RouterProvider router={router} />)
    await userEvent.click(await screen.findByRole('button', { name: 'нажато 0' }))
    expect(await screen.findByRole('button', { name: 'нажато 1' })).toBeInTheDocument()
    expect(screen.getByText('для 1')).toBeInTheDocument()
    await act(async () => {
      await router.navigate('/addons/hello/sources/add/2')
    })
    expect(await screen.findByText('для 2')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'нажато 0' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'нажато 1' })).toBeNull()
  })

  it('outside an addon page the shared useParams is the router\'s own', () => {
    function Probe() {
      const useParams = sharedUseParams()
      return <p>{JSON.stringify(useParams())}</p>
    }
    render(
      <MemoryRouter initialEntries={['/title/5']}>
        <Routes>
          <Route path="/title/:titleId" element={<Probe />} />
        </Routes>
      </MemoryRouter>,
    )
    expect(screen.getByRole('paragraph').textContent).toBe(JSON.stringify({ titleId: '5' }))
  })

  it('the params are the same object between renders of one page (an effect may depend on it)', async () => {
    const seen: unknown[] = []
    function Spy() {
      const useParams = sharedUseParams()
      seen.push(useParams())
      const [n, setN] = useState(0)
      return <button onClick={() => setN(n + 1)}>again {n}</button>
    }
    await signIn(admin, [{ id: 'hello', entry: '/addons/hello/ui/index.js' }])
    vi.spyOn(importer, 'load').mockImplementation(async () => {
      registerAddon('hello', { routes: [{ path: 'x/:id', title: 'Привет', element: () => <Spy /> }] })
      return {}
    })
    render(<RouterProvider router={createMemoryRouter(routes, { initialEntries: ['/addons/hello/x/1'] })} />)
    await userEvent.click(await screen.findByRole('button', { name: 'again 0' }))
    await screen.findByRole('button', { name: 'again 1' })
    expect(seen.length).toBeGreaterThan(1)
    expect(new Set(seen).size).toBe(1)
  })
})
