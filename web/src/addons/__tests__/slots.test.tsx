// The core's side of the 2d slots: whether the profile can add titles from
// outside the library (a search block is there for it) and where an
// addon's relative link leads.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, render, screen } from '@testing-library/react'
import { useCallback, useMemo, useState } from 'react'
import type { EpisodeCtx, EpisodePlay } from '../../components/episodes/episodeTypes'
import { session } from '../../state/session'
import { registerAddon, resetAddonsForTests, setAddonUser } from '../registry'
import { resolveAddonPath, useCanAdd, useEpisodeActions, useProfileMenu, useSourceKinds } from '../slots'

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

  it('an entry\'s core kinds are dropped with a warning, and each entry has a key of its own', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    await signIn(adult)
    const use = play('a')
    registerAddon('alpha', {
      episodeActions: [
        { kinds: ['library', 'alpha', 'online'], use },
        { kinds: ['alpha'], use },
        { kinds: ['online'], use },
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
    expect(warn).toHaveBeenCalledWith(expect.stringMatching(/addon alpha: episodeActions kind "online" dropped/))
    warn.mockRestore()
  })

  it('kinds: the core\'s order and labels, then each addon\'s; an addon cannot move a core kind', async () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    await signIn(adult)
    registerAddon('alpha', {
      episodeActions: [{ kinds: ['alpha', 'online'], labels: { alpha: 'k.alpha', online: 'k.online' }, order: { alpha: 10, online: 99 }, use: play('a') }],
      strings: { ru: { 'k.alpha': 'Альфа', 'k.online': 'Не онлайн' }, en: { 'k.alpha': 'Alpha', 'k.online': 'Not online' } },
    })
    let got: ReturnType<typeof useSourceKinds> | null = null
    function Read() {
      got = useSourceKinds()
      return null
    }
    render(<Read />)
    expect(got!.order).toEqual({ library: 0, online: 20, alpha: 10 })
    expect(got!.label('alpha')).toBe('Альфа')
    expect(got!.label('online')).toBe('Онлайн')
    expect(got!.label('library')).toBe('Диск')
    expect(got!.label('gamma')).toBe('gamma')
    vi.restoreAllMocks()
  })

  it('kinds: an order of 0 or below is dropped (the library ranks first), warned once', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    await signIn(adult)
    registerAddon('alpha', { episodeActions: [{ kinds: ['alpha', 'beta', 'gamma', 'online'], order: { alpha: -1, beta: 0, gamma: 5 }, use: play('a') }] })
    let got: ReturnType<typeof useSourceKinds> | null = null
    function Read() {
      got = useSourceKinds()
      useEpisodeActions()
      return null
    }
    const r = render(<Read />)
    r.rerender(<Read />)
    act(() => registerAddon('other', { searchBlocks: [{ element: () => null }] }))
    expect(got!.order).toEqual({ library: 0, online: 20, gamma: 5 })
    const lines = warn.mock.calls.map((c) => String(c[0]))
    expect(lines.filter((l) => /order of "alpha" dropped/.test(l))).toHaveLength(1)
    expect(lines.filter((l) => /order of "beta" dropped/.test(l))).toHaveLength(1)
    expect(lines.filter((l) => /kind "online" dropped/.test(l))).toHaveLength(1)
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
    expect(got!.order).toEqual({ library: 0, online: 20 })
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
