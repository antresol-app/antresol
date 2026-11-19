// «Управление каналами»: «Удалить» works for every kind of channel. A hand-made
// channel goes; an automatic one goes too and lands in «Удалённые каналы»
// (the generator leaves it alone) with «Вернуть»; a channel with fetching says
// what happens to what was fetched for it. Against a fake server.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { session } from '../../../state/session'
import { TvManage } from '../TvManage'

const ADMIN = { id: 1, name: 'Админ', avatar: 'grad-1', hasPassword: true, isAdmin: true, isKid: false, maxRating: null }
const channel = (id: number, name: string, over: Record<string, unknown> = {}) => ({
  id, number: id, name, category: 'Кино', personal: false, auto: false, enabled: true, canEdit: true, logo: 0, rules: { order: 'shuffle' }, ...over,
})
const HAND = channel(1, 'Мой канал')
const AUTO = channel(2, 'Комедии', { auto: true, category: 'Кино по жанрам' })
const FETCH = channel(3, 'Марафон', { rules: { order: 'ordered', fetch: { titles: [{ kind: 'series', tmdbId: 100, title: 'Друзья' }] } } })

type Call = { method: string; url: string }
let calls: Call[] = []
let channels: ReturnType<typeof channel>[] = []
let removed: { id: number; name: string; category: string; personal: boolean }[] = []
let restored = true
const json = (body: unknown, status = 200) => Promise.resolve(new Response(body === null ? null : JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

beforeEach(async () => {
  calls = []
  channels = [HAND, AUTO, FETCH]
  removed = []
  restored = true
  vi.spyOn(globalThis, 'fetch').mockImplementation((input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url
    const method = (init?.method ?? 'GET').toUpperCase()
    calls.push({ method, url })
    if (url.endsWith('/api/login')) return json(ADMIN)
    if (method === 'GET' && url.startsWith('/api/channels?manage=1')) return json({ channels, lastChannel: null, canManage: true, canCreate: true, removed })
    const del = /^\/api\/channels\/(\d+)$/.exec(url)
    if (method === 'DELETE' && del) {
      const c = channels.find((x) => x.id === Number(del[1]))!
      channels = channels.filter((x) => x !== c)
      if (c.auto) removed = [{ id: 9, name: c.name, category: c.category, personal: false }, ...removed]
      return json(null, 204)
    }
    const back = /^\/api\/channels\/removed\/(\d+)\/restore$/.exec(url)
    if (method === 'POST' && back) {
      removed = removed.filter((x) => x.id !== Number(back[1]))
      if (restored) channels = [...channels, AUTO]
      return json({ restored })
    }
    return json({ error: 'unexpected ' + method + ' ' + url }, 500)
  })
  await session.login(ADMIN.id)
})
afterEach(() => {
  session.reset()
  vi.restoreAllMocks()
})

const open = () =>
  render(
    <MemoryRouter>
      <TvManage />
    </MemoryRouter>,
  )

async function askDelete(name: string) {
  fireEvent.click(await screen.findByRole('button', { name }))
  fireEvent.click(within(await screen.findByRole('menu')).getByRole('menuitem', { name: 'Удалить' }))
  return screen.findByRole('alertdialog')
}

describe('«Управление каналами»: удаление', () => {
  it('deletes a hand-made channel and offers nothing to bring back', async () => {
    open()
    const dialog = await askDelete('Мой канал')
    expect(within(dialog).getByText('Удалить канал «Мой канал»?')).toBeInTheDocument()
    expect(within(dialog).getByText(/Канал, его расписание и номер удалятся/)).toBeInTheDocument()
    fireEvent.click(within(dialog).getByRole('button', { name: 'Удалить' }))
    await waitFor(() => expect(screen.queryByText('Мой канал')).toBeNull())
    expect(calls).toContainEqual({ method: 'DELETE', url: '/api/channels/1' })
    expect(await screen.findByText('Канал «Мой канал» удалён')).toBeInTheDocument()
    expect(screen.queryByText('Удалённые каналы')).toBeNull()
  })

  it('deletes an automatic channel (no more «Выключить») and brings it back from «Удалённые каналы»', async () => {
    open()
    const dialog = await askDelete('Комедии')
    expect(within(dialog).getByText(/не вернётся при обновлении автоматических каналов/)).toBeInTheDocument()
    expect(within(dialog).queryByRole('button', { name: 'Выключить' })).toBeNull()
    fireEvent.click(within(dialog).getByRole('button', { name: 'Удалить' }))
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Комедии' })).toBeNull())
    // the list now shows it under «Удалённые каналы»
    const section = (await screen.findByText('Удалённые каналы')).closest('section')!
    expect(within(section).getByText('Комедии')).toBeInTheDocument()
    expect(within(section).getByText('Кино по жанрам')).toBeInTheDocument()
    fireEvent.click(within(section).getByRole('button', { name: 'Вернуть канал «Комедии»' }))
    await waitFor(() => expect(calls).toContainEqual({ method: 'POST', url: '/api/channels/removed/9/restore' }))
    expect(await screen.findByText('Канал «Комедии» вернулся')).toBeInTheDocument()
    await waitFor(() => expect(screen.queryByText('Удалённые каналы')).toBeNull())
    expect(screen.getByRole('button', { name: 'Комедии' })).toBeInTheDocument()
  })

  it('says so when the library has not enough for the channel yet', async () => {
    restored = false
    open()
    const dialog = await askDelete('Комедии')
    fireEvent.click(within(dialog).getByRole('button', { name: 'Удалить' }))
    const section = (await screen.findByText('Удалённые каналы')).closest('section')!
    fireEvent.click(within(section).getByRole('button', { name: 'Вернуть канал «Комедии»' }))
    expect(await screen.findByText(/появится, когда в медиатеке будет достаточно материала/)).toBeInTheDocument()
  })

  it('tells what happens to what was fetched for a channel with fetching', async () => {
    open()
    const dialog = await askDelete('Марафон')
    expect(within(dialog).getByText(/Скачанные для него фильмы и серии удалятся после срока хранения/)).toBeInTheDocument()
    fireEvent.click(within(dialog).getByRole('button', { name: 'Отмена' }))
    expect(calls.some((c) => c.method === 'DELETE')).toBe(false)
  })
})
