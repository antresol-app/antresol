import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ShikimoriState, User } from '../../../api/types'
import { resetAccounts } from '../../../state/accounts'
import { session } from '../../../state/session'
import { closeWritePrompt } from '../../../state/writePrompt'
import { MarkButton } from '../../MarkButton'
import { WritePrompt } from '../WritePrompt'

const json = (b: unknown, status = 200) => new Response(JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } })
const adult: User = { id: 2, name: 'Мама', avatar: 'grad-3', hasPassword: false, isAdmin: false, isKid: false, maxRating: null }
const base: ShikimoriState = { linked: true, nickname: 'tester', status: 'ok', lastError: '', lastSync: null, writeState: 'unasked', canWrite: true, unmapped: 0 }

let state: ShikimoriState
let calls: string[]
function serve() {
  calls = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      const method = init?.method ?? 'GET'
      calls.push(`${method} ${url}${init?.body ? ' ' + String(init.body) : ''}`)
      if (url.endsWith('/api/login')) return json(adult)
      if (url === '/api/accounts') return json({ shikimori: state })
      if (url === '/api/accounts/shikimori/asked') return new Response(null, { status: 204 })
      if (url === '/api/accounts/shikimori/write') {
        return JSON.parse(String(init?.body)).enable ? json({ authorizeUrl: 'https://auth.example/a' }) : new Response(null, { status: 204 })
      }
      if (url.startsWith('/api/marks/') && method === 'GET') return json({ mark: null })
      if (url.startsWith('/api/marks/') && method === 'PUT') return json({ mark: { state: 'planned', score: null, origin: 'local', updatedAt: '2026-11-14T10:00:00Z' } })
      return json({})
    }),
  )
}
async function mark() {
  await userEvent.click(await screen.findByRole('button', { name: /Отметка/ }))
  await userEvent.click(await screen.findByRole('menuitemradio', { name: 'Хочу посмотреть' }))
}
const setup = async () => {
  serve()
  await session.login(adult.id)
  render(
    <>
      <MarkButton kind="series" tmdbId={7} />
      <WritePrompt />
    </>,
  )
}

beforeEach(() => {
  session.reset()
  resetAccounts()
  closeWritePrompt()
  state = base
})
afterEach(() => vi.unstubAllGlobals())

describe('WritePrompt', () => {
  it('prompt shows once after first mark change', async () => {
    await setup()
    await mark()
    expect(await screen.findByRole('alertdialog', { name: 'Отмечать и на Shikimori?' })).toBeInTheDocument()
    expect(calls).toContain('POST /api/accounts/shikimori/asked')
    await userEvent.click(screen.getByRole('button', { name: 'Нет' }))
    expect(calls).toContain('POST /api/accounts/shikimori/write {"enable":false}')
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
    // a second change: no prompt, no second accounts fetch
    await mark()
    await waitFor(() => expect(calls.filter((c) => c.startsWith('PUT /api/marks')).length).toBe(2))
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(calls.filter((c) => c === 'GET /api/accounts').length).toBe(1)
  })

  it('yes goes to authorize', async () => {
    const assign = vi.fn()
    vi.stubGlobal('location', { ...window.location, assign })
    await setup()
    await mark()
    await userEvent.click(await screen.findByRole('button', { name: 'Да' }))
    await waitFor(() => expect(assign).toHaveBeenCalledWith('https://auth.example/a'))
  })

  it('stays quiet when not unasked or cannot write', async () => {
    state = { ...base, writeState: 'asked' }
    await setup()
    await mark()
    await waitFor(() => expect(calls.some((c) => c.startsWith('PUT /api/marks'))).toBe(true))
    await waitFor(() => expect(calls).toContain('GET /api/accounts'))
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(calls).not.toContain('POST /api/accounts/shikimori/asked')
  })
})
