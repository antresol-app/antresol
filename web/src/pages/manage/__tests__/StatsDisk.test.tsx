// «Статистика → Медиадиск»: a category of the bar or its legend opens what
// it is made of, largest first; «Другое» is walked in the background and
// polled until it is ready.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { act, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import type { Stats as Data } from '../../../api/types'
import { session } from '../../../state/session'
import { Stats } from '../Stats'

const GB = 1024 ** 3
const json = (b: unknown, status = 200) => new Response(JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } })

const stats: Data = {
  disk: {
    total: 1800 * GB,
    used: 1000 * GB,
    free: 800 * GB,
    categories: [
      { id: 'movies', bytes: 252 * GB },
      { id: 'downloads', bytes: 100 * GB },
      { id: 'other', bytes: 120 * GB },
    ],
  },
  counts: { movies: 1, series: 0, episodes: 0, albums: 0, tracks: 0, books: 0, audiobooks: 0, comics: 0 },
  watchedByUser: [],
  topItems30d: [],
}

function stub(breakdown: (cat: string, call: number) => unknown) {
  const calls: Record<string, number> = {}
  const f = vi.fn(async (input: RequestInfo | URL) => {
    const u = String(input)
    if (u.endsWith('/api/stats')) return json(stats)
    const m = /\/api\/stats\/disk\/([\w-]+)$/.exec(u)
    if (m) {
      calls[m[1]!] = (calls[m[1]!] ?? 0) + 1
      return json(breakdown(m[1]!, calls[m[1]!]!))
    }
    return json({ error: 'нет' }, 404)
  })
  vi.stubGlobal('fetch', f)
  return f
}

const show = () =>
  render(
    <MemoryRouter>
      <Stats />
    </MemoryRouter>,
  )

afterEach(() => {
  vi.unstubAllGlobals()
  session.reset()
})

describe('disk breakdown', () => {
  it('a legend item opens its category: the folders, largest first, with their sizes', async () => {
    stub(() => ({
      category: 'movies',
      pending: false,
      bytes: 252 * GB,
      entries: [
        { name: 'Interstellar (2014)', bytes: 62 * GB, dir: true },
        { name: 'Garlic (2021)', bytes: 4 * GB, dir: true },
      ],
      more: { count: 3, bytes: 5 * GB },
    }))
    show()
    await userEvent.click(await screen.findByRole('button', { name: /^Фильмы/ }))
    const sheet = await screen.findByRole('dialog', { name: /Фильмы/ })
    const rows = within(sheet).getAllByRole('listitem')
    expect(rows.map((r) => r.querySelector('[data-name]')?.textContent)).toEqual(['Interstellar (2014)', 'Garlic (2021)', 'Ещё 3 папки'])
    expect(within(rows[0]!).getByText(/^62(,0)? ГБ$/)).toBeInTheDocument()
  })

  it('«Другое» waits for the walk, then names the trash and what no folder explains', async () => {
    const f = stub((_, n) =>
      n < 2
        ? { category: 'other', pending: true, bytes: 120 * GB, entries: [] }
        : { category: 'other', pending: false, bytes: 120 * GB, entries: [{ name: '.vault-trash', bytes: 64 * GB, dir: true }, { name: 'backup', bytes: 41 * GB, dir: true }], rest: 15 * GB },
    )
    show()
    await userEvent.click(await screen.findByRole('button', { name: /^Другое/ }))
    const sheet = await screen.findByRole('dialog', { name: /Другое/ })
    expect(within(sheet).getByRole('status')).toHaveTextContent('Считаем, что занимает место…')
    expect(await within(sheet).findByText('Корзина', {}, { timeout: 4000 })).toBeInTheDocument()
    expect(within(sheet).getByText('backup')).toBeInTheDocument()
    expect(within(sheet).getByText('Система и файловая система')).toBeInTheDocument()
    expect(f.mock.calls.filter(([u]) => String(u).endsWith('/api/stats/disk/other'))).toHaveLength(2)
  })

  it('a walk that never ends: asks less and less often, then stops and says to look later', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const f = stub(() => ({ category: 'other', pending: true, bytes: 120 * GB, entries: [] }))
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })
    show()
    await user.click(await screen.findByRole('button', { name: /^Другое/ }))
    const asked = () => f.mock.calls.filter(([u]) => String(u).endsWith('/api/stats/disk/other')).length
    await act(() => vi.advanceTimersByTimeAsync(10 * 60_000))
    const n = asked()
    expect(n).toBeGreaterThan(3)
    expect(n).toBeLessThan(25)
    expect(screen.getByRole('dialog', { name: /Другое/ })).toHaveTextContent('Подсчёт ещё идёт')
    await act(() => vi.advanceTimersByTimeAsync(10 * 60_000))
    expect(asked()).toBe(n)
    vi.useRealTimers()
  })

  it('a walk that ran out of time shows what it counted, said to be partial', async () => {
    stub(() => ({ category: 'other', pending: false, partial: true, bytes: 120 * GB, entries: [{ name: 'backup', bytes: 41 * GB, dir: true }] }))
    show()
    await userEvent.click(await screen.findByRole('button', { name: /^Другое/ }))
    const sheet = await screen.findByRole('dialog', { name: /Другое/ })
    expect(await within(sheet).findByText('backup')).toBeInTheDocument()
    expect(within(sheet).getByText(/Подсчитано не всё/)).toBeInTheDocument()
  })

  it('library-only: no breakdown, the meter stays', async () => {
    const me = { id: 1, name: 'Папа', avatar: 'grad-1', hasPassword: true, isAdmin: true, isKid: false, maxRating: null, libraryOnly: true }
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const u = String(input)
        return u.endsWith('/api/login') ? json(me) : u.endsWith('/api/stats') ? json(stats) : json({ error: 'нет' }, 404)
      }),
    )
    await session.login(me.id)
    show()
    expect(await screen.findByText('Медиадиск')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^Фильмы/ })).toBeNull()
  })
})
