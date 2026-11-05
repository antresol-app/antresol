import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { RouterProvider, createMemoryRouter } from 'react-router'
import { getHome, login } from '../../api/client'
import { session } from '../../state/session'
import { routes } from '../routes'

const admin = { id: 1, name: 'Александр', avatar: 'grad-1', hasPassword: true, isAdmin: true, isKid: false, maxRating: null }

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

/** fetch mock: login works, /api/home says the session has expired. */
function expiredSession() {
  return vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input)
    if (url.endsWith('/api/login')) return json(admin)
    if (url.endsWith('/api/profiles')) return json([{ id: 1, name: 'Александр', avatar: 'grad-1', hasPassword: true }])
    return json({ error: 'Нужно войти' }, 401)
  })
}

describe('expired session (401)', () => {
  beforeEach(() => session.reset())
  afterEach(() => vi.unstubAllGlobals())

  it('signs the session out when an API call returns 401', async () => {
    vi.stubGlobal('fetch', expiredSession())
    await session.login(1, 'pw')
    expect(session.get().status).toBe('signed-in')
    await expect(getHome()).rejects.toMatchObject({ status: 401 })
    expect(session.get()).toEqual({ status: 'signed-out', user: null, remote: false })
  })

  it('a wrong password on login does not count as an expired session', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json({ error: 'Неверный пароль' }, 401)))
    await expect(login(1, 'x')).rejects.toMatchObject({ status: 401 })
    expect(session.get().status).toBe('loading')
  })

  it('takes the user from Главная to «Кто смотрит?» instead of a dead end', async () => {
    vi.stubGlobal('fetch', expiredSession())
    await session.login(1, 'pw')
    const router = createMemoryRouter(routes, { initialEntries: ['/'] })
    render(<RouterProvider router={router} />)
    expect(await screen.findByRole('heading', { name: 'Кто смотрит?' })).toBeInTheDocument()
    await waitFor(() => expect(router.state.location.pathname).toBe('/profiles'))
    expect(screen.queryByText('Повторить')).not.toBeInTheDocument()
  })
})
