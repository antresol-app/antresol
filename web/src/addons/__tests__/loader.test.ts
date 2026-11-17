import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { renderHook } from '@testing-library/react'
import { registerAddon, useEvents } from '../sdk'
import { contributionsOf, resetAddonsForTests } from '../registry'
import { importer, initAddons, reloadAddons, resetLoaderForTests } from '../loader'

const admin = { id: 1, isAdmin: true }
const kid = { id: 2, isAdmin: false, isKid: true }
const adult = { id: 3, isAdmin: false, isKid: false }

function serve(list: unknown) {
  const fetchMock = vi.fn(async () => new Response(JSON.stringify(list), { status: 200, headers: { 'Content-Type': 'application/json' } }))
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}

describe('addon loader', () => {
  beforeEach(() => vi.spyOn(console, 'warn').mockImplementation(() => {}))
  afterEach(() => {
    resetAddonsForTests()
    resetLoaderForTests()
    globalThis.__antresol = undefined
    vi.restoreAllMocks()
    vi.unstubAllGlobals()
  })

  it('a failing entry is skipped, the other one registers', async () => {
    serve([
      { id: 'good', entry: '/addons/good/ui/index.js' },
      { id: 'bad', entry: '/addons/bad/ui/index.js' },
    ])
    const load = vi.spyOn(importer, 'load').mockImplementation(async (url: string) => {
      if (url.includes('bad')) throw new Error('boom')
      registerAddon('good', { manageTiles: [{ label: 'G', icon: 'gear', to: '/addons/good/' }] })
      return {}
    })
    await expect(initAddons(admin)).resolves.toBeUndefined()
    expect(load).toHaveBeenCalledTimes(2)
    expect(contributionsOf('good')?.manageTiles?.[0]?.label).toBe('G')
    expect(contributionsOf('bad')).toBeUndefined()
    expect(console.warn).toHaveBeenCalled()
    const shared = globalThis.__antresol?.shared ?? {}
    expect(shared['@antresol/addon-sdk']).toHaveProperty('registerAddon')
    expect(shared['react-router']).toHaveProperty('useNavigate')
    expect(shared['react/jsx-runtime']).toHaveProperty('jsx')
  })

  it('an empty list imports nothing', async () => {
    const fetchMock = serve([])
    const load = vi.spyOn(importer, 'load')
    await initAddons(admin)
    expect(fetchMock).toHaveBeenCalledWith('/api/addons/web', expect.anything())
    expect(load).not.toHaveBeenCalled()
    // no bundle to serve: the shared runtime (React, router, SDK) is not even fetched
    expect(globalThis.__antresol).toBeUndefined()
  })

  it('runs once per profile', async () => {
    const fetchMock = serve([])
    await initAddons(admin)
    await initAddons(admin)
    expect(fetchMock).toHaveBeenCalledTimes(1)
    await initAddons({ ...admin, id: admin.id + 100 })
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })

  it('a kid, or a server without addons, does not even ask', async () => {
    const fetchMock = serve([{ id: 'good', entry: '/addons/good/ui/index.js' }])
    const load = vi.spyOn(importer, 'load')
    await initAddons(kid)
    await initAddons({ ...admin, addons: false })
    expect(fetchMock).not.toHaveBeenCalled()
    expect(load).not.toHaveBeenCalled()
  })

  it('an adult loads what the server lists for them (the adult addons), and no admin events', async () => {
    const fetchMock = serve([{ id: 'fam', entry: '/addons/fam/ui/index.js' }])
    const load = vi.spyOn(importer, 'load').mockImplementation(async () => {
      registerAddon('fam', { events: ['progress'], manageTiles: [{ label: 'F', icon: 'gear', to: 'page' }] })
      return {}
    })
    await initAddons(adult)
    expect(fetchMock).toHaveBeenCalledWith('/api/addons/web', expect.anything())
    expect(load).toHaveBeenCalledWith('/addons/fam/ui/index.js')
    expect(contributionsOf('fam')?.manageTiles?.[0]?.label).toBe('F')
    const types: string[] = []
    vi.stubGlobal(
      'EventSource',
      class {
        onmessage: unknown = null
        addEventListener(t: string) {
          types.push(t)
        }
        close() {}
      },
    )
    const { unmount } = renderHook(() => useEvents(() => {}, ['addon.fam.progress']))
    expect(types).toContain('addon.fam.progress')
    expect(types).not.toContain('addon.fam.admin.progress')
    unmount()
  })

  it("a profile switch drops the previous profile's addons, even when they were cached", async () => {
    serve([{ id: 'good', entry: '/addons/good/ui/index.js' }])
    vi.spyOn(importer, 'load').mockImplementation(async () => {
      registerAddon('good', { manageTiles: [{ label: 'G', icon: 'gear', to: '/addons/good/' }] })
      return {}
    })
    await initAddons(admin)
    expect(contributionsOf('good')).toBeDefined()
    serve([])
    await initAddons(kid)
    expect(contributionsOf('good')).toBeUndefined()
  })

  it("listens for the addon's events by their full names (admin ones for admins)", async () => {
    serve([{ id: 'good', entry: '/addons/good/ui/index.js' }])
    vi.spyOn(importer, 'load').mockImplementation(async () => {
      registerAddon('good', { events: ['progress'] })
      return {}
    })
    await initAddons(admin)
    const types: string[] = []
    vi.stubGlobal(
      'EventSource',
      class {
        onmessage: unknown = null
        addEventListener(t: string) {
          types.push(t)
        }
        close() {}
      },
    )
    const { unmount } = renderHook(() => useEvents(() => {}, ['addon.good.progress']))
    expect(types).toContain('addon.good.progress')
    expect(types).toContain('addon.good.admin.progress')
    unmount()
  })

  it('reloadAddons loads the same profile again: a new addon joins, a gone one leaves', async () => {
    serve([{ id: 'good', entry: '/addons/good/ui/index.js' }])
    vi.spyOn(importer, 'load').mockImplementation(async (url: string) => {
      const id = url.split('/')[2]!
      registerAddon(id, { manageTiles: [{ label: id, icon: 'gear', to: `/addons/${id}/` }] })
      return {}
    })
    await initAddons(admin)
    expect(contributionsOf('good')).toBeDefined()
    const fetchMock = serve([{ id: 'other', entry: '/addons/other/ui/index.js' }])
    await reloadAddons()
    expect(fetchMock).toHaveBeenCalledWith('/api/addons/web', expect.anything())
    expect(contributionsOf('other')).toBeDefined()
    expect(contributionsOf('good')).toBeUndefined()
  })

  it('after an update the reload imports the new entry and its contributions replace the old ones', async () => {
    serve([{ id: 'good', entry: '/addons/good/ui/index.js?v=1.0.0' }])
    const load = vi.spyOn(importer, 'load').mockImplementation(async (url: string) => {
      const v = new URL(url, 'http://x').searchParams.get('v')!
      registerAddon('good', { manageTiles: [{ label: `G ${v}`, icon: 'gear', to: '/addons/good/' }] })
      return {}
    })
    await initAddons(admin)
    expect(contributionsOf('good')?.manageTiles?.map((t) => t.label)).toEqual(['G 1.0.0'])
    serve([{ id: 'good', entry: '/addons/good/ui/index.js?v=1.1.0' }])
    await reloadAddons()
    expect(load).toHaveBeenLastCalledWith('/addons/good/ui/index.js?v=1.1.0')
    expect(contributionsOf('good')?.manageTiles?.map((t) => t.label)).toEqual(['G 1.1.0'])
  })

  it('reloadAddons before any profile signed in does nothing', async () => {
    const fetchMock = serve([])
    await reloadAddons()
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('a server error leaves no addons and does not throw', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('nope', { status: 500 })))
    const load = vi.spyOn(importer, 'load')
    await expect(initAddons(admin)).resolves.toBeUndefined()
    expect(load).not.toHaveBeenCalled()
  })

  it('links an addon\'s stylesheet once, versioned, and drops it when the addon goes', async () => {
    const links = () => [...document.head.querySelectorAll<HTMLLinkElement>('link[data-addon]')].map((l) => `${l.dataset.addon} ${l.rel} ${l.getAttribute('href')}`)
    vi.spyOn(importer, 'load').mockResolvedValue({})
    serve([
      { id: 'styled', entry: '/addons/styled/ui/index.js?v=1', style: '/addons/styled/ui/index.css?v=1' },
      { id: 'plain', entry: '/addons/plain/ui/index.js?v=1' },
    ])
    await initAddons(admin)
    expect(links()).toEqual(['styled stylesheet /addons/styled/ui/index.css?v=1'])
    // the same list again: still one link
    await reloadAddons()
    expect(links()).toEqual(['styled stylesheet /addons/styled/ui/index.css?v=1'])
    // an update: a new versioned URL replaces the old link
    serve([{ id: 'styled', entry: '/addons/styled/ui/index.js?v=2', style: '/addons/styled/ui/index.css?v=2' }])
    await reloadAddons()
    expect(links()).toEqual(['styled stylesheet /addons/styled/ui/index.css?v=2'])
    // removed: its link goes
    serve([])
    await reloadAddons()
    expect(links()).toEqual([])
  })

  it('a kid gets no addon stylesheet (nor the list)', async () => {
    const fetchMock = serve([{ id: 'styled', entry: '/addons/styled/ui/index.js', style: '/addons/styled/ui/index.css' }])
    await initAddons(kid)
    expect(fetchMock).not.toHaveBeenCalled()
    expect(document.head.querySelectorAll('link[data-addon]')).toHaveLength(0)
  })

  it('the stylesheet never holds up the script', async () => {
    // jsdom never loads a <link>: the entry still runs and registers
    serve([{ id: 'styled', entry: '/addons/styled/ui/index.js', style: '/addons/styled/ui/index.css' }])
    vi.spyOn(importer, 'load').mockImplementation(async () => {
      registerAddon('styled', { manageTiles: [{ label: 'S', icon: 'gear', to: 'x' }] })
      return {}
    })
    await initAddons(admin)
    expect(contributionsOf('styled')?.manageTiles?.[0]?.label).toBe('S')
  })
})
