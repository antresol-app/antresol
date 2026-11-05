import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import type { User } from '../../api/types'
import { session, useSession } from '../../state/session'
import { ProfileMenu } from '../ProfileMenu'

const json = (b: unknown) => new Response(JSON.stringify(b), { status: 200, headers: { 'Content-Type': 'application/json' } })
const adult: User = { id: 2, name: 'Мама', avatar: 'grad-3', hasPassword: false, isAdmin: false, isKid: false, maxRating: null }

function Menu() {
  const { user } = useSession()
  return user ? <ProfileMenu user={user} /> : null
}

describe('ProfileMenu: «Скрывать прогресс просмотра»', () => {
  beforeEach(() => session.reset())
  afterEach(() => vi.unstubAllGlobals())

  it('an adult turns it on for itself', async () => {
    const calls: [string, string | undefined, unknown][] = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input)
        calls.push([url, init?.method, init?.body ? JSON.parse(String(init.body)) : undefined])
        if (url.endsWith('/api/login')) return json(adult)
        return json({ ...adult, hideProgress: true })
      }),
    )
    await session.login(2)
    render(
      <MemoryRouter>
        <Menu />
      </MemoryRouter>,
    )
    await userEvent.click(screen.getByRole('button', { name: /Мама/ }))
    const item = screen.getByRole('menuitemcheckbox', { name: 'Скрывать прогресс просмотра' })
    expect(item).toHaveAttribute('aria-checked', 'false')
    await userEvent.click(item)
    await waitFor(() => expect(screen.getByRole('menuitemcheckbox')).toHaveAttribute('aria-checked', 'true'))
    expect(calls.at(-1)).toEqual(['/api/me', 'PATCH', { hideProgress: true }])
  })

  it('a kid does not get it (the admin decides)', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json({ ...adult, isKid: true, maxRating: '12+' })))
    await session.login(2)
    render(
      <MemoryRouter>
        <Menu />
      </MemoryRouter>,
    )
    await userEvent.click(screen.getByRole('button', { name: /Мама/ }))
    expect(screen.queryByRole('menuitemcheckbox')).toBeNull()
  })
})
