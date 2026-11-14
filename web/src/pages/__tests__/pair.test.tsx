// «Вход на телевизоре» (/pair?code=…): the device shown from the code in the
// address, approve and deny, an admin's choice of profile, the remote
// warning, a code typed by hand and a dead code.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router'
import type { User } from '../../api/types'
import { session } from '../../state/session'
import { Pair } from '../Pair'

const json = (b: unknown, status = 200) => new Response(JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } })
const adult: User = { id: 2, name: 'Мама', avatar: 'grad-3', hasPassword: true, isAdmin: false, isKid: false, maxRating: null }
const admin: User = { ...adult, id: 1, name: 'Папа', isAdmin: true }
const req = (remote = false) => ({
  code: 'ABC234',
  device: { name: 'Гостиная', model: 'Android TV', version: '3.1' },
  remote,
  createdAt: '2026-11-14T12:00:00Z',
  expiresAt: '2026-11-14T12:05:00Z',
})

type Call = [string, string | undefined, unknown]
function serve(user: User, extra?: (url: string, body: unknown) => Response | undefined) {
  const calls: Call[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      const body = init?.body ? JSON.parse(String(init.body)) : undefined
      calls.push([url, init?.method, body])
      const r = extra?.(url, body)
      if (r) return r
      if (url.endsWith('/api/login')) return json(user)
      if (url.endsWith('/api/profiles'))
        return json([
          { id: 1, name: 'Папа', avatar: 'grad-1', hasPassword: true },
          { id: 3, name: 'Вася', avatar: 'grad-2', hasPassword: true },
        ])
      if (url.endsWith('/api/auth/pair/lookup')) return json(req())
      if (url.endsWith('/api/auth/pair/approve')) return json({ ok: true, userId: body.userId || user.id })
      if (url.endsWith('/api/auth/pair/deny')) return new Response(null, { status: 204 })
      return json({})
    }),
  )
  return calls
}

async function open(user: User, path: string, extra?: (url: string, body: unknown) => Response | undefined) {
  const calls = serve(user, extra)
  await session.login(user.id)
  render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/pair" element={<Pair />} />
      </Routes>
    </MemoryRouter>,
  )
  return calls
}

const pairCalls = (calls: Call[]) => calls.filter(([u]) => u.includes('/api/auth/pair/'))

beforeEach(() => session.reset())
afterEach(() => vi.unstubAllGlobals())

describe('Pair', () => {
  it('shows the device from the code and approves it for the caller', async () => {
    const calls = await open(adult, '/pair?code=abc-234&sid=srv')
    expect(await screen.findByText('Гостиная')).toBeInTheDocument()
    expect(screen.getByText('Android TV')).toBeInTheDocument()
    expect(screen.getByText('Мама')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Подтвердить вход' }))
    expect(await screen.findByText('Вход подтверждён')).toBeInTheDocument()
    expect(screen.getByText('Телевизор открывает профиль «Мама».')).toBeInTheDocument()
    expect(pairCalls(calls)).toEqual([
      ['/api/auth/pair/lookup', 'POST', { code: 'ABC234' }],
      ['/api/auth/pair/approve', 'POST', { code: 'ABC234', userId: 0 }],
    ])
  })

  it('denies', async () => {
    const calls = await open(adult, '/pair?code=ABC234')
    await userEvent.click(await screen.findByRole('button', { name: 'Отклонить' }))
    expect(await screen.findByText('Вход отклонён')).toBeInTheDocument()
    expect(pairCalls(calls).at(-1)).toEqual(['/api/auth/pair/deny', 'POST', { code: 'ABC234' }])
  })

  it('lets an admin sign the TV in to another profile', async () => {
    const calls = await open(admin, '/pair?code=ABC234')
    await screen.findByText('Гостиная')
    await userEvent.click(await screen.findByRole('button', { name: /Профиль/ }))
    await userEvent.click(await screen.findByRole('menuitemradio', { name: 'Вася' }))
    await userEvent.click(screen.getByRole('button', { name: 'Подтвердить вход' }))
    expect(await screen.findByText('Телевизор открывает профиль «Вася».')).toBeInTheDocument()
    expect(pairCalls(calls).at(-1)).toEqual(['/api/auth/pair/approve', 'POST', { code: 'ABC234', userId: 3 }])
  })

  it('warns about a device outside the home network', async () => {
    await open(adult, '/pair?code=ABC234', (url) => (url.endsWith('/lookup') ? json(req(true)) : undefined))
    expect(await screen.findByText('Устройство не в домашней сети')).toBeInTheDocument()
  })

  it('takes a typed code, and says when a code is dead', async () => {
    const calls = await open(adult, '/pair', (url, body) =>
      url.endsWith('/lookup') && (body as { code: string }).code === 'ZZZ999' ? json({ error: 'Код не найден или устарел' }, 404) : undefined,
    )
    const field = screen.getByLabelText('Код с экрана телевизора')
    const go = screen.getByRole('button', { name: 'Продолжить' })
    expect(go).toBeDisabled()
    await userEvent.type(field, 'zzz-999')
    await userEvent.click(go)
    expect(await screen.findByText('Код не найден или устарел')).toBeInTheDocument()
    await userEvent.clear(screen.getByLabelText('Код с экрана телевизора'))
    await userEvent.type(screen.getByLabelText('Код с экрана телевизора'), 'abc234')
    await userEvent.click(screen.getByRole('button', { name: 'Продолжить' }))
    expect(await screen.findByText('Гостиная')).toBeInTheDocument()
    await waitFor(() => expect(pairCalls(calls).map((c) => c[2])).toEqual([{ code: 'ZZZ999' }, { code: 'ABC234' }]))
  })
})
