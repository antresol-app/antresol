// The buttons addons put on an empty library page: only the page's scope,
// `admin: true` ones for admins, labels from the addon's strings, and an
// entry that throws takes only itself away.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { session } from '../../state/session'
import { registerAddon, resetAddonsForTests, setAddonUser } from '../../addons/registry'
import { EmptyStateActions } from '../EmptyStateActions'

const admin = { id: 1, name: 'Папа', avatar: 'grad-3', hasPassword: false, isAdmin: true, isKid: false, maxRating: null }
const adult = { ...admin, id: 2, name: 'Мама', isAdmin: false }
const kid = { ...adult, id: 3, name: 'Вася', isKid: true }

const json = (b: unknown) => new Response(JSON.stringify(b), { status: 200, headers: { 'Content-Type': 'application/json' } })

async function signIn(user: { id: number; isAdmin: boolean }) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => json(user)),
  )
  await session.login(user.id)
  act(() => setAddonUser({ isAdmin: user.isAdmin }))
}

const strings = { ru: { 'empty.find': 'Найти и добавить' }, en: { 'empty.find': 'Find and add' } }

const renderAt = (scope: 'home' | 'music') =>
  render(
    <MemoryRouter>
      <EmptyStateActions scope={scope} />
    </MemoryRouter>,
  )

describe('EmptyStateActions', () => {
  beforeEach(() => session.reset())
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
    session.reset()
    resetAddonsForTests()
  })

  it('shows the entries of its scope only', async () => {
    await signIn(admin)
    registerAddon('dl', {
      strings,
      emptyStateActions: [
        { scope: 'home', label: 'empty.find', to: '/search' },
        { scope: 'music', label: 'music', to: '/search?dk=music' },
      ],
    })
    renderAt('home')
    expect(screen.getAllByRole('link')).toHaveLength(1)
    expect(screen.getByRole('link', { name: 'Найти и добавить' })).toHaveAttribute('href', '/search')
  })

  it('a relative `to` leads under the addon', async () => {
    await signIn(admin)
    registerAddon('dl', { emptyStateActions: [{ scope: 'music', label: 'go', to: 'find' }] })
    renderAt('music')
    expect(screen.getByRole('link', { name: 'go' })).toHaveAttribute('href', '/addons/dl/find')
  })

  it('an admin-only entry is hidden for an adult', async () => {
    await signIn(adult)
    registerAddon('dl', {
      emptyStateActions: [
        { scope: 'home', label: 'admins', to: '/search', admin: true },
        { scope: 'home', label: 'all', to: '/search' },
      ],
    })
    renderAt('home')
    expect(screen.getAllByRole('link').map((a) => a.textContent)).toEqual(['all'])
  })

  it('a kid gets none', async () => {
    await signIn(kid)
    registerAddon('dl', { emptyStateActions: [{ scope: 'home', label: 'all', to: '/search' }] })
    renderAt('home')
    expect(screen.queryByRole('link')).not.toBeInTheDocument()
  })

  it('a throwing contribution renders nothing and keeps the other', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {})
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    await signIn(admin)
    // a bundle's malformed entry: its `to` is no string
    const bad = { scope: 'home' as const, label: 'bad', to: null as unknown as string }
    registerAddon('bad', { emptyStateActions: [bad] })
    registerAddon('dl', { emptyStateActions: [{ scope: 'home', label: 'all', to: '/search' }] })
    renderAt('home')
    expect(screen.getAllByRole('link').map((a) => a.textContent)).toEqual(['all'])
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})
