// The profile menu's addon entries (profileMenu): for adults (labels the
// addon's strings, `to` resolved), none for a kid; visible(user) hides one,
// a throwing one only itself. A library-only profile gets them too:
// libraryOnly gates online only (a library-only server lists no addon).
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router'
import type { User } from '../../api/types'
import { registerAddon, resetAddonsForTests } from '../../addons/registry'
import { session } from '../../state/session'
import { ProfileMenu } from '../ProfileMenu'

const adult: User = { id: 2, name: 'Мама', avatar: 'grad-3', hasPassword: true, isAdmin: false, isKid: false, maxRating: null }
const admin: User = { ...adult, id: 1, name: 'Папа', isAdmin: true }
const kid: User = { ...adult, id: 3, name: 'Вася', isKid: true }
const json = (b: unknown) => new Response(JSON.stringify(b), { status: 200, headers: { 'Content-Type': 'application/json' } })

function Where() {
  return <span data-testid="where">{useLocation().pathname}</span>
}

async function openMenu(u: User) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => (String(input).endsWith('/api/login') ? json(u) : json({}))),
  )
  await session.login(u.id)
  const r = render(
    <MemoryRouter>
      <ProfileMenu user={u} />
      <Routes>
        <Route path="*" element={<Where />} />
      </Routes>
    </MemoryRouter>,
  )
  fireEvent.click(screen.getByRole('button', { name: /Профиль|Администратор|Ребёнок|Мама|Папа|Вася/ }))
  return r
}

function registerFake() {
  registerAddon('fake', {
    strings: { ru: { 'menu.mine': 'Мои подборки' }, en: {} },
    profileMenu: [
      { label: 'menu.mine', icon: 'eye', to: 'mine' },
      { label: 'Только не админу', icon: 'eye', to: '/elsewhere', visible: (u) => !u.isAdmin },
      {
        label: 'Сломанная',
        icon: 'eye',
        to: 'x',
        visible: () => {
          throw new Error('boom')
        },
      },
    ],
  })
}

describe('the profile menu’s addon entries', () => {
  beforeEach(() => {
    session.reset()
    resetAddonsForTests()
  })
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it('addon entries for adults, none for kids; a library-only profile gets them', async () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    registerFake()
    const first = await openMenu(adult)
    fireEvent.click(screen.getByRole('menuitem', { name: 'Мои подборки' }))
    expect(screen.getByTestId('where')).toHaveTextContent('/addons/fake/mine')
    first.unmount()

    session.reset()
    const kidMenu = await openMenu(kid)
    expect(screen.queryByRole('menuitem', { name: 'Мои подборки' })).toBeNull()
    kidMenu.unmount()

    session.reset()
    await openMenu({ ...adult, libraryOnly: true })
    expect(screen.getByRole('menuitem', { name: 'Мои подборки' })).toBeInTheDocument()
  })

  it('visible() hides an entry', async () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    registerFake()
    const first = await openMenu(adult)
    fireEvent.click(screen.getByRole('menuitem', { name: 'Только не админу' }))
    expect(screen.getByTestId('where')).toHaveTextContent('/elsewhere')
    // a throwing visible() hides only its own entry
    expect(screen.queryByRole('menuitem', { name: 'Сломанная' })).toBeNull()
    first.unmount()

    session.reset()
    await openMenu(admin)
    expect(screen.queryByRole('menuitem', { name: 'Только не админу' })).toBeNull()
    expect(screen.getByRole('menuitem', { name: 'Мои подборки' })).toBeInTheDocument()
  })

  it('names this device’s separate history', async () => {
    await openMenu({ ...adult, deviceHistory: { id: 9, name: 'Гостиная' } })
    expect(await screen.findByText('История этого устройства: Гостиная')).toBeInTheDocument()
  })

  it('«Сменить профиль» is the round button in the head, reached with the arrows but not focused first', async () => {
    await openMenu(adult)
    const sw = screen.getByRole('menuitem', { name: 'Сменить профиль' })
    // in the head beside the profile's name, not a row among the places
    expect(sw.closest('[role="menu"]')!.querySelector('[class*="head"]')).toContainElement(sw)
    // the menu opens on its first place, not on the switch
    expect(document.activeElement).toHaveAccessibleName('Календарь')
    fireEvent.click(sw)
    expect(await screen.findByTestId('where')).toHaveTextContent('/profiles')
  })
})
