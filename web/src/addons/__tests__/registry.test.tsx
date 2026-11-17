import { afterEach, describe, expect, it, vi } from 'vitest'
import { act, render, screen } from '@testing-library/react'
import { registerAddon } from '../sdk'
import { contributionsOf, resetAddonsForTests, setActiveAddons, setAddonUser, useContributions } from '../registry'

function Tiles() {
  const tiles = useContributions('manageTiles')
  return <p>{tiles.map((t) => `${t.addonId}:${t.label}`).join(',')}</p>
}

describe('addon slot registry', () => {
  afterEach(() => resetAddonsForTests())

  it('lists the tiles of every addon in id order and re-renders on registration', () => {
    render(<Tiles />)
    expect(screen.getByRole('paragraph').textContent).toBe('')
    act(() => registerAddon('zeta', { manageTiles: [{ label: 'Z', icon: 'gear', to: '/addons/zeta/' }] }))
    act(() => registerAddon('alpha', { manageTiles: [{ label: 'A', icon: 'gear', to: '/addons/alpha/' }] }))
    expect(screen.getByRole('paragraph').textContent).toBe('alpha:A,zeta:Z')
  })

  it('a second registration replaces the first', () => {
    render(<Tiles />)
    act(() => registerAddon('alpha', { manageTiles: [{ label: 'A', icon: 'gear', to: '/a' }] }))
    act(() => registerAddon('alpha', { manageTiles: [{ label: 'B', icon: 'gear', to: '/b' }] }))
    expect(screen.getByRole('paragraph').textContent).toBe('alpha:B')
    expect(contributionsOf('alpha')?.manageTiles?.[0]?.to).toBe('/b')
  })

  it('shows only the addons active for the signed-in profile', () => {
    render(<Tiles />)
    act(() => {
      registerAddon('alpha', { manageTiles: [{ label: 'A', icon: 'gear', to: '/a' }] })
      registerAddon('beta', { manageTiles: [{ label: 'B', icon: 'gear', to: '/b' }] })
    })
    act(() => setActiveAddons(['beta']))
    expect(screen.getByRole('paragraph').textContent).toBe('beta:B')
    expect(contributionsOf('alpha')).toBeUndefined()
  })

  it('an admin: true entry shows to admins only', () => {
    act(() => setAddonUser({ isAdmin: false }))
    render(<Tiles />)
    act(() =>
      registerAddon('alpha', {
        manageTiles: [
          { label: 'Всем', icon: 'gear', to: '/a' },
          { label: 'Админу', icon: 'gear', to: '/b', admin: true },
        ],
      }),
    )
    expect(screen.getByRole('paragraph').textContent).toBe('alpha:Всем')
    act(() => setAddonUser({ isAdmin: true }))
    expect(screen.getByRole('paragraph').textContent).toBe('alpha:Всем,alpha:Админу')
  })

  it('drops a route under ui/ (the bundle\'s own files live there) with a warning', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const element = () => <p />
    registerAddon('alpha', {
      routes: [
        { path: 'ui/index.js', element, title: 'Плохо' },
        { path: '/ui/x', element, title: 'Тоже плохо' },
        { path: 'panel', element, title: 'Панель' },
        { path: 'uix', element, title: 'Можно' },
      ],
    })
    expect(contributionsOf('alpha')?.routes?.map((r) => r.path)).toEqual(['panel', 'uix'])
    expect(warn).toHaveBeenCalledTimes(2)
    warn.mockRestore()
  })

  it('admin-only entries of the new slots', () => {
    const el = () => <p />
    registerAddon('dl', {
      navBadges: [
        { to: 'list', icon: 'gear', label: 'badge', useCount: () => 1 },
        { to: 'admin', icon: 'gear', label: 'adminBadge', useCount: () => 1, admin: true },
      ],
      searchBlocks: [{ element: el }, { element: el, admin: true }],
      emptyStateActions: [
        { scope: 'home', label: 'add', to: '/search' },
        { scope: 'music', label: 'adminAdd', to: 'find', admin: true },
      ],
      requestActions: [{ element: el }, { element: el, admin: true }],
      statsBlocks: [{ element: el }, { element: el, admin: true }],
    })
    const slots = ['navBadges', 'searchBlocks', 'emptyStateActions', 'requestActions', 'statsBlocks'] as const
    function Counts() {
      const counts = slots.map((k) => useContributions(k).length)
      return <p>{counts.join(',')}</p>
    }
    act(() => setAddonUser({ isAdmin: false }))
    render(<Counts />)
    expect(screen.getByRole('paragraph').textContent).toBe('1,1,1,1,1')
    act(() => setAddonUser({ isAdmin: true }))
    expect(screen.getByRole('paragraph').textContent).toBe('2,2,2,2,2')
  })

  it('admin-only entries of the 2e slots', () => {
    const el = () => <p />
    registerAddon('alpha', {
      itemPanels: [{ element: el }, { element: el, admin: true }],
      episodeActions: [
        { kinds: ['alpha'], use: () => null },
        { kinds: ['alpha-admin'], use: () => null, admin: true },
      ],
      playerTrackActions: [
        { label: 'track', run: () => {} },
        { label: 'adminTrack', run: () => {}, admin: true },
      ],
      profileMenu: [
        { label: 'menu', icon: 'gear', to: 'mine' },
        { label: 'adminMenu', icon: 'gear', to: 'all', admin: true },
      ],
    })
    const slots = ['itemPanels', 'episodeActions', 'playerTrackActions', 'profileMenu'] as const
    function Counts() {
      const counts = slots.map((k) => useContributions(k).length)
      return <p>{counts.join(',')}</p>
    }
    act(() => setAddonUser({ isAdmin: false }))
    render(<Counts />)
    expect(screen.getByRole('paragraph').textContent).toBe('1,1,1,1')
    act(() => setAddonUser({ isAdmin: true }))
    expect(screen.getByRole('paragraph').textContent).toBe('2,2,2,2')
  })
})
