// Shared test harness for «Файлы»: a fetch mock that records requests and a
// router with FilesPage mounted at /files/*.
import { vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { RouterProvider, createMemoryRouter } from 'react-router'
import type { FileEntry, FileListing, User } from '../../../api/types'
import { session } from '../../../state/session'
import { FilesPage } from '../FilesPage'

export const admin: User = { id: 1, name: 'Александр', avatar: 'grad-1', hasPassword: true, isAdmin: true, isKid: false, maxRating: null }
export const member: User = { ...admin, id: 2, name: 'Маша', isAdmin: false }
export const kid: User = { ...admin, id: 3, name: 'Петя', isAdmin: false, isKid: true }

export const entry = (name: string, over: Partial<FileEntry> = {}): FileEntry => ({
  name,
  path: name,
  isDir: false,
  size: 0,
  mtime: '2026-10-20T10:00:00Z',
  kind: 'other',
  ...over,
})

export function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

export const ROOTS = [
  { id: 'movies', title: 'Фильмы', icon: 'film' },
  { id: 'series', title: 'Сериалы', icon: 'tv' },
  { id: 'stream-cache', title: 'Кеш просмотра', icon: 'clock' },
]

export interface Call {
  method: string
  path: string
  query: URLSearchParams
  body: Record<string, unknown> | undefined
  signal?: AbortSignal
}

/** Route handler: return a Response to override, or undefined for the default. */
export type Override = (c: Call) => Response | Promise<Response> | undefined

export function mockApi(user: User, listings: Record<string, FileListing>, override?: Override) {
  const calls: Call[] = []
  const fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), 'http://vault.test')
    const body = typeof init?.body === 'string' ? (JSON.parse(init.body) as Record<string, unknown>) : undefined
    const c: Call = { method: init?.method ?? 'GET', path: url.pathname, query: url.searchParams, body, signal: init?.signal ?? undefined }
    calls.push(c)
    const o = await override?.(c)
    if (o) return o
    switch (url.pathname) {
      case '/api/login':
        return json(user)
      case '/api/files/roots':
        return json(ROOTS)
      case '/api/files/list': {
        const key = `${url.searchParams.get('root')}:${url.searchParams.get('path') ?? ''}`
        return listings[key] ? json(listings[key]) : json({ error: 'Не найдено' }, 404)
      }
      case '/api/files/usage':
        return json({ total: 4e12, used: 3e12, free: 1e12, categories: [{ id: 'movies', bytes: 2e12 }] })
      case '/api/files/stat': {
        const l = listings[`${url.searchParams.get('root')}:`]
        const e = l?.entries.find((x) => x.path === url.searchParams.get('path'))
        return e ? json(e) : json({ error: 'Не найдено' }, 404)
      }
      case '/api/home':
        return json({ hero: [], shelves: [] })
      default:
        return json({ error: 'Не найдено' }, 404)
    }
  })
  return { fetch, calls }
}

export const listing = (root: string, path: string, entries: FileEntry[]): FileListing => ({
  root,
  path,
  breadcrumbs: [{ name: 'Фильмы', path: '' }],
  entries,
})

export async function renderFiles(user: User, mock: ReturnType<typeof mockApi>, path = '/files/movies', waitFor?: RegExp) {
  vi.stubGlobal('fetch', mock.fetch)
  await session.login(user.id, 'pw')
  const router = createMemoryRouter([{ path: '/files/*', element: <FilesPage /> }, { path: '*', element: <p>другая страница</p> }], {
    initialEntries: [path],
  })
  render(<RouterProvider router={router} />)
  if (waitFor) await screen.findByRole('option', { name: waitFor })
  return router
}
