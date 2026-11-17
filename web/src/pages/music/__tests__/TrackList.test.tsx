// A playlist's songs not brought yet: where the server fetches nothing each
// says «Нет в медиатеке»; where it does, why it is not here yet. For an
// admin the server's own answer decides (GET /api/import/services
// `downloads`); for another profile, whether an addon adds titles.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { registerAddon, resetAddonsForTests } from '../../../addons/registry'
import { session } from '../../../state/session'
import { pendingLabel, TrackList } from '../TrackList'
import { json } from './fixtures'

const adult = { id: 2, name: 'Мама', avatar: 'grad-3', hasPassword: false, isAdmin: false, isKid: false, maxRating: null }

const admin = { ...adult, id: 1, name: 'Папа', isAdmin: true }

const row = { position: 0, artist: 'Кино', title: 'Кукушка', state: 'waiting_download', waitingFor: 'Кино' }
const show = (pending = [row]) =>
  render(
    <MemoryRouter>
      <TrackList tracks={[]} albumArtist="" onPlay={() => {}} variant="songs" pending={pending} />
    </MemoryRouter>,
  )

/** Signs `user` in; /api/import/services answers `downloads` (null: it never answers). */
async function signIn(user: object, downloads: boolean | null = false) {
  const f = vi.fn(async (input: RequestInfo | URL) => {
    if (String(input).endsWith('/api/import/services')) {
      if (downloads === null) return new Promise<Response>(() => {})
      return json({ services: [], downloads, catalog: false })
    }
    return json(user)
  })
  vi.stubGlobal('fetch', f)
  await session.login((user as { id: number }).id)
  return f
}
const servicesCalls = (f: ReturnType<typeof vi.fn>) => f.mock.calls.filter(([u]) => String(u).endsWith('/api/import/services')).length

describe('TrackList pending rows', () => {
  beforeEach(async () => {
    session.reset()
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => json(adult)),
    )
    await session.login(2)
  })
  afterEach(() => {
    vi.unstubAllGlobals()
    session.reset()
    resetAddonsForTests()
  })

  it('local labels without an addon', () => {
    show()
    expect(screen.getByText('Нет в медиатеке')).toBeInTheDocument()
  })

  it('with an addon that adds titles: why it is not here yet', () => {
    registerAddon('dl', { searchBlocks: [{ element: () => null }] })
    show()
    expect(screen.getByText('Ждёт загрузку «Кино»')).toBeInTheDocument()
  })

  it('while the addons still load: not «Нет в медиатеке» first (a pending row means an addon brings it)', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => json({ ...adult, addons: true })),
    )
    await session.login(2)
    show()
    expect(screen.queryByText('Нет в медиатеке')).not.toBeInTheDocument()
    expect(screen.getByText('Ждёт загрузку «Кино»')).toBeInTheDocument()
  })

  it('an admin: the server downloads nothing (no addon answers), so «Нет в медиатеке» even beside an addon that adds titles', async () => {
    await signIn(admin, false)
    registerAddon('dl', { searchBlocks: [{ element: () => null }] })
    show()
    expect(await screen.findByText('Нет в медиатеке')).toBeInTheDocument()
  })

  it('an admin: the server downloads, so why it is not here yet', async () => {
    const f = await signIn(admin, true)
    show([row, { ...row, position: 1, title: 'Звезда' }])
    await waitFor(() => expect(servicesCalls(f)).toBe(1))
    expect(screen.getAllByText('Ждёт загрузку «Кино»')).toHaveLength(2)
    expect(screen.queryByText('Нет в медиатеке')).not.toBeInTheDocument()
  })

  it('an admin, while the server has not answered: not «Нет в медиатеке» first', async () => {
    await signIn(admin, null)
    show()
    expect(screen.queryByText('Нет в медиатеке')).not.toBeInTheDocument()
    expect(screen.getByText('Ждёт загрузку «Кино»')).toBeInTheDocument()
  })

  it('no pending rows, or not an admin: the server is not asked', async () => {
    const f = await signIn(admin, true)
    show([])
    const g = await signIn(adult, true)
    show()
    await new Promise((r) => setTimeout(r, 20))
    expect(servicesCalls(f)).toBe(0)
    expect(servicesCalls(g)).toBe(0)
  })

  it('pendingLabel: `local` decides', () => {
    expect(pendingLabel('waiting_download', 'Кино', true)).toBe('Нет в медиатеке')
    expect(pendingLabel('waiting_download', 'Кино', false)).toBe('Ждёт загрузку «Кино»')
    expect(pendingLabel('skipped', undefined, false)).toBe('Пропущено')
  })
})
