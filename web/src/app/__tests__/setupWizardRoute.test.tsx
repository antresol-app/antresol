// /setup/wizard (the steps after the first administrator) is for an
// administrator only; anyone else is sent home without the wizard asking the
// server anything.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import { RouterProvider, createMemoryRouter } from 'react-router'
import { session } from '../../state/session'
import { routes } from '../routes'

const adult = { id: 2, name: 'Adult', avatar: 'grad-3', hasPassword: false, isAdmin: false, isKid: false, maxRating: null }
const admin = { ...adult, id: 1, name: 'Admin', isAdmin: true }

const json = (b: unknown) => new Response(JSON.stringify(b), { status: 200, headers: { 'Content-Type': 'application/json' } })

async function signIn(user: { id: number }, pending: boolean) {
  const calls: string[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input)
      calls.push(url)
      if (url.endsWith('/api/login')) return json(user)
      if (url.endsWith('/api/setup/wizard')) return json({ pending })
      return json({})
    }),
  )
  await session.login(user.id)
  return calls
}

describe('the setup wizard route', () => {
  beforeEach(() => {
    session.reset()
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    vi.spyOn(console, 'error').mockImplementation(() => {})
  })
  afterEach(() => {
    cleanup()
    vi.unstubAllGlobals()
    session.reset()
  })

  it('an administrator reaches it while it is pending', async () => {
    await signIn(admin, true)
    const router = createMemoryRouter(routes, { initialEntries: ['/setup/wizard'] })
    render(<RouterProvider router={router} />)
    expect(await screen.findByRole('heading', { name: 'Медиа-папки' })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/setup/wizard')
  })

  it('an administrator is sent home once it is over', async () => {
    await signIn(admin, false)
    const router = createMemoryRouter(routes, { initialEntries: ['/setup/wizard'] })
    render(<RouterProvider router={router} />)
    await waitFor(() => expect(router.state.location.pathname).toBe('/'))
    expect(screen.queryByRole('heading', { name: 'Медиа-папки' })).toBeNull()
  })

  it('anyone else is sent home at once', async () => {
    const calls = await signIn(adult, true)
    const router = createMemoryRouter(routes, { initialEntries: ['/setup/wizard'] })
    render(<RouterProvider router={router} />)
    await waitFor(() => expect(router.state.location.pathname).toBe('/'))
    expect(calls.some((u) => u.endsWith('/api/setup/wizard'))).toBe(false)
    expect(screen.queryByRole('heading', { name: 'Медиа-папки' })).toBeNull()
  })
})
