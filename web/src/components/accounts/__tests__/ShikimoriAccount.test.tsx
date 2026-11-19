import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import type { AccountsState, ShikimoriState, User } from '../../../api/types'
import { resetAccounts } from '../../../state/accounts'
import { session } from '../../../state/session'
import { ShikimoriAccount } from '../ShikimoriAccount'
import { ShikimoriAdmin } from '../ShikimoriAdmin'

const json = (b: unknown, status = 200) => new Response(JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } })
const adult: User = { id: 2, name: 'Мама', avatar: 'grad-3', hasPassword: false, isAdmin: false, isKid: false, maxRating: null }
const kid: User = { ...adult, id: 3, name: 'Вася', isKid: true, maxRating: '12+' }

const unlinked: ShikimoriState = { linked: false, nickname: '', status: 'ok', lastError: '', lastSync: null, writeState: 'unasked', canWrite: true, unmapped: 0 }
const linked = (p: Partial<ShikimoriState> = {}): ShikimoriState => ({ ...unlinked, linked: true, nickname: 'tester', lastSync: new Date(Date.now() - 5 * 60000).toISOString(), ...p })

let state: ShikimoriState
let calls: string[]
function serve(user: User) {
  calls = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      const method = init?.method ?? 'GET'
      calls.push(`${method} ${url}${init?.body ? ' ' + String(init.body) : ''}`)
      if (url.endsWith('/api/login')) return json(user)
      if (url === '/api/accounts' && method === 'GET') return json({ shikimori: state } satisfies AccountsState)
      if (url === '/api/accounts/shikimori' && method === 'PUT') {
        state = linked()
        return json({ shikimori: state })
      }
      if (url === '/api/accounts/shikimori/write') {
        const { enable } = JSON.parse(String(init?.body)) as { enable: boolean }
        if (enable) return json({ authorizeUrl: 'https://auth.example/authorize?state=s1' })
        state = { ...state, writeState: 'declined' }
        return new Response(null, { status: 204 })
      }
      if (url === '/api/accounts/shikimori' && method === 'DELETE') return new Response(null, { status: 204 })
      return json({})
    }),
  )
}
const open = async (user = adult) => {
  serve(user)
  await session.login(user.id)
  return render(
    <MemoryRouter>
      <ShikimoriAccount />
    </MemoryRouter>,
  )
}

beforeEach(() => {
  session.reset()
  resetAccounts()
  state = unlinked
})
afterEach(() => vi.unstubAllGlobals())

describe('ShikimoriAccount', () => {
  it('links by nickname', async () => {
    await open()
    await userEvent.type(await screen.findByLabelText('Ник на Shikimori'), 'tester')
    await userEvent.click(screen.getByRole('button', { name: 'Связать' }))
    expect(await screen.findByText('Связан как tester')).toBeInTheDocument()
    expect(calls).toContain('PUT /api/accounts/shikimori {"nickname":"tester"}')
  })

  it('says it is not linked, and that it syncs both ways once marks may go back', async () => {
    await open()
    expect(await screen.findByText('Не связан')).toBeInTheDocument()
    expect(screen.getByText(/В обе стороны: список с Shikimori попадает в отметки, а после одного входа туда/)).toBeInTheDocument()
    // no unlink, no last sync for a card that has no link
    expect(screen.queryByRole('button', { name: 'Отвязать' })).toBeNull()
    expect(screen.queryByText(/Синхронизировано|Ещё не синхронизировано/)).toBeNull()
  })

  it('is honest about the direction: reading only until marks are allowed to go back', async () => {
    state = linked()
    const { unmount } = await open()
    expect(await screen.findByText(/Сейчас только чтение: список с Shikimori попадает в отметки, обратно ничего не уходит/)).toBeInTheDocument()
    unmount()
    resetAccounts()
    state = linked({ writeState: 'enabled' })
    await open()
    expect(await screen.findByText('В обе стороны: список с Shikimori попадает в отметки, а твои отметки уходят на Shikimori.')).toBeInTheDocument()
  })

  it('shows the last sync and how it went, and unlinks', async () => {
    state = linked()
    await open()
    expect(await screen.findByText('Синхронизировано 5 мин назад')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Отвязать' }))
    expect(await screen.findByText('Не связан')).toBeInTheDocument()
    expect(calls).toContain('DELETE /api/accounts/shikimori')
  })

  it('puts a failed sync in the card', async () => {
    state = linked({ status: 'error', lastError: 'Shikimori не отвечает' })
    await open()
    expect(await screen.findByText('Не удалось синхронизировать: Shikimori не отвечает')).toBeInTheDocument()
  })

  it('shows synced status with the unmapped count', async () => {
    state = linked({ unmapped: 3 })
    await open()
    expect(await screen.findByText('Синхронизировано 5 мин назад · не найдено в TMDB: 3')).toBeInTheDocument()
  })

  it('shows private list status', async () => {
    state = linked({ status: 'private' })
    await open()
    expect(await screen.findByText(/Список на Shikimori закрыт/)).toBeInTheDocument()
  })

  it('toggle on opens authorize url', async () => {
    state = linked()
    const assign = vi.fn()
    vi.stubGlobal('location', { ...window.location, assign })
    await open()
    await userEvent.click(await screen.findByRole('switch', { name: 'Отмечать и на Shikimori' }))
    await waitFor(() => expect(assign).toHaveBeenCalledWith('https://auth.example/authorize?state=s1'))
    expect(calls).toContain('POST /api/accounts/shikimori/write {"enable":true}')
  })

  it('toggle off declines', async () => {
    state = linked({ writeState: 'enabled' })
    await open()
    const sw = await screen.findByRole('switch', { name: 'Отмечать и на Shikimori' })
    expect(sw).toBeChecked()
    await userEvent.click(sw)
    await waitFor(() => expect(calls).toContain('POST /api/accounts/shikimori/write {"enable":false}'))
    await waitFor(() => expect(screen.getByRole('switch', { name: 'Отмечать и на Shikimori' })).not.toBeChecked())
  })

  it('hidden for kid', async () => {
    const { container } = await open(kid)
    expect(container).toBeEmptyDOMElement()
    expect(calls.filter((c) => c.includes('/api/accounts'))).toEqual([])
  })

  it('hidden when accounts are not mounted', async () => {
    serve(adult)
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => (String(input).endsWith('/api/login') ? json(adult) : json({ error: 'not found' }, 404))),
    )
    await session.login(adult.id)
    const { container } = render(
      <MemoryRouter>
        <ShikimoriAccount />
      </MemoryRouter>,
    )
    await waitFor(() => expect(fetch).toHaveBeenCalledWith('/api/accounts', expect.anything()))
    expect(container).toBeEmptyDOMElement()
  })
})

describe('ShikimoriAdmin', () => {
  it('admin hint shows redirect uri', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => json({ clientId: 'my-id', hasSecret: true, fromEnv: false, redirectUri: 'https://media.example/api/accounts/shikimori/callback', scope: 'user_rates' })),
    )
    render(<ShikimoriAdmin />)
    expect(await screen.findByDisplayValue('https://media.example/api/accounts/shikimori/callback')).toHaveAttribute('readonly')
    expect(screen.getByLabelText('Scope')).toHaveValue('user_rates')
    expect(screen.getByLabelText('Client ID')).toHaveValue('my-id')
    expect(screen.getByLabelText('Client Secret')).toHaveAttribute('placeholder', 'сохранён')
  })

  it('read-only when from env', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json({ clientId: 'env-id', hasSecret: true, fromEnv: true, redirectUri: '', scope: 'user_rates' })))
    render(<ShikimoriAdmin />)
    expect(await screen.findByLabelText('Client ID')).toHaveAttribute('readonly')
  })
})
