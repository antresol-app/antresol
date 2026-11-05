// «Общая медиатека / Моя медиатека»: the picker on the library pages and in
// the profile menu, kept on the server for the profile; the pages ask with
// ?scope=mine; a kid's profile never gets the picker.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import type { User } from '../../api/types'
import { ProfileMenu } from '../../components/ProfileMenu'
import { libraryScopeOf } from '../../state/libraryScope'
import { session } from '../../state/session'
import { Browse } from '../Browse'
import { card } from '../../components/__tests__/fixtures'

const json = (b: unknown, status = 200) => new Response(JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } })
const adult: User = { id: 2, name: 'Мама', avatar: 'grad-3', hasPassword: false, isAdmin: false, isKid: false, maxRating: null, libraryScope: 'all' }
const kid: User = { ...adult, id: 3, name: 'Вася', isKid: true }

type Call = { url: string; method: string; body?: string }
let calls: Call[] = []
/** The server: login answers `u`, PUT /api/me/library-scope saves (or fails), /api/items lists one movie per scope. */
function server(u: User, opts: { failSave?: boolean } = {}) {
  calls = []
  let saved = u.libraryScope ?? 'all'
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      calls.push({ url, method: init?.method ?? 'GET', body: typeof init?.body === 'string' ? init.body : undefined })
      if (url.endsWith('/api/login')) return json({ ...u, libraryScope: saved })
      if (url.endsWith('/api/me/library-scope')) {
        if (opts.failSave) return json({ error: 'Что-то пошло не так' }, 500)
        saved = JSON.parse(String(init?.body)).scope
        return json({ ...u, libraryScope: saved })
      }
      if (url.includes('/api/items')) {
        const mine = url.includes('scope=mine')
        const items = mine ? [card(1, { title: 'Моё' })] : [card(1, { title: 'Моё' }), card(2, { title: 'Чужое' })]
        return json({ items, total: items.length })
      }
      if (url.includes('/api/library/pending')) return json([])
      return json({ error: 'нет' }, 404)
    }),
  )
}

beforeEach(() => session.reset())
afterEach(() => {
  vi.unstubAllGlobals()
  session.reset()
})

describe('library picker', () => {
  it('a kid gets no picker and always the shared library', async () => {
    server({ ...kid, libraryScope: 'mine' })
    await session.login(kid.id)
    expect(libraryScopeOf(session.get().user)).toBe('all')
    render(
      <MemoryRouter initialEntries={['/movies']}>
        <ProfileMenu user={session.get().user!} />
        <Browse kind="movie" title="Фильмы" emptyTitle="Нет фильмов" />
      </MemoryRouter>,
    )
    expect(await screen.findByText('Чужое')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^Медиатека:/ })).not.toBeInTheDocument()
    expect(calls.filter((c) => c.url.includes('/api/items')).every((c) => !c.url.includes('scope='))).toBe(true)
    await userEvent.click(screen.getByRole('button', { name: 'Профиль: Вася' }))
    expect(screen.queryByRole('group', { name: 'Медиатека' })).not.toBeInTheDocument()
  })
})
