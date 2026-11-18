// The core's side of the 2d slots: whether the profile can add titles from
// outside the library (a search block is there for it) and where an
// addon's relative link leads.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, render, screen } from '@testing-library/react'
import { useCallback, useMemo, useState } from 'react'
import type { EpisodeCtx, EpisodePlay } from '../../components/episodes/episodeTypes'
import { session } from '../../state/session'
import { bestSource } from '../../state/pickSource'
import { registerAddon, resetAddonsForTests, setAddonUser } from '../registry'
import { addonRest, matchAddonRoute, resolveAddonPath, useCanAdd, useEpisodeActions, useProfileMenu, useSourceKinds } from '../slots'

const adult = { id: 2, name: 'Мама', avatar: 'grad-3', hasPassword: false, isAdmin: false, isKid: false, maxRating: null }
const admin = { ...adult, id: 1, name: 'Папа', isAdmin: true }
const kid = { ...adult, id: 3, name: 'Вася', isKid: true }

const json = (b: unknown) => new Response(JSON.stringify(b), { status: 200, headers: { 'Content-Type': 'application/json' } })

async function signIn(user: { id: number }) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => json(user)),
  )
  await session.login(user.id)
}

function CanAdd() {
  return <p>{useCanAdd() ? 'yes' : 'no'}</p>
}

const block = { element: () => <p>block</p> }

describe('useCanAdd follows searchBlocks', () => {
  beforeEach(() => session.reset())
  afterEach(() => {
    vi.unstubAllGlobals()
    session.reset()
    resetAddonsForTests()
  })

  it('no search block: false; one registered: true', async () => {
    await signIn(admin)
    act(() => setAddonUser({ isAdmin: true }))
    render(<CanAdd />)
    expect(screen.getByRole('paragraph').textContent).toBe('no')
    act(() => registerAddon('dl', { searchBlocks: [block] }))
    expect(screen.getByRole('paragraph').textContent).toBe('yes')
  })

  it('a kid never can', async () => {
    await signIn(kid)
    registerAddon('dl', { searchBlocks: [block] })
    render(<CanAdd />)
    expect(screen.getByRole('paragraph').textContent).toBe('no')
  })

  it('an admin: true block does not count for an adult', async () => {
    await signIn(adult)
    act(() => setAddonUser({ isAdmin: false }))
    registerAddon('dl', { searchBlocks: [{ ...block, admin: true }] })
    render(<CanAdd />)
    expect(screen.getByRole('paragraph').textContent).toBe('no')
  })
})

describe('resolveAddonPath', () => {
  it('an app path stays, a relative one is under the addon', () => {
    expect(resolveAddonPath('dl', '/search?dk=music')).toBe('/search?dk=music')
    expect(resolveAddonPath('dl', 'downloads')).toBe('/addons/dl/downloads')
  })
})

const libraryOnly = { ...adult, id: 4, name: 'Гость', libraryOnly: true }

describe('useEpisodeActions', () => {
  beforeEach(() => session.reset())
  afterEach(() => {
    vi.unstubAllGlobals()
    session.reset()
    resetAddonsForTests()
  })

  const ctx: EpisodeCtx = { kind: 'series', tmdbId: 7, title: 'Сериал', episodes: [] }

  /** A guide's stand-in: one Host per contribution, the answers lifted into state by kind. */
  function Guide({ renders }: { renders?: { n: number } }) {
    const actions = useEpisodeActions()
    const [plays, setPlays] = useState<Record<string, EpisodePlay | null>>({})
    const onPlay = useCallback((kind: string, p: EpisodePlay | null) => setPlays((m) => ({ ...m, [kind]: p })), [])
    const c = useMemo(() => ctx, [])
    if (renders) renders.n++
    return (
      <>
        {actions.map((a, i) => (
          <a.Host key={i} ctx={c} onPlay={onPlay} />
        ))}
        <p>
          {Object.entries(plays)
            .filter(([, p]) => p)
            .map(([k]) => k)
            .sort()
            .join(',')}
        </p>
      </>
    )
  }

  const play = (tag: string) => (): EpisodePlay => ({ play: () => {}, other: () => {}, node: <span>{tag}</span> })

  it('a contribution registered after the first render is picked up without a hooks error', async () => {
    const error = vi.spyOn(console, 'error').mockImplementation(() => {})
    await signIn(adult)
    registerAddon('alpha', { episodeActions: [{ kinds: ['alpha'], use: play('узел альфы') }] })
    const renders = { n: 0 }
    render(<Guide renders={renders} />)
    expect(screen.getByRole('paragraph').textContent).toBe('alpha')
    expect(screen.getByText('узел альфы')).toBeInTheDocument()
    act(() =>
      registerAddon('beta', {
        episodeActions: [
          {
            kinds: ['beta', 'beta-2'],
            // a hook of its own: the core calls it in a component of its own
            use: (c) => {
              const [n] = useState(c.tmdbId)
              return n === 7 ? play('узел беты')() : null
            },
          },
        ],
      }),
    )
    expect(screen.getByRole('paragraph').textContent).toBe('alpha,beta,beta-2')
    expect(screen.getByText('узел беты')).toBeInTheDocument()
    expect(error).not.toHaveBeenCalled()
    // a new answer object each render does not loop
    expect(renders.n).toBeLessThan(20)
    error.mockRestore()
  })

  it('a Host that goes says (kind, null) for each of its kinds', async () => {
    await signIn(adult)
    registerAddon('alpha', { episodeActions: [{ kinds: ['alpha', 'alpha-2'], use: play('a') }] })
    const onPlay = vi.fn()
    function Shown({ on }: { on: boolean }) {
      const actions = useEpisodeActions()
      return <>{on && actions.map((a) => <a.Host key={a.key} ctx={ctx} onPlay={onPlay} />)}</>
    }
    const r = render(<Shown on />)
    expect(onPlay).toHaveBeenCalledWith('alpha', expect.objectContaining({ node: expect.anything() }))
    onPlay.mockClear()
    r.rerender(<Shown on={false} />)
    expect(onPlay.mock.calls).toEqual([
      ['alpha', null],
      ['alpha-2', null],
    ])
  })

  it('a use() that throws drops its kinds only', async () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    vi.spyOn(console, 'error').mockImplementation(() => {})
    await signIn(adult)
    registerAddon('alpha', { episodeActions: [{ kinds: ['alpha'], use: play('a') }] })
    registerAddon('bad', {
      episodeActions: [
        {
          kinds: ['bad'],
          use: () => {
            throw new Error('boom')
          },
        },
      ],
    })
    render(<Guide />)
    expect(screen.getByRole('paragraph').textContent).toBe('alpha')
    vi.restoreAllMocks()
  })

  it('an entry\'s core kind (the library\'s) is dropped with a warning, and each entry has a key of its own', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    await signIn(adult)
    const use = play('a')
    registerAddon('alpha', {
      episodeActions: [
        { kinds: ['library', 'alpha'], use },
        { kinds: ['alpha'], use },
        { kinds: ['library'], use },
      ],
    })
    let got: ReturnType<typeof useEpisodeActions> = []
    function Read() {
      got = useEpisodeActions()
      return null
    }
    render(<Read />)
    expect(got.map((a) => a.kinds)).toEqual([['alpha'], ['alpha']])
    expect(new Set(got.map((a) => a.key)).size).toBe(2)
    expect(got[0]!.Host).not.toBe(got[1]!.Host)
    expect(warn).toHaveBeenCalledWith(expect.stringMatching(/addon alpha: episodeActions kind "library" dropped/))
    expect(warn.mock.calls.filter((c) => /kind "library" dropped/.test(String(c[0])))).toHaveLength(1)
    expect(warn.mock.calls.filter((c) => /kind "online" dropped/.test(String(c[0])))).toHaveLength(0)
    warn.mockRestore()
  })

  it('an addon\'s episodeActions for online is kept and sorts by its order map', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    await signIn(adult)
    registerAddon('alpha', { episodeActions: [{ kinds: ['alpha'], order: { alpha: 30 }, use: play('a') }] })
    registerAddon('onl', {
      episodeActions: [{ kinds: ['online'], labels: { online: 'k.online' }, order: { online: 20 }, use: play('o') }],
      strings: { ru: { 'k.online': 'Из сети' }, en: { 'k.online': 'From the net' } },
    })
    let actions: ReturnType<typeof useEpisodeActions> = []
    let got: ReturnType<typeof useSourceKinds> | null = null
    function Read() {
      actions = useEpisodeActions()
      got = useSourceKinds()
      return null
    }
    render(<Read />)
    // kept: it plays the rows of its kind, with no "the core plays it" warning
    expect(actions.map((a) => [a.addonId, a.kinds])).toEqual([
      ['alpha', ['alpha']],
      ['onl', ['online']],
    ])
    expect(warn).not.toHaveBeenCalled()
    // its place and its name are its addon's: no core seed for them
    expect(got!.order).toEqual({ library: 0, alpha: 30, online: 20 })
    expect(got!.label('online')).toBe('Из сети')
    // so it sorts ahead of alpha (30), though alpha's file is the better one
    const src = (kind: string, quality: string) => ({ kind, ref: kind, labels: [], firstSeen: '2026-01-01T00:00:00Z', meta: {}, quality })
    const online = src('online', '720p')
    expect(bestSource([src('alpha', '2160p'), online], got!.order)).toBe(online)
    warn.mockRestore()
  })

  it('kinds: the library\'s order and label, then each addon\'s; an addon cannot move the library', async () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    await signIn(adult)
    registerAddon('alpha', {
      episodeActions: [{ kinds: ['alpha', 'library'], labels: { alpha: 'k.alpha', library: 'k.library' }, order: { alpha: 10, library: 99 }, use: play('a') }],
      strings: { ru: { 'k.alpha': 'Альфа', 'k.library': 'Не диск' }, en: { 'k.alpha': 'Alpha', 'k.library': 'Not disk' } },
    })
    let got: ReturnType<typeof useSourceKinds> | null = null
    function Read() {
      got = useSourceKinds()
      return null
    }
    render(<Read />)
    expect(got!.order).toEqual({ library: 0, alpha: 10 })
    expect(got!.label('alpha')).toBe('Альфа')
    expect(got!.label('library')).toBe('Диск')
    // no addon names online: the core gives it neither a place nor a name
    expect(got!.label('online')).toBe('online')
    expect(got!.label('gamma')).toBe('gamma')
    vi.restoreAllMocks()
  })

  it('kinds: an order of 0 or below is dropped (the library ranks first), warned once', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    await signIn(adult)
    registerAddon('alpha', { episodeActions: [{ kinds: ['alpha', 'beta', 'gamma', 'library'], order: { alpha: -1, beta: 0, gamma: 5 }, use: play('a') }] })
    let got: ReturnType<typeof useSourceKinds> | null = null
    function Read() {
      got = useSourceKinds()
      useEpisodeActions()
      return null
    }
    const r = render(<Read />)
    r.rerender(<Read />)
    act(() => registerAddon('other', { searchBlocks: [{ element: () => null }] }))
    expect(got!.order).toEqual({ library: 0, gamma: 5 })
    const lines = warn.mock.calls.map((c) => String(c[0]))
    expect(lines.filter((l) => /order of "alpha" dropped/.test(l))).toHaveLength(1)
    expect(lines.filter((l) => /order of "beta" dropped/.test(l))).toHaveLength(1)
    expect(lines.filter((l) => /kind "library" dropped/.test(l))).toHaveLength(1)
    warn.mockRestore()
  })

  it('kinds: a malformed labels or order map names nothing', async () => {
    await signIn(adult)
    registerAddon('alpha', { episodeActions: [{ kinds: ['alpha'], labels: 'x' as never, order: 5 as never, use: play('a') }] })
    let got: ReturnType<typeof useSourceKinds> | null = null
    function Read() {
      got = useSourceKinds()
      return null
    }
    render(<Read />)
    expect(got!.order).toEqual({ library: 0 })
    expect(got!.label('alpha')).toBe('alpha')
  })

  it('a kid gets none', async () => {
    await signIn(kid)
    registerAddon('alpha', { episodeActions: [{ kinds: ['alpha'], use: play('a') }] })
    render(<Guide />)
    expect(screen.getByRole('paragraph').textContent).toBe('')
  })
})

describe('useProfileMenu', () => {
  beforeEach(() => session.reset())
  afterEach(() => {
    vi.unstubAllGlobals()
    session.reset()
    resetAddonsForTests()
  })

  function Menu() {
    return <p>{useProfileMenu().map((e) => `${e.label}>${e.to}`).join(',')}</p>
  }

  it('visible(user) hides an entry; a throwing visible() hides only its own', async () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    await signIn(adult)
    registerAddon('alpha', {
      profileMenu: [
        { label: 'mine', icon: 'gear', to: 'mine' },
        { label: 'admins', icon: 'gear', to: '/manage', visible: (u) => u.isAdmin },
        {
          label: 'broken',
          icon: 'gear',
          to: 'x',
          visible: () => {
            throw new Error('boom')
          },
        },
      ],
    })
    render(<Menu />)
    expect(screen.getByRole('paragraph').textContent).toBe('mine>/addons/alpha/mine')
    vi.restoreAllMocks()
  })

  it('a library-only profile gets the entries too; a kid none', async () => {
    await signIn(libraryOnly)
    registerAddon('alpha', { profileMenu: [{ label: 'mine', icon: 'gear', to: 'mine' }] })
    render(<Menu />)
    expect(screen.getByRole('paragraph').textContent).toBe('mine>/addons/alpha/mine')
    act(() => session.reset())
    await signIn(kid)
    expect(screen.getByRole('paragraph').textContent).toBe('')
  })
})

describe('matchAddonRoute: the route of an addon that a path names', () => {
  const route = (addonId: string, path: string) => ({ addonId, path, title: 'page', element: () => <p>{path}</p> })
  const routes = [
    route('dl', 'downloads'),
    route('dl', 'sources/add'),
    route('dl', 'sources/add/:id'),
    route('dl', 'sources/:sourceId/edit'),
    route('dl', 'play/:kind/:id'),
    route('dl', 'Mixed'),
    route('other', 'downloads'),
    route('other', 'only-other'),
  ]
  const hit = (addonId: string, rest: string) => {
    const m = matchAddonRoute(routes, addonId, rest)
    return m && { path: m.route.path, params: m.params }
  }

  it('a plain path matches as it is, with no params', () => {
    expect(hit('dl', 'downloads')).toEqual({ path: 'downloads', params: {} })
    expect(hit('dl', 'sources/add')).toEqual({ path: 'sources/add', params: {} })
  })

  it('a :name segment takes the value of its place', () => {
    expect(hit('dl', 'sources/add/alpha')).toEqual({ path: 'sources/add/:id', params: { id: 'alpha' } })
    expect(hit('dl', 'sources/7/edit')).toEqual({ path: 'sources/:sourceId/edit', params: { sourceId: '7' } })
    expect(hit('dl', 'play/movie/42')).toEqual({ path: 'play/:kind/:id', params: { kind: 'movie', id: '42' } })
  })

  it('an encoded value comes decoded', () => {
    expect(hit('dl', 'sources/add/two%20words')?.params).toEqual({ id: 'two words' })
  })

  it('an encoded slash stays inside its value; a plain path of non-ASCII letters matches its encoded form', () => {
    expect(hit('dl', 'sources/add/a%2Fb')).toEqual({ path: 'sources/add/:id', params: { id: 'a/b' } })
    const ru = [route('dl', 'страница'), route('dl', 'найти/:q')]
    expect(matchAddonRoute(ru, 'dl', '%D1%81%D1%82%D1%80%D0%B0%D0%BD%D0%B8%D1%86%D0%B0')?.route.path).toBe('страница')
    expect(matchAddonRoute(ru, 'dl', encodeURI('найти/кино'))?.params).toEqual({ q: 'кино' })
  })

  it('a malformed escape does not throw', () => {
    expect(hit('dl', 'sources/add/100%')).toEqual({ path: 'sources/add/:id', params: { id: '100%' } })
    expect(hit('dl', 'nothing%E0%A4%A')).toBeNull()
  })

  it('no match: a missing, an extra or a different segment', () => {
    expect(hit('dl', 'nothing')).toBeNull()
    expect(hit('dl', 'sources/7')).toBeNull()
    expect(hit('dl', 'sources/7/edit/more')).toBeNull()
    expect(hit('dl', 'sources/7/remove')).toBeNull()
    expect(hit('dl', 'play/movie')).toBeNull()
    expect(hit('dl', 'downloads/x')).toBeNull()
    expect(hit('dl', '')).toBeNull()
  })

  it('one leading or trailing slash on either side does not matter', () => {
    expect(hit('dl', '/downloads/')).toEqual({ path: 'downloads', params: {} })
    expect(hit('dl', 'sources/add/alpha/')).toEqual({ path: 'sources/add/:id', params: { id: 'alpha' } })
    expect(matchAddonRoute([route('dl', '/downloads/')], 'dl', 'downloads')?.params).toEqual({})
    expect(matchAddonRoute([route('dl', '/sources/:id/')], 'dl', 'sources/3')?.params).toEqual({ id: '3' })
  })

  it('any number of slashes at either end does not matter', () => {
    expect(hit('dl', '///downloads///')).toEqual({ path: 'downloads', params: {} })
    expect(hit('dl', '//sources/add/alpha//')).toEqual({ path: 'sources/add/:id', params: { id: 'alpha' } })
    expect(matchAddonRoute([route('dl', '//downloads//')], 'dl', 'downloads')?.params).toEqual({})
    expect(matchAddonRoute([route('dl', '///sources/:id///')], 'dl', '/sources/3/')?.params).toEqual({ id: '3' })
  })

  it('a route path is react-router\'s matchPath syntax: * takes the rest, ? makes a segment optional', () => {
    const more = [route('dl', 'files/*'), route('dl', 'page/:id?')]
    expect(matchAddonRoute(more, 'dl', 'files/a/b')?.params).toEqual({ '*': 'a/b' })
    expect(matchAddonRoute(more, 'dl', 'files')?.params).toEqual({ '*': '' })
    expect(matchAddonRoute(more, 'dl', 'page')?.params).toEqual({})
    expect(matchAddonRoute(more, 'dl', 'page/7')?.params).toEqual({ id: '7' })
    expect(matchAddonRoute(more, 'dl', 'page/7/8')).toBeNull()
  })

  it('only the routes of the addon asked about', () => {
    expect(hit('other', 'only-other')).toEqual({ path: 'only-other', params: {} })
    expect(hit('dl', 'only-other')).toBeNull()
    expect(hit('other', 'sources/add')).toBeNull()
    expect(hit('nobody', 'downloads')).toBeNull()
    // the same path in two addons: each gets its own
    expect(matchAddonRoute(routes, 'other', 'downloads')?.route.addonId).toBe('other')
  })

  it('case counts', () => {
    expect(hit('dl', 'Mixed')).toEqual({ path: 'Mixed', params: {} })
    expect(hit('dl', 'mixed')).toBeNull()
    expect(hit('dl', 'Downloads')).toBeNull()
    expect(hit('dl', 'SOURCES/add')).toBeNull()
  })

  it('the addon root: an empty path', () => {
    expect(matchAddonRoute([route('dl', '')], 'dl', '')?.params).toEqual({})
    expect(matchAddonRoute([route('dl', '')], 'dl', '/')?.params).toEqual({})
    expect(matchAddonRoute([route('dl', '')], 'dl', 'x')).toBeNull()
  })

  it('the first of the routes that match wins', () => {
    const two = [route('dl', 'a/:x'), route('dl', 'a/b')]
    expect(matchAddonRoute(two, 'dl', 'a/b')?.route.path).toBe('a/:x')
  })
})

describe('addonRest: the path after /addons/<id>/', () => {
  it('cuts the addon prefix', () => {
    expect(addonRest('/addons/dl/sources/add/x', 'dl')).toBe('sources/add/x')
    expect(addonRest('/addons/dl/page', 'dl')).toBe('page')
    expect(addonRest('/addons/dl/page/', 'dl')).toBe('page/')
  })
  it('the addon root is empty', () => {
    expect(addonRest('/addons/dl', 'dl')).toBe('')
    expect(addonRest('/addons/dl/', 'dl')).toBe('')
  })
  it('the /addons prefix is as case-insensitive as the app\'s own route to it', () => {
    expect(addonRest('/Addons/dl/sources/add/x', 'dl')).toBe('sources/add/x')
    expect(addonRest('/ADDONS/dl', 'dl')).toBe('')
    expect(addonRest('/Addons/dl/page', 'dl')).toBe('page')
    // the addon's id is not: another addon's path is still empty
    expect(addonRest('/Addons/dlx/page', 'dl')).toBe('')
    expect(addonRest('/Addons/other/page', 'dl')).toBe('')
  })
  it('another addon, a longer id or a core path is empty', () => {
    expect(addonRest('/addons/dlx/page', 'dl')).toBe('')
    expect(addonRest('/addons/other/page', 'dl')).toBe('')
    expect(addonRest('/manage/addons', 'dl')).toBe('')
  })
})
