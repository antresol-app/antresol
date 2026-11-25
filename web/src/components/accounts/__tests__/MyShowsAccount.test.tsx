import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { AccountsState, MyShowsState, ShikimoriState, User } from '../../../api/types'
import { resetAccounts } from '../../../state/accounts'
import { session } from '../../../state/session'
import { MyShowsAccount } from '../MyShowsAccount'

const json = (b: unknown, status = 200) => new Response(JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } })
const adult: User = { id: 2, name: 'Мама', avatar: 'grad-3', hasPassword: false, isAdmin: false, isKid: false, maxRating: null }
const kid: User = { ...adult, id: 3, name: 'Вася', isKid: true, maxRating: '12+' }

const shiki: ShikimoriState = { linked: false, nickname: '', status: 'ok', lastError: '', lastSync: null, syncing: false, writeState: 'unasked', canWrite: true, unmapped: 0 }
const unlinked: MyShowsState = { linked: false, nickname: '', status: 'ok', lastError: '', lastSync: null, syncing: false, unmapped: 0 }
const linked = (p: Partial<MyShowsState> = {}): MyShowsState => ({ ...unlinked, linked: true, nickname: 'tester', lastSync: new Date(Date.now() - 5 * 60000).toISOString(), syncing: false, unmapped: 3, ...p })

let state: MyShowsState
let offered: boolean
let syncRefusal = ''
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
      if (url === '/api/accounts' && method === 'GET') return json((offered ? { shikimori: shiki, myshows: state } : { shikimori: shiki }) satisfies AccountsState)
      if (url === '/api/accounts/myshows' && method === 'PUT') {
        state = linked({ lastSync: null, syncing: true, unmapped: 0 })
        return json({ shikimori: shiki, myshows: state })
      }
      if (url === '/api/accounts/myshows' && method === 'DELETE') return new Response(null, { status: 204 })
      if (url === '/api/accounts/myshows/sync' && method === 'POST') {
        if (syncRefusal) return json({ error: syncRefusal }, 429)
        state = { ...state, syncing: true }
        return json({ shikimori: shiki, myshows: state }, 202)
      }
      return json({})
    }),
  )
}
const open = async (user = adult) => {
  serve(user)
  await session.login(user.id)
  return render(<MyShowsAccount />)
}

beforeEach(() => {
  session.reset()
  resetAccounts()
  state = unlinked
  offered = true
  syncRefusal = ''
})
afterEach(() => vi.unstubAllGlobals())

describe('MyShowsAccount', () => {
  it('links by login', async () => {
    await open()
    await userEvent.type(await screen.findByLabelText('Логин на MyShows'), 'tester')
    await userEvent.click(screen.getByRole('button', { name: 'Связать' }))
    expect(await screen.findByText('Связан как tester')).toBeInTheDocument()
    expect(calls).toContain('PUT /api/accounts/myshows {"nickname":"tester"}')
  })

  it('says in one line that it only reads, and shows the status line', async () => {
    state = linked()
    await open()
    expect(await screen.findByText('Только чтение: список с MyShows попадает в отметки.')).toBeInTheDocument()
    expect(screen.getByText(/Синхронизировано .* · не найдено в TMDB: 3/)).toBeInTheDocument()
  })

  it('polls after a link until the first sync lands', async () => {
    await open()
    await userEvent.type(await screen.findByLabelText('Логин на MyShows'), 'tester')
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      await userEvent.click(screen.getByRole('button', { name: 'Связать' }))
      expect(await screen.findByText('Синхронизация…')).toBeInTheDocument()
      expect(screen.getByText('Первое чтение списка займёт несколько минут')).toBeInTheDocument()
      // minutes pass without a result: still asking, with growing pauses
      const getsNow = () => calls.filter((c) => c === 'GET /api/accounts').length
      const before = getsNow()
      await vi.advanceTimersByTimeAsync(5 * 60000)
      const during = getsNow() - before
      expect(during).toBeGreaterThan(8)
      expect(during).toBeLessThan(40)
      await vi.advanceTimersByTimeAsync(5 * 60000)
      expect(getsNow() - before).toBeGreaterThan(during)
      state = linked({ unmapped: 0 }) // the pull finished on the server
      await vi.advanceTimersByTimeAsync(31000)
      expect(await screen.findByText(/Синхронизировано/)).toBeInTheDocument()
      const gets = () => calls.filter((c) => c === 'GET /api/accounts').length
      const n = gets()
      await vi.advanceTimersByTimeAsync(20000)
      expect(gets()).toBe(n) // stopped once the server said it ended
    } finally {
      vi.useRealTimers()
    }
  })

  it('syncs now: says it runs, then shows how it went', async () => {
    state = linked({ unmapped: 3 })
    await open()
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      await userEvent.click(await screen.findByRole('button', { name: 'Синхронизировать' }))
      expect(calls).toContain('POST /api/accounts/myshows/sync')
      expect(await screen.findByText('Синхронизация…')).toBeInTheDocument()
      const button = screen.getByRole('button', { name: 'Синхронизировать' })
      expect(button).toBeDisabled()
      expect(button).toHaveAttribute('aria-busy', 'true')
      expect(screen.queryByText('Первое чтение списка займёт несколько минут')).toBeNull() // it has been read before
      // a long list: still running some polls later
      await vi.advanceTimersByTimeAsync(20000)
      expect(screen.getByText('Синхронизация…')).toBeInTheDocument()
      state = linked({ lastSync: new Date().toISOString(), unmapped: 1 }) // it ended on the server
      await vi.advanceTimersByTimeAsync(31000)
      expect(await screen.findByText('Синхронизировано только что · не найдено в TMDB: 1')).toBeInTheDocument()
      expect(screen.getByRole('button', { name: 'Синхронизировать' })).toBeEnabled()
      const gets = () => calls.filter((c) => c === 'GET /api/accounts').length
      const n = gets()
      await vi.advanceTimersByTimeAsync(40000)
      expect(gets()).toBe(n)
    } finally {
      vi.useRealTimers()
    }
  })

  it('shows a failed sync as its result', async () => {
    state = linked()
    await open()
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      await userEvent.click(await screen.findByRole('button', { name: 'Синхронизировать' }))
      expect(await screen.findByText('Синхронизация…')).toBeInTheDocument()
      state = linked({ status: 'error', lastError: 'MyShows не отвечает, попробуйте позже', lastSync: new Date().toISOString() })
      await vi.advanceTimersByTimeAsync(5000)
      expect(await screen.findByText('Не удалось синхронизировать: MyShows не отвечает, попробуйте позже')).toBeInTheDocument()
      expect(screen.getByRole('button', { name: 'Синхронизировать' })).toBeEnabled()
    } finally {
      vi.useRealTimers()
    }
  })

  it('shows a sync it did not start (the hourly one) and follows it to its end', async () => {
    state = linked({ syncing: true })
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      await open()
      expect(await screen.findByText('Синхронизация…')).toBeInTheDocument()
      expect(screen.getByRole('button', { name: 'Синхронизировать' })).toBeDisabled()
      expect(calls.some((c) => c.includes('/sync'))).toBe(false)
      state = linked({ lastSync: new Date().toISOString() })
      await vi.advanceTimersByTimeAsync(5000)
      expect(await screen.findByText('Синхронизировано только что · не найдено в TMDB: 3')).toBeInTheDocument()
    } finally {
      vi.useRealTimers()
    }
  })

  it('says why a sync was not started, and offers none before linking', async () => {
    state = linked()
    syncRefusal = 'Только что синхронизировано, повторите через минуту'
    const { unmount } = await open()
    await userEvent.click(await screen.findByRole('button', { name: 'Синхронизировать' }))
    expect(await screen.findByText('Только что синхронизировано, повторите через минуту')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Синхронизировать' })).toBeEnabled()
    expect(screen.queryByText('Синхронизация…')).toBeNull()
    unmount()
    resetAccounts()
    state = unlinked
    await open()
    await screen.findByText('Не связан')
    expect(screen.queryByRole('button', { name: 'Синхронизировать' })).toBeNull()
  })

  it('unlinks', async () => {
    state = linked()
    await open()
    await userEvent.click(await screen.findByRole('button', { name: 'Отвязать' }))
    expect(await screen.findByLabelText('Логин на MyShows')).toBeInTheDocument()
    expect(calls).toContain('DELETE /api/accounts/myshows')
  })

  it('is hidden for a kid', async () => {
    const { container } = await open(kid)
    await Promise.resolve()
    expect(container).toBeEmptyDOMElement()
    expect(calls.some((c) => c.includes('/api/accounts'))).toBe(false)
  })

  it('is hidden when the server does not offer MyShows', async () => {
    offered = false
    const { container } = await open()
    await vi.waitFor(() => expect(calls).toContain('GET /api/accounts'))
    await Promise.resolve()
    expect(container).toBeEmptyDOMElement()
  })
})
