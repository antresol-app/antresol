// The counts addons put beside the navigation (navBadges): hidden at 0,
// capped at 99+, a link to the addon's page, and a count that throws takes
// only its own badge away.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { session } from '../../state/session'
import { registerAddon, resetAddonsForTests, setAddonUser } from '../../addons/registry'
import { NavBadges } from '../NavBadges'

const admin = { id: 1, name: 'Папа', avatar: 'grad-3', hasPassword: false, isAdmin: true, isKid: false, maxRating: null }
const kid = { ...admin, id: 3, name: 'Вася', isAdmin: false, isKid: true }

const json = (b: unknown) => new Response(JSON.stringify(b), { status: 200, headers: { 'Content-Type': 'application/json' } })

async function signIn(user: { id: number; isAdmin: boolean }) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => json(user)),
  )
  await session.login(user.id)
  act(() => setAddonUser({ isAdmin: user.isAdmin }))
}

const strings = { ru: { 'badge.label': 'В работе: {count}' }, en: { 'badge.label': 'In progress: {count}' } }

function renderBadges() {
  return render(
    <MemoryRouter>
      <NavBadges />
    </MemoryRouter>,
  )
}

describe('NavBadges', () => {
  beforeEach(() => session.reset())
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
    session.reset()
    resetAddonsForTests()
  })

  it('a count of 0 is hidden', async () => {
    await signIn(admin)
    registerAddon('dl', { strings, navBadges: [{ to: 'list', icon: 'download', label: 'badge.label', useCount: () => 0 }] })
    renderBadges()
    expect(screen.queryByRole('link')).not.toBeInTheDocument()
  })

  it('120 shows as 99+, links to the resolved path, the label takes the count', async () => {
    await signIn(admin)
    registerAddon('dl', { strings, navBadges: [{ to: 'list', icon: 'download', label: 'badge.label', useCount: () => 120 }] })
    renderBadges()
    const link = screen.getByRole('link', { name: 'В работе: 120' })
    expect(link).toHaveAttribute('href', '/addons/dl/list')
    expect(link.textContent).toBe('99+')
  })

  it('an app path stays as it is', async () => {
    await signIn(admin)
    registerAddon('dl', { navBadges: [{ to: '/manage', icon: 'download', label: 'plain', useCount: () => 3 }] })
    renderBadges()
    expect(screen.getByRole('link', { name: 'plain' })).toHaveAttribute('href', '/manage')
  })

  it('a throwing useCount renders nothing and keeps the other', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {})
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    await signIn(admin)
    registerAddon('bad', {
      navBadges: [
        {
          to: 'x',
          icon: 'download',
          label: 'bad',
          useCount: () => {
            throw new Error('boom')
          },
        },
      ],
    })
    registerAddon('dl', { strings, navBadges: [{ to: 'list', icon: 'download', label: 'badge.label', useCount: () => 2 }] })
    renderBadges()
    expect(screen.getAllByRole('link')).toHaveLength(1)
    expect(screen.getByRole('link', { name: 'В работе: 2' })).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('a kid gets no badge', async () => {
    await signIn(kid)
    registerAddon('dl', { navBadges: [{ to: 'list', icon: 'download', label: 'b', useCount: () => 5 }] })
    renderBadges()
    expect(screen.queryByRole('link')).not.toBeInTheDocument()
  })
})
