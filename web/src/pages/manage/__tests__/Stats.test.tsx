// «Статистика»: the addons' blocks (statsBlocks) sit under the library's
// tiles, each in its own boundary.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import type { Stats as Data } from '../../../api/types'
import { registerAddon, resetAddonsForTests, setAddonUser } from '../../../addons/registry'
import { session } from '../../../state/session'
import { Stats } from '../Stats'

const admin = { id: 1, name: 'Папа', avatar: 'grad-1', hasPassword: false, isAdmin: true, isKid: false, maxRating: null }
const json = (b: unknown) => new Response(JSON.stringify(b), { status: 200, headers: { 'Content-Type': 'application/json' } })

const stats: Data = {
  disk: { total: 0, used: 0, free: 0, categories: [] },
  counts: { movies: 1, series: 0, episodes: 0, albums: 0, tracks: 0, books: 0, audiobooks: 0, comics: 0 },
  watchedByUser: [],
  topItems30d: [],
}

async function show() {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => (String(input).endsWith('/api/login') ? json(admin) : json(stats))),
  )
  await session.login(1)
  act(() => setAddonUser({ isAdmin: true }))
  render(
    <MemoryRouter>
      <Stats />
    </MemoryRouter>,
  )
}

describe('Stats', () => {
  beforeEach(() => session.reset())
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
    session.reset()
    resetAddonsForTests()
  })

  it('renders stats blocks', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {})
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    registerAddon('bad', {
      statsBlocks: [
        {
          element: () => {
            throw new Error('boom')
          },
        },
      ],
    })
    registerAddon('dl', { statsBlocks: [{ element: () => <p>блок статистики</p>, admin: true }] })
    await show()
    expect(await screen.findByText('блок статистики')).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('without addons: the tiles, no block, and no section with nothing in it', async () => {
    await show()
    expect(await screen.findByText('фильм')).toBeInTheDocument()
    expect(screen.queryByText('Кто сколько смотрел за 30 дней')).not.toBeInTheDocument()
    expect(screen.queryByText('Смотрели чаще всего за 30 дней')).not.toBeInTheDocument()
  })
})
