// The settings are apart from the app's own pages: an admin's channel
// management is a section of «Настройки» (/manage/channels), the old
// addresses (/tv/manage…) lead there, «Обновления» became «Состояние»
// (/manage/updates → /manage/health), «Перекодирование» part of «Обработка»
// (handled by the settings screen itself). A profile that is not an admin
// still manages its own channels from «ТВ».
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, waitFor } from '@testing-library/react'
import { RouterProvider, createMemoryRouter } from 'react-router'
import { session } from '../../state/session'
import { routes } from '../routes'

const adult = { id: 2, name: 'Мама', avatar: 'grad-3', hasPassword: false, isAdmin: false, isKid: false, maxRating: null }
const admin = { ...adult, id: 1, name: 'Папа', isAdmin: true }

const json = (b: unknown) => new Response(JSON.stringify(b), { status: 200, headers: { 'Content-Type': 'application/json' } })

async function signIn(user: { id: number }) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (url.endsWith('/api/login')) return json(user)
      return json({})
    }),
  )
  await session.login(user.id)
}

function at(path: string) {
  const router = createMemoryRouter(routes, { initialEntries: [path] })
  render(<RouterProvider router={router} />)
  return router
}

describe('settings addresses', () => {
  beforeEach(() => {
    session.reset()
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    vi.spyOn(console, 'error').mockImplementation(() => {})
  })
  afterEach(() => {
    cleanup()
    vi.restoreAllMocks()
    vi.unstubAllGlobals()
  })

  it.each([
    ['/tv/manage', '/manage/channels', ''],
    ['/tv/manage?new=1', '/manage/channels', '?new=1'],
    ['/tv/manage/sources', '/manage/channels/sources', ''],
    ['/tv/manage/sources/7', '/manage/channels/sources/7', ''],
    ['/tv/manage/franchises', '/manage/channels/franchises', ''],
    ['/manage/updates', '/manage/health', ''],
  ])('an admin at %s lands on %s', async (from, to, search) => {
    await signIn(admin)
    const router = at(from)
    await waitFor(() => expect(router.state.location.pathname).toBe(to))
    expect(router.state.location.search).toBe(search)
  })

  it('a profile that is not an admin keeps its channels in «ТВ»', async () => {
    await signIn(adult)
    const router = at('/tv/manage')
    await new Promise((r) => setTimeout(r, 50))
    expect(router.state.location.pathname).toBe('/tv/manage')
  })
})
