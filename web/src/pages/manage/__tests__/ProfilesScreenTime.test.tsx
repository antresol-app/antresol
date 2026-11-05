import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import type { User } from '../../../api/types'
import { Profiles } from '../Profiles'

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })

afterEach(() => vi.unstubAllGlobals())

// A limited kid's row warns about the profiles open without a password.
describe('Profiles: «Экранное время» and passwordless profiles', () => {
  it('warns on a limited kid’s row only', async () => {
    const u = (id: number, name: string, isKid: boolean, hasPassword: boolean) =>
      ({ id, name, avatar: 'grad-1', hasPassword, isAdmin: false, isKid, maxRating: null }) as unknown as User
    const users = [u(1, 'Папа', false, true), u(2, 'Мама', false, false), u(3, 'Вася', true, false), u(4, 'Петя', true, false)]
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input)
        if (url.endsWith('/api/users')) return json(users)
        if (url.endsWith('/api/screen-time/limited'))
          return json({ userIds: [3], passwordless: [{ id: 2, name: 'Мама' }, { id: 3, name: 'Вася' }, { id: 4, name: 'Петя' }] })
        return json({ error: 'нет' }, 404)
      }),
    )
    render(
      <MemoryRouter>
        <Profiles />
      </MemoryRouter>,
    )
    const warnings = await screen.findAllByText(/Ребёнок может переключиться на профили без пароля/)
    expect(warnings).toHaveLength(1)
    expect(warnings[0]).toHaveTextContent('без пароля: Мама, Петя. Задайте им пароль, чтобы лимиты работали.')
  })
})
