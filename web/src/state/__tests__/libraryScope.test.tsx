// «Моя медиатека» while the profile's addons are still loading: the scope
// stays the profile's own (no first paint of the whole library, no second
// fetch) and the can-add answer is not a «no» yet.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, render, waitFor } from '@testing-library/react'
import { session } from '../session'
import { useLibraryScope } from '../libraryScope'
import { usePendingCards } from '../pendingCards'
import { useCanAddState } from '../../addons/slots'
import { registerAddon, resetAddonsForTests } from '../../addons/registry'
import { importer, initAddons, resetLoaderForTests } from '../../addons/loader'

const mine = {
  id: 7,
  name: 'Мама',
  avatar: 'grad-3',
  hasPassword: false,
  isAdmin: false,
  isKid: false,
  maxRating: null,
  addons: true,
  libraryScope: 'mine' as const,
}

const json = (b: unknown) => new Response(JSON.stringify(b), { status: 200, headers: { 'Content-Type': 'application/json' } })

let addonList: unknown[] = []
const pendingAsks: string[] = []

function serve() {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => {
      if (url.startsWith('/api/addons/web')) return json(addonList)
      if (url.startsWith('/api/library/pending')) {
        pendingAsks.push(url)
        return json([])
      }
      return json(mine)
    }),
  )
}

const scopes: string[] = []
const answers: (boolean | undefined)[] = []

function Probe() {
  const { scope } = useLibraryScope()
  const canAdd = useCanAddState()
  usePendingCards()
  scopes.push(scope)
  answers.push(canAdd)
  return null
}

describe('the library scope while the addons load', () => {
  beforeEach(async () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    scopes.length = 0
    answers.length = 0
    pendingAsks.length = 0
    session.reset()
    serve()
    await session.login(mine.id)
  })
  afterEach(() => {
    session.reset()
    resetAddonsForTests()
    resetLoaderForTests()
    globalThis.__antresol = undefined
    vi.restoreAllMocks()
    vi.unstubAllGlobals()
  })

  it('a «mine» profile is «mine» from the first paint, fetched once, while the bundle is slow', async () => {
    addonList = [{ id: 'dl', entry: '/addons/dl/ui/index.js' }]
    let finish = () => {}
    vi.spyOn(importer, 'load').mockImplementation(
      () =>
        new Promise((resolve) => {
          finish = () => {
            registerAddon('dl', { searchBlocks: [{ element: () => null }] })
            resolve({})
          }
        }),
    )
    // the page renders before the loader starts (the Gate's effect runs after its children's)
    render(<Probe />)
    expect(scopes[0]).toBe('mine')
    expect(answers[0]).toBeUndefined()
    const loading = initAddons(mine)
    await waitFor(() => expect(importer.load).toHaveBeenCalled())
    await act(async () => {
      finish()
      await loading
    })
    expect(answers.at(-1)).toBe(true)
    expect(new Set(scopes)).toEqual(new Set(['mine']))
    expect(answers).not.toContain(false)
    await waitFor(() => expect(pendingAsks.length).toBeGreaterThan(0))
    expect(pendingAsks).toEqual(['/api/library/pending?scope=mine'])
  })

  it('once loaded without an addon that adds titles, the scope is the shared library', async () => {
    addonList = []
    render(<Probe />)
    expect(scopes[0]).toBe('mine')
    await act(async () => {
      await initAddons(mine)
    })
    expect(scopes.at(-1)).toBe('all')
    expect(answers.at(-1)).toBe(false)
  })
})
