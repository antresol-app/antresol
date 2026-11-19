// Review round 1: keyboard scope, needsConfirm flows, partial moves,
// reported undo failures and kid gating (the 5000-item folder: listRows.test).
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, RouterProvider, createMemoryRouter } from 'react-router'
import { routes } from '../../../app/routes'
import { ProfileMenu } from '../../../components/ProfileMenu'
import { session } from '../../../state/session'
import { resetUsageCache } from '../StorageBar'
import { resetDirs } from '../useDir'
import { admin, entry, json, kid, listing, mockApi, renderFiles, type Call } from './harness'

const FOLDER = entry('Интерстеллар (2014)', { isDir: true, kind: 'folder', childCount: 1 })
const BASE = {
  'movies:': listing('movies', '', [
    FOLDER,
    entry('Брат 2.mkv', { size: 3e9, kind: 'video' }),
    entry('Брат 2.ru.srt', { size: 4e4, kind: 'subtitle' }),
  ]),
  'movies:Интерстеллар (2014)': listing('movies', 'Интерстеллар (2014)', [
    entry('Interstellar.mkv', { path: 'Интерстеллар (2014)/Interstellar.mkv', size: 9e9, kind: 'video' }),
  ]),
}
const posts = (calls: Call[], path: string) => calls.filter((c) => c.method === 'POST' && c.path === path).map((c) => c.body)

beforeEach(() => {
  session.reset()
  resetDirs()
  resetUsageCache()
  try {
    localStorage.clear()
  } catch {
    /* ignore */
  }
})
afterEach(() => vi.unstubAllGlobals())

describe('keyboard belongs to the file view', () => {
  it('Enter on «Назад» goes back instead of opening the selected item', async () => {
    const u = userEvent.setup()
    const router = await renderFiles(admin, mockApi(admin, BASE), '/files/movies', /Брат 2\.mkv/)
    fireEvent.doubleClick(screen.getByRole('option', { name: /Интерстеллар/ }))
    await screen.findByRole('option', { name: /Interstellar\.mkv/ })
    await u.click(screen.getByRole('option', { name: /Interstellar\.mkv/ }))
    screen.getByRole('button', { name: 'Назад' }).focus()
    await u.keyboard('{Enter}')
    await waitFor(() => expect(router.state.location.pathname).toBe('/files/movies'))
    expect(screen.queryByRole('dialog', { name: /Быстрый просмотр/ })).not.toBeInTheDocument()
  })

  it('Enter on a sort menu item changes the sort', async () => {
    const u = userEvent.setup()
    await renderFiles(admin, mockApi(admin, BASE), '/files/movies', /Брат 2\.mkv/)
    await u.click(screen.getByRole('radio', { name: 'Список' }))
    await u.click(screen.getByRole('row', { name: 'Брат 2.mkv' }))
    await u.click(screen.getByRole('button', { name: 'Сортировка' }))
    const menu = await screen.findByRole('menu', { name: 'Сортировка' })
    within(menu).getByRole('menuitemradio', { name: 'Размер' }).focus()
    await u.keyboard('{Enter}')
    await waitFor(() => expect(screen.getByRole('columnheader', { name: /Размер/ })).toHaveAttribute('aria-sort', 'ascending'))
    expect(screen.queryByRole('dialog', { name: /Быстрый просмотр/ })).not.toBeInTheDocument()
  })

  it('Space on a toolbar button does not open Quick Look', async () => {
    const u = userEvent.setup()
    await renderFiles(admin, mockApi(admin, BASE), '/files/movies', /Брат 2\.mkv/)
    await u.click(screen.getByRole('option', { name: /Брат 2\.ru\.srt/ }))
    screen.getByRole('button', { name: 'Свойства' }).focus()
    await u.keyboard(' ')
    expect(await screen.findByRole('complementary', { name: 'Свойства' })).toBeInTheDocument()
    expect(screen.queryByRole('dialog', { name: /Быстрый просмотр/ })).not.toBeInTheDocument()
  })
})

describe('needsConfirm: the server asks before touching files an extension holds', () => {
  it('rename → the warnings sheet names what stops → resent with confirm:true', async () => {
    const u = userEvent.setup()
    const mock = mockApi(admin, BASE, (c) =>
      c.path === '/api/files/rename'
        ? json(c.body?.confirm ? entry('Брат 2 (2000).mkv', { kind: 'video' }) : { needsConfirm: true, warnings: ['«Брат 2» перестанет обновляться'] })
        : undefined,
    )
    await renderFiles(admin, mock, '/files/movies', /Брат 2\.mkv/)
    fireEvent.contextMenu(screen.getByRole('option', { name: /Брат 2\.mkv/ }))
    await u.click(await screen.findByRole('menuitem', { name: /Переименовать/ }))
    const field = screen.getByRole('textbox', { name: 'Новое имя' })
    await u.clear(field)
    await u.type(field, 'Брат 2 (2000).mkv')
    await u.click(screen.getByRole('button', { name: 'Переименовать' }))
    const warn = await screen.findByRole('alertdialog', { name: 'Эти файлы используются' })
    expect(warn).toHaveTextContent('Эти файлы использует расширение. После этого оно перестанет с ними работать:')
    expect(within(warn).getByText('«Брат 2» перестанет обновляться')).toBeInTheDocument()
    await u.click(within(warn).getByRole('button', { name: 'Продолжить' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
    expect(posts(mock.calls, '/api/files/rename').map((b) => b?.confirm)).toEqual([false, true])
  })

  async function moveTwo(u: ReturnType<typeof userEvent.setup>, mock: ReturnType<typeof mockApi>) {
    await renderFiles(admin, mock, '/files/movies', /Брат 2\.mkv/)
    await u.click(screen.getByRole('option', { name: /Брат 2\.mkv/ }))
    fireEvent.click(screen.getByRole('option', { name: /Брат 2\.ru\.srt/ }), { metaKey: true })
    fireEvent.contextMenu(screen.getByRole('option', { name: /Брат 2\.ru\.srt/ }))
    await u.click(await screen.findByRole('menuitem', { name: /Переместить…/ }))
    const sheet = await screen.findByRole('dialog', { name: /Переместить 2 объекта/ })
    await u.click(await within(sheet).findByRole('option', { name: /Интерстеллар/ }))
    await u.click(within(sheet).getByRole('button', { name: 'Переместить' }))
  }

  it('move: a failure keeps the sheet open with «Перемещено N из M» and retries only the rest', async () => {
    const u = userEvent.setup()
    let failSrt = true
    const mock = mockApi(admin, BASE, (c) => {
      if (c.path !== '/api/files/move') return undefined
      if (c.body?.path === 'Брат 2.ru.srt' && failSrt) return json({ error: 'Объект с таким именем уже есть' }, 409)
      return json(entry(String(c.body?.path)))
    })
    await moveTwo(u, mock)
    const sheet = screen.getByRole('dialog', { name: /Переместить 2 объекта/ })
    expect(await within(sheet).findByRole('alert')).toHaveTextContent('Перемещено 1 из 2. «Брат 2.ru.srt»: Объект с таким именем уже есть')
    failSrt = false
    await u.click(within(sheet).getByRole('button', { name: 'Переместить' }))
    await waitFor(() => expect(screen.queryByRole('dialog', { name: /Переместить/ })).not.toBeInTheDocument())
    expect(posts(mock.calls, '/api/files/move').map((b) => b?.path)).toEqual(['Брат 2.mkv', 'Брат 2.ru.srt', 'Брат 2.ru.srt'])
  })

  it('the move picker never offers the stream cache', async () => {
    const u = userEvent.setup()
    await renderFiles(admin, mockApi(admin, BASE), '/files/movies', /Брат 2\.mkv/)
    fireEvent.contextMenu(screen.getByRole('option', { name: /Брат 2\.mkv/ }))
    await u.click(await screen.findByRole('menuitem', { name: /Переместить…/ }))
    const sheet = await screen.findByRole('dialog', { name: /Переместить/ })
    await u.click(within(sheet).getByRole('button', { name: 'Назад' }))
    await u.click(within(sheet).getByRole('button', { name: 'Назад' }))
    expect(within(sheet).getByRole('option', { name: 'Сериалы' })).toBeInTheDocument()
    expect(within(sheet).queryByRole('option', { name: 'Кеш просмотра' })).not.toBeInTheDocument()
  })

  it('a failed «Отменить» after trashing is reported', async () => {
    const u = userEvent.setup()
    const mock = mockApi(admin, BASE, (c) => {
      if (c.path === '/api/files/trash' && c.method === 'POST')
        return json({ trashed: [{ id: 9, root: 'movies', path: 'Брат 2.mkv', name: 'Брат 2.mkv', isDir: false, size: 1, deletedAt: '' }], warnings: [], needsConfirm: false })
      if (c.path === '/api/files/trash/restore') return json({ error: 'Объект с таким именем уже есть' }, 409)
      return undefined
    })
    await renderFiles(admin, mock, '/files/movies', /Брат 2\.mkv/)
    await u.click(screen.getByRole('option', { name: /Брат 2\.mkv/ }))
    await u.keyboard('{Meta>}{Backspace}{/Meta}')
    await u.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'В корзину' }))
    await u.click(within(await screen.findByRole('status')).getByRole('button', { name: 'Отменить' }))
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('Не удалось восстановить «Брат 2.mkv»: Объект с таким именем уже есть'))
  })
})

describe('the toast', () => {
  it('«Отменить» after trashing is the design system’s Toast, stacked clear of the tab bar and the player bar', async () => {
    const u = userEvent.setup()
    const mock = mockApi(admin, BASE, (c) =>
      c.path === '/api/files/trash' && c.method === 'POST'
        ? json({ trashed: [{ id: 9, root: 'movies', path: 'Брат 2.mkv', name: 'Брат 2.mkv', isDir: false, size: 1, deletedAt: '' }], warnings: [], needsConfirm: false })
        : undefined,
    )
    await renderFiles(admin, mock, '/files/movies', /Брат 2\.mkv/)
    await u.click(screen.getByRole('option', { name: /Брат 2\.mkv/ }))
    await u.keyboard('{Meta>}{Backspace}{/Meta}')
    await u.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'В корзину' }))
    const status = await screen.findByRole('status')
    // ui/Toast: placed by --toast-bottom, above the tab bar on a phone
    expect(status).toHaveAttribute('data-testid', 'toast-region')
    expect(within(status).getByRole('button', { name: 'Отменить' })).toBeInTheDocument()
  })
})

describe('navigation', () => {
  it('leaving a folder aborts its listing that is still loading', async () => {
    const u = userEvent.setup()
    let slow: AbortSignal | undefined
    const mock = mockApi(admin, BASE, (c) => {
      if (c.path === '/api/files/list' && c.query.get('path') === 'Интерстеллар (2014)') {
        slow = c.signal
        return new Promise<Response>((_, reject) => c.signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError'))))
      }
      return undefined
    })
    const router = await renderFiles(admin, mock, '/files/movies', /Брат 2\.mkv/)
    fireEvent.doubleClick(screen.getByRole('option', { name: /Интерстеллар/ }))
    await waitFor(() => expect(slow).toBeDefined())
    await u.click(screen.getByRole('navigation', { name: 'Места' }).querySelector('a[href="/files/movies"]') as HTMLElement)
    await waitFor(() => expect(router.state.location.pathname).toBe('/files/movies'))
    expect(slow!.aborted).toBe(true)
    expect(await screen.findByRole('option', { name: /Брат 2\.mkv/ })).toBeInTheDocument()
  })
})

describe('error states', () => {
  it('Свойства says when the details cannot be loaded; StorageBar says when usage is unknown', async () => {
    const u = userEvent.setup()
    const mock = mockApi(admin, BASE, (c) =>
      c.path === '/api/files/stat' ? json({ error: 'Нет доступа к файлу' }, 403) : c.path === '/api/files/usage' ? json({ error: 'x' }, 500) : undefined,
    )
    await renderFiles(admin, mock, '/files/movies', /Брат 2\.mkv/)
    await u.click(screen.getByRole('option', { name: /Брат 2\.mkv/ }))
    await u.keyboard('{Meta>}i{/Meta}')
    const info = await screen.findByRole('complementary', { name: 'Свойства' })
    expect(await within(info).findByText('Нет доступа к файлу')).toBeInTheDocument()
    expect(screen.getByText('Не удалось узнать свободное место')).toBeInTheDocument()
  })

  it('a folder size that never finishes ends with «Не удалось посчитать»', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      const mock = mockApi(admin, BASE, (c) => (c.path === '/api/files/stat' ? json({ ...FOLDER, dirSizePending: true }) : undefined))
      await renderFiles(admin, mock, '/files/movies', /Брат 2\.mkv/)
      fireEvent.click(screen.getByRole('option', { name: /Интерстеллар/ }))
      fireEvent.keyDown(screen.getByRole('listbox'), { key: 'i', metaKey: true })
      const info = await screen.findByRole('complementary', { name: 'Свойства' })
      expect(await within(info).findByText(/Подсчёт/)).toBeInTheDocument()
      await act(async () => {
        await vi.advanceTimersByTimeAsync(200_000)
      })
      expect(await within(info).findByText('Не удалось посчитать')).toBeInTheDocument()
    } finally {
      vi.useRealTimers()
    }
  })
})

describe('kid profiles', () => {
  it('have no «Файлы» in the profile menu and are sent from /files to Главная', async () => {
    vi.stubGlobal('fetch', mockApi(kid, BASE).fetch)
    render(
      <MemoryRouter>
        <ProfileMenu user={kid} />
      </MemoryRouter>,
    )
    fireEvent.click(screen.getByRole('button', { name: /Профиль/ }))
    expect(screen.queryByRole('menuitem', { name: 'Файлы' })).not.toBeInTheDocument()
    expect(screen.getByRole('menuitem', { name: 'Сменить профиль' })).toBeInTheDocument()

    await session.login(kid.id, 'pw')
    const router = createMemoryRouter(routes, { initialEntries: ['/files/movies'] })
    render(<RouterProvider router={router} />)
    await waitFor(() => expect(router.state.location.pathname).toBe('/'))
  })
})
