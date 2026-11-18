// The kid's request search block (core): what is not in the library yet, from
// GET /api/requests/search, each with «Попросить» or the request's state.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import type { KidRequest, User } from '../../api/types'
import { resetKidRequests } from '../../state/kidRequests'
import { session } from '../../state/session'
import { RequestSearch } from '../RequestSearch'
import type { SearchScope } from '../../app/sections'

const kid: User = { id: 3, name: 'Вася', avatar: 'grad-3', hasPassword: false, isAdmin: false, isKid: true, maxRating: null, canRequest: true }

const json = (b: unknown, status = 200) =>
  new Response(b === undefined ? null : JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } })

const req = (o: Partial<KidRequest>): KidRequest => ({
  id: 1, key: 'tmdb:1', kind: 'movie', tmdbId: 1, extId: '', title: 'Дюна', subtitle: '', year: 2021, poster: '', aliases: [],
  rating: '12+', status: 'pending', reason: '', libraryItemId: null, createdAt: new Date().toISOString(), ...o,
})

const cand = (o: object) => ({ kind: 'movie', tmdbId: 1, extId: '', title: 'Дюна', subtitle: '', year: 2021, poster: '', explicit: false, aliases: [], ...o })

type Handler = (url: string, init?: RequestInit) => Response | undefined

function stub(handle: Handler) {
  const f = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input)
    if (url.endsWith('/api/login')) return json(kid)
    const r = handle(url, init)
    if (r) return r
    if (url.endsWith('/api/requests/mine')) return json([])
    return json({})
  })
  vi.stubGlobal('fetch', f)
  return f
}
const searched = (f: ReturnType<typeof stub>) => f.mock.calls.map(([u]) => String(u)).filter((u) => u.includes('/api/requests/search'))

const show = (text: string, scope: SearchScope = 'all') =>
  render(
    <MemoryRouter>
      <RequestSearch q={text} text={text} scope={scope} />
    </MemoryRouter>,
  )

describe('RequestSearch', () => {
  beforeEach(async () => {
    session.reset()
    resetKidRequests()
  })
  afterEach(() => vi.unstubAllGlobals())

  // the title page is the core's in every build (kids included)
  it('links a movie to its title page', async () => {
    stub((url) => {
      if (url.includes('/api/requests/search')) return json([cand({ tmdbId: 7, kidRequest: { key: 'tmdb:7', allowed: true, request: null } })])
    })
    await session.login(3)
    show('Дюна')
    await screen.findByRole('button', { name: 'Попросить «Дюна»' })
    const links = screen.getAllByRole('link', { name: 'Дюна' })
    expect(links.length).toBeGreaterThan(0)
    for (const a of links) expect(a).toHaveAttribute('href', '/title/movie/7')
  })

  it('asks for a movie', async () => {
    const f = stub((url, init) => {
      if (url.includes('/api/requests/search')) return json([cand({ kidRequest: { key: 'tmdb:1', allowed: true, request: null } })])
      if (url.endsWith('/api/requests') && init?.method === 'POST') return json(req({}))
    })
    await session.login(3)
    show('Дюна')
    expect(screen.getByRole('heading', { name: 'Найти и попросить' })).toBeInTheDocument()
    await userEvent.click(await screen.findByRole('button', { name: 'Попросить «Дюна»' }))
    expect(await screen.findByText('Ждёт ответа')).toBeInTheDocument()
    const url = new URL(searched(f)[0]!, 'http://x')
    expect([url.searchParams.get('q'), url.searchParams.get('kind')]).toEqual(['Дюна', 'video'])
    const posts = f.mock.calls.filter(([u, i]) => String(u).endsWith('/api/requests') && i?.method === 'POST').map(([, i]) => JSON.parse(String(i!.body)) as unknown)
    expect(posts).toEqual([{ kind: 'movie', tmdbId: 1, title: 'Дюна' }])
  })

  it('an album row has placeholder art', async () => {
    const f = stub((url) => {
      if (url.includes('/api/requests/search'))
        return json([cand({ kind: 'music', tmdbId: 0, extId: 'mb-1', title: 'Группа крови', subtitle: 'Кино', year: 1988, poster: 'https://example.com/cover.jpg', kidRequest: { key: 'ext:mb-1', allowed: true, request: null } })])
    })
    await session.login(3)
    show('Группа крови', 'music')
    const ask = await screen.findByRole('button', { name: 'Попросить «Группа крови»' })
    const row = ask.closest('li')!
    expect(within(row).getByText('Кино')).toBeInTheDocument()
    expect(row.querySelector('img')).toBeNull()
    expect(new URL(searched(f)[0]!, 'http://x').searchParams.get('kind')).toBe('music')
  })

  it('a request already asked shows its state', async () => {
    stub((url) => {
      if (url.includes('/api/requests/search'))
        return json([cand({ kidRequest: { key: 'tmdb:1', allowed: true, request: req({ status: 'approved' }) } })])
    })
    await session.login(3)
    show('Дюна', 'movies')
    expect(await screen.findByText('Одобрено — скоро появится')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Попросить/ })).toBeNull()
  })

  it('errors show the server text', async () => {
    stub((url) => {
      if (url.includes('/api/requests/search')) return json({ error: 'Запросы здесь не принимаются' }, 403)
    })
    await session.login(3)
    show('Дюна')
    expect(await screen.findByText('Запросы здесь не принимаются')).toBeInTheDocument()
  })
})
