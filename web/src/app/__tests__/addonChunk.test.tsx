// Entering an addon's fullWindow route while the AddonPage chunk still
// downloads: the player's black screen, never the section spinner.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, render, screen, waitFor } from '@testing-library/react'
import { RouterProvider, createMemoryRouter } from 'react-router'
import { session } from '../../state/session'
import { registerAddon, resetAddonsForTests, useContributions } from '../../addons/registry'
import { importer, resetLoaderForTests } from '../../addons/loader'

// the AddonPage chunk is held until the test lets it come
const chunk = vi.hoisted(() => {
  let open!: () => void
  const gate = new Promise<void>((r) => (open = r))
  return { gate, open: () => open() }
})
vi.mock('../../pages/AddonPage', async (orig) => {
  await chunk.gate
  return orig()
})

import { routes } from '../routes'

const admin = { id: 1, name: 'Папа', avatar: 'grad-3', hasPassword: false, isAdmin: true, isKid: false, maxRating: null, addons: true }
const json = (b: unknown) => new Response(JSON.stringify(b), { status: 200, headers: { 'Content-Type': 'application/json' } })

function Probe() {
  // the addon registered: the route is known before the chunk comes
  return <span data-testid="routes">{useContributions('routes').length}</span>
}

describe('a fullWindow addon route whose page chunk is pending', () => {
  afterEach(() => {
    cleanup()
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
    session.reset()
    resetAddonsForTests()
    resetLoaderForTests()
    globalThis.__antresol = undefined
  })

  it('shows the player fallback, never the spinner', async () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    vi.spyOn(console, 'error').mockImplementation(() => {})
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input)
        if (url.endsWith('/api/login')) return json(admin)
        if (url.endsWith('/api/addons/web')) return json([{ id: 'hello', entry: '/addons/hello/ui/index.js' }])
        return json({})
      }),
    )
    await session.login(1)
    vi.spyOn(importer, 'load').mockImplementation(async () => {
      registerAddon('hello', {
        routes: [{ path: 'play/:id', title: 'Плеер', fullWindow: true, element: ({ params }) => <h1>Плеер {params.id}</h1> }],
      })
      return {}
    })
    const router = createMemoryRouter(routes, { initialEntries: ['/settings'] })
    const { container } = render(
      <>
        <Probe />
        <RouterProvider router={router} />
      </>,
    )
    await waitFor(() => expect(screen.getByTestId('routes')).toHaveTextContent('1'))
    // «Смотреть» on a core page: into the addon's player
    await act(() => router.navigate('/addons/hello/play/7'))
    await waitFor(() => expect(container.querySelector('.playerFallback')).not.toBeNull())
    expect(container.querySelector('.spinner')).toBeNull()
    expect(document.getElementById('main')).toBeNull()
    act(() => chunk.open())
    expect(await screen.findByRole('heading', { name: 'Плеер 7' })).toBeInTheDocument()
    expect(container.querySelector('.spinner')).toBeNull()
  })
})
