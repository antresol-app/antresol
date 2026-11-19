// «Аккаунты и синхронизация» of a profile's settings (pages/AccountsSettings.tsx):
// one page with a card per service, the row that leads to it from «Настройки»,
// the old addresses that still land there, and what a kid or a server without
// accounts gets.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, RouterProvider, Routes, createMemoryRouter, useLocation } from 'react-router'
import type { AccountsState, User } from '../../api/types'
import { routes } from '../../app/routes'
import { Notice } from '../../components/Notice'
import { resetAccounts } from '../../state/accounts'
import { session } from '../../state/session'
import { setViewport } from '../../test/setup'
import { AccountsSettings } from '../AccountsSettings'
import { ProfileSettings } from '../ProfileSettings'

const json = (b: unknown, status = 200) => new Response(JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } })
const adult: User = { id: 2, name: 'Мама', avatar: 'grad-3', hasPassword: false, isAdmin: false, isKid: false, maxRating: null }
const admin: User = { ...adult, id: 1, name: 'Папа', isAdmin: true }
const kid: User = { ...adult, id: 3, name: 'Вася', isKid: true, maxRating: '12+' }

const ago = (min: number) => new Date(Date.now() - min * 60000).toISOString()
const full: AccountsState = {
  shikimori: { linked: true, nickname: 'tester', status: 'ok', lastError: '', lastSync: ago(5), syncing: false, writeState: 'enabled', canWrite: true, unmapped: 0 },
  myshows: { linked: false, nickname: '', status: 'ok', lastError: '', lastSync: null, syncing: false, unmapped: 0 },
  goodreads: { linked: true, nickname: 'reader', status: 'error', lastError: 'Goodreads не отвечает', lastSync: ago(60), syncing: false, total: 4, read: 2, reading: 1, toRead: 1, inLibrary: 2 },
}

/** What `/api/accounts` answers: a state, or null for a server that has none (404). */
function serve(user: User, accounts: AccountsState | null) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      const method = init?.method ?? 'GET'
      if (url.endsWith('/api/login')) return json(user)
      if (url === '/api/accounts' && method === 'GET') return accounts ? json(accounts) : json({ error: 'не найдено' }, 404)
      if (url.endsWith('/api/me/ui-language')) return json({ language: '' })
      return json({})
    }),
  )
}

function Where() {
  const l = useLocation()
  return <span data-testid="where">{l.pathname + l.search}</span>
}

async function openAccounts(user: User, accounts: AccountsState | null, at = '/settings/accounts') {
  serve(user, accounts)
  await session.login(user.id)
  return render(
    <MemoryRouter initialEntries={[at]}>
      <Notice />
      <Routes>
        <Route path="/settings/accounts" element={<AccountsSettings />} />
        <Route path="/settings" element={<ProfileSettings />} />
        <Route path="*" element={<Where />} />
      </Routes>
      <Where />
    </MemoryRouter>,
  )
}

beforeEach(() => {
  session.reset()
  resetAccounts()
  setViewport(1440)
})
afterEach(() => {
  vi.unstubAllGlobals()
  session.reset()
})

describe('AccountsSettings', () => {
  it('lists every service as a card: who it is linked as, which way it syncs, when it last ran', async () => {
    await openAccounts(adult, full)
    expect(await screen.findByRole('heading', { name: 'Аккаунты и синхронизация', level: 1 })).toBeInTheDocument()
    const card = async (title: string) => within((await screen.findByRole('heading', { name: title, level: 3 })).closest('section')!)

    const shiki = await card('Shikimori')
    expect(await shiki.findByText('Связан как tester')).toBeInTheDocument()
    expect(shiki.getByText(/В обе стороны: список с Shikimori попадает в отметки, а твои отметки уходят на Shikimori/)).toBeInTheDocument()
    expect(shiki.getByText('Синхронизировано 5 мин назад')).toBeInTheDocument()
    expect(shiki.getByRole('button', { name: 'Отвязать' })).toBeInTheDocument()
    expect(shiki.getByRole('button', { name: 'Синхронизировать' })).toBeEnabled()

    const myshows = await card('MyShows')
    expect(await myshows.findByText('Не связан')).toBeInTheDocument()
    expect(myshows.getByText('Только чтение: список с MyShows попадает в отметки; отправка обратно появится позже.')).toBeInTheDocument()
    expect(myshows.getByRole('button', { name: 'Связать' })).toBeInTheDocument()
    expect(myshows.queryByRole('button', { name: 'Отвязать' })).toBeNull()
    expect(myshows.queryByRole('button', { name: 'Синхронизировать' })).toBeNull() // nothing to sync before linking

    const goodreads = await card('Goodreads')
    expect(await goodreads.findByText('Связан как reader')).toBeInTheDocument()
    expect(goodreads.getByText(/Только чтение: полки с Goodreads попадают в «Книги»/)).toBeInTheDocument()
    // the last result is on the card, an error as an error
    expect(goodreads.getByText('Не удалось синхронизировать: Goodreads не отвечает')).toBeInTheDocument()
    expect(goodreads.getByRole('button', { name: 'Выбрать файл' })).toBeInTheDocument()
    expect(goodreads.getByRole('button', { name: 'Синхронизировать' })).toBeInTheDocument()
  })

  it('leaves out a service the server does not offer', async () => {
    await openAccounts(adult, { shikimori: full.shikimori })
    expect(await screen.findByRole('heading', { name: 'Shikimori', level: 3 })).toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: 'MyShows' })).toBeNull()
    expect(screen.queryByRole('heading', { name: 'Goodreads' })).toBeNull()
  })

  it('says so on a server without accounts', async () => {
    await openAccounts(adult, null)
    expect(await screen.findByText('Аккаунты недоступны')).toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: 'Shikimori' })).toBeNull()
  })

  it('has a way back to «Настройки»', async () => {
    await openAccounts(adult, full)
    await userEvent.click(await screen.findByRole('link', { name: 'Настройки' }))
    expect(await screen.findByRole('heading', { name: 'Настройки', level: 1 })).toBeInTheDocument()
  })

  it('is drawn for an administrator too, in «Управление»', async () => {
    await openAccounts(admin, full)
    expect(await screen.findByRole('heading', { name: 'Аккаунты и синхронизация', level: 1 })).toBeInTheDocument()
    expect(await screen.findByText('Связан как tester')).toBeInTheDocument()
    // the wide window's sidebar already leads back: no second back link in the page
    expect(screen.getByRole('complementary')).toBeInTheDocument()
  })

  it('sends a kid back to «Настройки»', async () => {
    await openAccounts(kid, full)
    expect(await screen.findByRole('heading', { name: 'Настройки', level: 1 })).toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: 'Аккаунты и синхронизация' })).toBeNull()
  })

  describe('back from Shikimori sign-in', () => {
    it('says how it went and clears the address', async () => {
      await openAccounts(adult, full, '/settings/accounts?account=shikimori&error=denied')
      expect(await screen.findByText('Доступ на Shikimori не разрешён.')).toBeInTheDocument()
      await waitFor(() => expect(screen.getByTestId('where')).toHaveTextContent(/^\/settings\/accounts$/))
    })

    it('confirms a success, and takes an unknown error for a failed exchange', async () => {
      await openAccounts(adult, full, '/settings/accounts?account=shikimori&ok=1')
      expect(await screen.findByText('Shikimori подключён: отметки будут отправляться')).toBeInTheDocument()
      cleanup()
      session.reset()
      await openAccounts(adult, full, '/settings/accounts?account=shikimori&error=whatever')
      expect(await screen.findByText('Shikimori не принял вход. Попробуй ещё раз позже.')).toBeInTheDocument()
    })
  })
})

describe('the old addresses', () => {
  it('«Настройки» with the sign-in\'s answer (the old redirect) lands on the accounts page and says how it went', async () => {
    await openAccounts(adult, full, '/settings?account=shikimori&error=denied')
    expect(await screen.findByRole('heading', { name: 'Аккаунты и синхронизация', level: 1 })).toBeInTheDocument()
    expect(await screen.findByText('Доступ на Shikimori не разрешён.')).toBeInTheDocument()
    await waitFor(() => expect(screen.getByTestId('where')).toHaveTextContent(/^\/settings\/accounts$/))
  })

  it('the route table has /settings/accounts for a profile', async () => {
    serve(adult, full)
    await session.login(adult.id)
    const router = createMemoryRouter(routes, { initialEntries: ['/settings/accounts'] })
    render(<RouterProvider router={router} />)
    expect(await screen.findByRole('heading', { name: 'Аккаунты и синхронизация', level: 1 })).toBeInTheDocument()
    expect(await screen.findByText('Связан как tester')).toBeInTheDocument()
  })

  it('and sends the old sign-in redirect on, through the route table', async () => {
    serve(adult, full)
    await session.login(adult.id)
    const router = createMemoryRouter(routes, { initialEntries: ['/settings?account=shikimori&ok=1'] })
    render(<RouterProvider router={router} />)
    expect(await screen.findByRole('heading', { name: 'Аккаунты и синхронизация', level: 1 })).toBeInTheDocument()
    await waitFor(() => expect(router.state.location.pathname).toBe('/settings/accounts'))
  })
})

describe('«Настройки» → «Аккаунты и синхронизация»', () => {
  it('is one row with the number of linked services, and the three services are not in the list any more', async () => {
    await openAccounts(adult, full, '/settings')
    const row = await screen.findByRole('button', { name: /Аккаунты и синхронизация/ })
    expect(row).toHaveTextContent('Связано: 2')
    expect(screen.queryByText('Shikimori')).toBeNull()
    expect(screen.queryByText('MyShows')).toBeNull()
    expect(screen.queryByText('Goodreads')).toBeNull()
    await userEvent.click(row)
    expect(await screen.findByRole('heading', { name: 'Аккаунты и синхронизация', level: 1 })).toBeInTheDocument()
  })

  it('says when nothing is linked', async () => {
    const none: AccountsState = { shikimori: { ...full.shikimori, linked: false, nickname: '', writeState: 'unasked' } }
    await openAccounts(adult, none, '/settings')
    expect(await screen.findByRole('button', { name: /Аккаунты и синхронизация/ })).toHaveTextContent('Не связаны')
  })

  it('is not there on a server without accounts, nor for a kid', async () => {
    await openAccounts(adult, null, '/settings')
    expect(await screen.findByRole('heading', { name: 'Настройки', level: 1 })).toBeInTheDocument()
    await waitFor(() => expect(fetch).toHaveBeenCalledWith('/api/accounts', expect.anything()))
    expect(screen.queryByRole('button', { name: /Аккаунты и синхронизация/ })).toBeNull()
    cleanup()
    session.reset()
    resetAccounts()
    await openAccounts(kid, full, '/settings')
    expect(await screen.findByRole('heading', { name: 'Настройки', level: 1 })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Аккаунты и синхронизация/ })).toBeNull()
  })
})
