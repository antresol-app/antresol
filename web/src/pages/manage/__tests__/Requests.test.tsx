// «Управление → Запросы»: «Отказать» takes a reason; «Одобрить» opens the
// sheet of the first addon request action that wants the request, and
// approves at once when there is none; a sheet that throws keeps the
// request waiting and says so (it never approves with nothing coming).
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import type { KidRequest, User } from '../../../api/types'
import { registerAddon, resetAddonsForTests, setAddonUser } from '../../../addons/registry'
import { resetKidRequests } from '../../../state/kidRequests'
import { session } from '../../../state/session'
import { Requests } from '../Requests'

const admin: User = { id: 1, name: 'Папа', avatar: 'grad-1', hasPassword: false, isAdmin: true, isKid: false, maxRating: null, requests: true }
const asker = { id: 3, name: 'Вася', avatar: 'grad-3' }

const json = (b: unknown, status = 200) => new Response(JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } })

const req = (o: Partial<KidRequest>): KidRequest => ({
  id: 1, key: 'tmdb:1', kind: 'movie', tmdbId: 1, extId: '', title: 'Дюна', subtitle: '', year: 2021, poster: '', aliases: [],
  rating: '12+', status: 'pending', reason: '', libraryItemId: null, createdAt: new Date().toISOString(), user: asker, ...o,
})

/** switchOn: the server's «Запросы детей» (null: an older server that has none). */
function stub(list: KidRequest[], switchOn: boolean | null = true) {
  const approved = new Set<number>()
  let kidRequests: boolean | null = switchOn
  const f = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input)
    if (url.endsWith('/api/login')) return json(admin)
    if (url.endsWith('/api/settings')) {
      if (init?.method === 'PUT') kidRequests = (JSON.parse(String(init.body)) as { kidRequests: boolean }).kidRequests
      return json(kidRequests === null ? {} : { kidRequests })
    }
    if (url.endsWith('/api/requests') && init?.method !== 'POST') return json(list.map((r) => (approved.has(r.id) ? { ...r, status: 'approved' } : r)))
    const a = /\/api\/requests\/(\d+)\/approve$/.exec(url)
    if (a) {
      approved.add(Number(a[1]))
      return json(req({ id: Number(a[1]), status: 'approved' }))
    }
    if (url.endsWith('/api/requests/1/decline')) return json(req({ id: 1, status: 'declined' }))
    if (url.endsWith('/api/requests/count')) return json({ pending: 0 })
    return json({})
  })
  vi.stubGlobal('fetch', f)
  return f
}
const posted = (f: ReturnType<typeof stub>, path: string) =>
  f.mock.calls.filter(([u, i]) => String(u).endsWith(path) && i?.method === 'POST').map(([, i]) => (i?.body ? (JSON.parse(String(i.body)) as unknown) : null))

async function show() {
  await session.login(1)
  act(() => setAddonUser({ isAdmin: true }))
  render(
    <MemoryRouter>
      <Requests />
    </MemoryRouter>,
  )
}

describe('«Запросы» (admin)', () => {
  beforeEach(() => {
    session.reset()
    resetKidRequests()
  })
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
    resetAddonsForTests()
  })

  it('declines with a reason', async () => {
    const f = stub([req({ id: 1 })])
    await show()
    await userEvent.click(await screen.findByRole('button', { name: 'Отказать: «Дюна»' }))
    const sheet = await screen.findByRole('dialog', { name: 'Отказать: «Дюна»?' })
    await userEvent.type(within(sheet).getByLabelText('Причина (необязательно)'), 'Сначала уроки')
    await userEvent.click(within(sheet).getByRole('button', { name: 'Отказать' }))
    await waitFor(() => expect(posted(f, '/api/requests/1/decline')).toEqual([{ reason: 'Сначала уроки' }]))
  })

  it('«Дети могут просить» is the server switch: shown as saved, turned off and on again', async () => {
    const f = stub([req({ id: 1 })])
    await show()
    const toggle = await screen.findByRole('switch', { name: 'Дети могут просить' })
    expect(toggle).toHaveAttribute('aria-checked', 'true')
    await userEvent.click(toggle)
    await waitFor(() => expect(screen.getByRole('switch', { name: 'Дети могут просить' })).toHaveAttribute('aria-checked', 'false'))
    await userEvent.click(screen.getByRole('switch', { name: 'Дети могут просить' }))
    await waitFor(() => expect(screen.getByRole('switch', { name: 'Дети могут просить' })).toHaveAttribute('aria-checked', 'true'))
    const puts = f.mock.calls.filter(([u, i]) => String(u).endsWith('/api/settings') && i?.method === 'PUT').map(([, i]) => JSON.parse(String(i!.body)) as unknown)
    expect(puts).toEqual([{ kidRequests: false }, { kidRequests: true }])
    // the list stays: answers to what was asked before are still the admin's
    expect(screen.getByRole('button', { name: 'Одобрить «Дюна»' })).toBeInTheDocument()
  })

  it('an older server without the setting shows no switch', async () => {
    stub([req({ id: 1 })], null)
    await show()
    expect(await screen.findByRole('button', { name: 'Одобрить «Дюна»' })).toBeInTheDocument()
    expect(screen.queryByRole('switch')).not.toBeInTheDocument()
  })

  it("approve opens the addon's sheet; without one approves at once", async () => {
    const f = stub([req({ id: 1 }), req({ id: 2, key: 'ext:mb-1', kind: 'music', extId: 'mb-1', title: 'Группа крови' })])
    registerAddon('dl', {
      requestActions: [
        {
          visible: (r) => r.kind === 'music',
          element: ({ request, onClose, onApproved }) => (
            <div role="dialog" aria-label={`Лист: ${request.title}`}>
              <button type="button" onClick={onApproved}>
                готово
              </button>
              <button type="button" onClick={onClose}>
                закрыть
              </button>
            </div>
          ),
        },
      ],
    })
    await show()
    // the album: the addon's sheet, nothing approved by the core
    await userEvent.click(await screen.findByRole('button', { name: 'Одобрить «Группа крови»' }))
    const sheet = await screen.findByRole('dialog', { name: 'Лист: Группа крови' })
    expect(posted(f, '/api/requests/2/approve')).toEqual([])
    await userEvent.click(within(sheet).getByRole('button', { name: 'готово' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    // the film: no action wants it, approved at once
    await userEvent.click(screen.getByRole('button', { name: 'Одобрить «Дюна»' }))
    await waitFor(() => expect(posted(f, '/api/requests/1/approve')).toHaveLength(1))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('a throwing request action keeps the request waiting and shows an error', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {})
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    const f = stub([req({ id: 5 })])
    registerAddon('dl', {
      requestActions: [
        {
          element: () => {
            throw new Error('boom')
          },
        },
      ],
    })
    await show()
    await userEvent.click(await screen.findByRole('button', { name: 'Одобрить «Дюна»' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Расширение не смогло открыть выбор — попробуйте ещё раз')
    expect(posted(f, '/api/requests/5/approve')).toHaveLength(0)
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    const waiting = screen.getByRole('list', { name: 'Ждут ответа' })
    expect(within(waiting).getByRole('button', { name: 'Одобрить «Дюна»' })).toBeInTheDocument()
  })
})
