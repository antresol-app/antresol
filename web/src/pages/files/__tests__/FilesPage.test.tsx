import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { RouterProvider, createMemoryRouter } from 'react-router'
import type { FileEntry, FileListing, User } from '../../../api/types'
import { session } from '../../../state/session'
import { FilesPage } from '../FilesPage'
import { resetDirs } from '../useDir'

const admin: User = { id: 1, name: 'Александр', avatar: 'grad-1', hasPassword: true, isAdmin: true, isKid: false, maxRating: null }
const member: User = { ...admin, id: 2, name: 'Маша', isAdmin: false }

const entry = (name: string, over: Partial<FileEntry> = {}): FileEntry => ({
  name,
  path: name,
  isDir: false,
  size: 0,
  mtime: '2026-10-20T10:00:00Z',
  kind: 'other',
  ...over,
})

const LISTING: FileListing = {
  root: 'movies',
  path: '',
  breadcrumbs: [{ name: 'Фильмы', path: '' }],
  entries: [
    entry('Интерстеллар (2014)', { isDir: true, kind: 'folder', childCount: 3, item: { id: 7, kind: 'movie', title: 'Интерстеллар' } }),
    entry('Брат 2.mkv', { size: 3_000_000_000, kind: 'video' }),
    entry('Брат 2.ru.srt', { size: 40_000, kind: 'subtitle' }),
    entry('Брат 2.nfo', { size: 900, kind: 'text' }),
  ],
}

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

function api(user: User) {
  return vi.fn(async (input: RequestInfo | URL) => {
    const url = new URL(String(input), 'http://vault.test')
    switch (url.pathname) {
      case '/api/login':
        return json(user)
      case '/api/files/roots':
        return json([
          { id: 'movies', title: 'Фильмы', icon: 'film' },
          { id: 'series', title: 'Сериалы', icon: 'tv' },
          { id: 'downloads', title: 'Загрузки', icon: 'download' },
        ])
      case '/api/files/list':
        if (url.searchParams.get('path') === 'Интерстеллар (2014)')
          return json({
            ...LISTING,
            path: 'Интерстеллар (2014)',
            entries: [entry('Interstellar.2014.mkv', { path: 'Интерстеллар (2014)/Interstellar.2014.mkv', size: 9e9, kind: 'video' })],
          })
        return json(LISTING)
      case '/api/files/usage':
        return json({ total: 4e12, used: 3e12, free: 1e12, categories: [{ id: 'movies', bytes: 2e12 }] })
      case '/api/files/stat': {
        const p = url.searchParams.get('path') ?? ''
        const e = LISTING.entries.find((x) => x.path === p) ?? LISTING.entries[0]!
        return json(e)
      }
      case '/api/files/content':
        return new Response('1\n00:00:01,000 --> 00:00:02,000\nПривет\n', { headers: { 'Content-Type': 'text/plain' } })
      default:
        return json({ error: 'Не найдено' }, 404)
    }
  })
}

async function open(user: User, path = '/files/movies') {
  vi.stubGlobal('fetch', api(user))
  await session.login(user.id, 'pw')
  const router = createMemoryRouter([{ path: '/files/*', element: <FilesPage /> }, { path: '*', element: <p>другая страница</p> }], {
    initialEntries: [path],
  })
  render(<RouterProvider router={router} />)
  await screen.findByRole('option', { name: /Брат 2\.mkv/ })
  return router
}

const option = (name: RegExp) => screen.getByRole('option', { name })

describe('Файлы', () => {
  beforeEach(() => {
    session.reset()
    resetDirs()
    try {
      localStorage.clear()
    } catch {
      /* ignore */
    }
  })
  afterEach(() => vi.unstubAllGlobals())

  it('Shift-click selects a range, ⌘-click adds one item', async () => {
    await open(admin)
    fireEvent.click(option(/Интерстеллар/))
    fireEvent.click(option(/Брат 2\.ru\.srt/), { shiftKey: true })
    const selected = () =>
      screen
        .getAllByRole('option')
        .filter((o) => o.getAttribute('aria-selected') === 'true')
        .map((o) => o.getAttribute('data-name'))
    // Folders come first, then natural name order.
    expect(selected()).toEqual(['Интерстеллар (2014)', 'Брат 2.mkv', 'Брат 2.nfo', 'Брат 2.ru.srt'])
    fireEvent.click(option(/Брат 2\.mkv/), { metaKey: true })
    expect(selected()).toEqual(['Интерстеллар (2014)', 'Брат 2.nfo', 'Брат 2.ru.srt'])
    expect(screen.getByText(/Выбрано 3 из 4/)).toBeInTheDocument()
  })

  it('Space opens Quick Look on the selected file, Esc closes it, ← → page through files', async () => {
    const u = userEvent.setup()
    await open(admin)
    await u.click(option(/Брат 2\.ru\.srt/))
    await u.keyboard(' ')
    const ql = await screen.findByRole('dialog', { name: /Быстрый просмотр/ })
    expect(within(ql).getByText('Брат 2.ru.srt')).toBeInTheDocument()
    expect(await within(ql).findByText(/Привет/)).toBeInTheDocument()
    await u.keyboard('{ArrowLeft}')
    expect(within(ql).getByText('Брат 2.nfo')).toBeInTheDocument()
    await u.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('dialog', { name: /Быстрый просмотр/ })).not.toBeInTheDocument())
  })

  it('list view sorts by a column header click and shows the sort direction', async () => {
    const u = userEvent.setup()
    await open(admin)
    await u.click(screen.getByRole('radio', { name: 'Список' }))
    const rows = () => screen.getAllByRole('row').slice(1).map((r) => r.getAttribute('data-name'))
    expect(rows()).toEqual(['Интерстеллар (2014)', 'Брат 2.mkv', 'Брат 2.nfo', 'Брат 2.ru.srt'])
    const size = screen.getByRole('columnheader', { name: /Размер/ })
    await u.click(within(size).getByRole('button'))
    expect(size).toHaveAttribute('aria-sort', 'descending')
    expect(rows()).toEqual(['Интерстеллар (2014)', 'Брат 2.mkv', 'Брат 2.ru.srt', 'Брат 2.nfo'])
    await u.click(within(size).getByRole('button'))
    expect(size).toHaveAttribute('aria-sort', 'ascending')
    expect(rows()).toEqual(['Интерстеллар (2014)', 'Брат 2.nfo', 'Брат 2.ru.srt', 'Брат 2.mkv'])
    expect(screen.getByRole('columnheader', { name: /Имя/ })).toHaveAttribute('aria-sort', 'none')
  })

  it('→ on a folder in the list opens it in place (disclosure triangle), ← closes it', async () => {
    const u = userEvent.setup()
    await open(admin)
    await u.click(screen.getByRole('radio', { name: 'Список' }))
    await u.click(screen.getByRole('row', { name: /Интерстеллар/ }))
    await u.keyboard('{ArrowRight}')
    const child = await screen.findByRole('row', { name: 'Interstellar.2014.mkv' })
    expect(child).toHaveAttribute('aria-level', '2')
    expect(screen.getByRole('row', { name: 'Интерстеллар (2014)' })).toHaveAttribute('aria-expanded', 'true')
    await u.keyboard('{ArrowLeft}')
    await waitFor(() => expect(screen.queryByRole('row', { name: 'Interstellar.2014.mkv' })).not.toBeInTheDocument())
  })

  it('arrow keys move the selection and Shift+arrow extends it', async () => {
    const u = userEvent.setup()
    await open(admin)
    await u.click(screen.getByRole('radio', { name: 'Список' }))
    await u.click(screen.getByRole('row', { name: /Брат 2\.mkv/ }))
    await u.keyboard('{Shift>}{ArrowDown}{ArrowDown}{/Shift}')
    const selected = screen.getAllByRole('row').filter((r) => r.getAttribute('aria-selected') === 'true')
    expect(selected.map((r) => r.getAttribute('data-name'))).toEqual(['Брат 2.mkv', 'Брат 2.nfo', 'Брат 2.ru.srt'])
  })

  it('a regular profile sees no «Переименовать» and no «Корзина»; the admin does', async () => {
    await open(member)
    fireEvent.contextMenu(option(/Интерстеллар/))
    const menu = await screen.findByRole('menu')
    expect(within(menu).getByRole('menuitem', { name: /Открыть/ })).toBeInTheDocument()
    expect(within(menu).queryByRole('menuitem', { name: /Переименовать/ })).not.toBeInTheDocument()
    expect(within(menu).queryByRole('menuitem', { name: /в корзину/i })).not.toBeInTheDocument()
    expect(screen.queryByRole('link', { name: 'Корзина' })).not.toBeInTheDocument()
  })

  it('the admin context menu offers rename and trash; ⌘I opens Свойства with the library link', async () => {
    const u = userEvent.setup()
    await open(admin)
    fireEvent.contextMenu(option(/Интерстеллар/))
    const menu = await screen.findByRole('menu')
    expect(within(menu).getByRole('menuitem', { name: /Переименовать/ })).toBeInTheDocument()
    expect(within(menu).getByRole('menuitem', { name: /Переместить в корзину/ })).toBeInTheDocument()
    await u.keyboard('{Escape}')
    await u.click(option(/Интерстеллар/))
    await u.keyboard('{Meta>}i{/Meta}')
    const info = await screen.findByRole('complementary', { name: 'Свойства' })
    expect(await within(info).findByRole('link', { name: /Фильм в медиатеке.*Интерстеллар/ })).toHaveAttribute('href', '/item/7')
    expect(screen.getByRole('link', { name: 'Корзина' })).toBeInTheDocument()
  })
})
