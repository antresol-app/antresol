import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import type { User } from '../../../api/types'
import { Profiles } from '../Profiles'

function json(body: unknown, status = 200) {
  return new Response(body === undefined ? null : JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

afterEach(() => vi.unstubAllGlobals())

describe('Profiles: «Сбросить пароль для приложений»', () => {
  it('ends another profile’s app password from its edit sheet', async () => {
    const masha = { id: 2, name: 'Маша', avatar: 'grad-2', hasPassword: true, isAdmin: false, isKid: false, maxRating: null } as unknown as User
    const f = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const u = String(input)
      if (u.endsWith('/api/users') && init?.method === 'GET') return json([masha])
      if (u.endsWith('/api/users/2/app-password') && init?.method === 'DELETE') return json(undefined, 204)
      return json({ error: 'нет' }, 404)
    })
    vi.stubGlobal('fetch', f)
    render(
      <MemoryRouter>
        <Profiles />
      </MemoryRouter>,
    )
    await userEvent.click(await screen.findByRole('button', { name: /Изменить/ }))
    const sheet = await screen.findByRole('dialog')
    await userEvent.click(within(sheet).getByRole('button', { name: 'Сбросить пароль для приложений' }))
    await waitFor(() => expect(within(sheet).getByText('Пароль для приложений сброшен.')).toBeInTheDocument())
    expect(f.mock.calls.some(([u, i]) => String(u).endsWith('/api/users/2/app-password') && i?.method === 'DELETE')).toBe(true)
  })
})
