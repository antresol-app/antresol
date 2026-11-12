// «Внешние источники» against a fake server (synthetic data, example.test
// hosts): the channel table stays virtualised at 3 000 rows; a group's bulk
// switch is one PATCH; the settings never echo a secret (the URL field is
// empty with the mask as its placeholder, and a save sends no URL unless one
// is typed); the pending-drop notice confirms or keeps; a manual guide
// binding; a device's set.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router'
import { SourceChannels } from '../SourceChannels'
import { SourcesPage } from '../SourcesPage'

const GROUPS = ['Новости', 'Спорт', 'Кино']
const CHANNELS = Array.from({ length: 3000 }, (_, i) => ({
  memberId: i + 1,
  channelId: 1000 + i,
  name: `Канал ${i + 1} HD`,
  providerName: `Канал ${i + 1} HD`,
  tvgId: `ch${i + 1}.example`,
  group: GROUPS[i % 3]!,
  logo: '',
  shown: i % 4 !== 0,
  number: 100 + i,
  numberManual: false,
  category: GROUPS[i % 3]!,
  epg: { xmlId: i % 5 ? `ch${i + 1}.example` : '', epgUrlId: 1, how: i % 5 ? 'tvg-id' : '', manual: false },
  why: 'source-default',
  whyText: '',
  present: true,
  new: i === 2,
}))
const SOURCE = {
  id: 1, name: 'Демо-провайдер', kind: 'm3u', url: 'http://example.test/…***', xtream: null, defaultShown: null, hideUrls: false,
  presets: { timeshift: true, adult: true }, refreshHours: 24, refreshOnOpen: true, position: 0, profiles: [],
  lastOk: new Date(Date.now() - 3_600_000).toISOString(), lastTry: new Date().toISOString(), lastError: '', pendingDrop: 412,
  counts: { total: 1300, shown: 860, hidden: 440, new: 1 },
  epg: [{ id: 1, url: 'http://epg.example.test/…***', auto: false, position: 0, refreshHours: 24, refreshOnOpen: false, lastOk: null, lastTry: null, lastError: '', matched: 2400 }],
}
const DEVICES = [
  { userId: 1, profile: 'Анна', kind: 'app', deviceId: 'dev-1', linkId: '', name: 'Гостиная', client: 'tvos', clientName: 'Apple TV', lastSeen: new Date().toISOString(), setId: 0, defaultSetId: 0 },
]
DEVICES.push({ userId: 1, profile: 'Анна', kind: 'iptv', deviceId: '', linkId: 'ab12', name: 'Кухня', client: 'iptv', clientName: 'IPTV', lastSeen: null as unknown as string, setId: 0, defaultSetId: 0 })
const SETS = [{ id: 2, name: 'Подписка А', sources: [1], hiddenGroups: [], hiddenChannels: [] }]

type Call = { method: string; url: string; body?: unknown }
let calls: Call[] = []
const json = (body: unknown, status = 200) => Promise.resolve(new Response(body === null ? null : JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

beforeEach(() => {
  calls = []
  vi.spyOn(globalThis, 'fetch').mockImplementation((input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url
    const method = (init?.method ?? 'GET').toUpperCase()
    calls.push({ method, url, body: init?.body ? JSON.parse(String(init.body)) : undefined })
    if (method === 'GET' && url === '/api/tv/sources') return json({ sources: [SOURCE], xtreamEnabled: false })
    if (method === 'GET' && url.startsWith('/api/tv/sources/1/channels')) return json({ groups: [], channels: CHANNELS, total: CHANNELS.length })
    if (method === 'GET' && url === '/api/tv/sets') return json({ sets: SETS })
    if (method === 'GET' && url === '/api/tv/devices') return json({ devices: DEVICES })
    if (method === 'GET' && url === '/api/users') return json([{ id: 1, name: 'Анна', isAdmin: true, isKid: false }])
    if (method === 'GET' && url.startsWith('/api/tv/epg/search')) return json({ results: [{ epgUrlId: 1, sourceId: 1, xmlId: 'news24.example', names: ['Новости 24', 'Новости 24 HD'] }] })
    if (method === 'GET' && url === '/api/tv/categories') return json({ categories: GROUPS })
    if (url.endsWith('/confirm')) return json({ ok: true, result: { total: 888, new: 0, lost: 412, shown: 860, hidden: 28, pendingDrop: 0 }, source: { ...SOURCE, pendingDrop: 0 } })
    if (url.endsWith('/reject')) return json({ ...SOURCE, pendingDrop: 0 })
    if (method === 'PUT' && url === '/api/tv/sources/1') return json(SOURCE)
    if (method === 'PUT' && url === '/api/tv/sets/2') return json({ id: 2, ...(JSON.parse(String(init!.body)) as object) })
    if (method === 'PATCH') return json({ updated: 1 })
    return json(null, 204)
  })
})
afterEach(() => vi.restoreAllMocks())

const at = (path: string) =>
  render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/tv/manage/sources" element={<SourcesPage />} />
        <Route path="/tv/manage/sources/:id" element={<SourceChannels />} />
      </Routes>
    </MemoryRouter>,
  )
const writes = () => calls.filter((c) => c.method !== 'GET')

describe('a source’s page', () => {
  it('renders fewer than 100 of 3 000 rows', async () => {
    at('/tv/manage/sources/1')
    const table = await screen.findByRole('table')
    expect(table).toHaveAttribute('aria-rowcount', '3001')
    expect(within(table).getAllByRole('row').length).toBeLessThan(100)
  })

  it('shows or hides a whole group with one call', async () => {
    at('/tv/manage/sources/1')
    await screen.findByRole('table')
    fireEvent.click(screen.getByRole('radio', { name: /^Спорт/ }))
    fireEvent.click(screen.getByRole('button', { name: 'Скрыть все' }))
    await waitFor(() => expect(writes().filter((c) => c.url === '/api/tv/groups')).toEqual([{ method: 'PATCH', url: '/api/tv/groups', body: { sourceId: 1, group: 'Спорт', shown: false } }]))
    expect(screen.getByText('Показано 0 из 1 000')).toBeInTheDocument()
  })

  it('confirms the shorter list', async () => {
    at('/tv/manage/sources/1')
    await screen.findByText('Пропало 412 каналов из 1 300 — подтвердить?')
    fireEvent.click(screen.getByRole('button', { name: 'Подтвердить' }))
    await waitFor(() => expect(writes().map((c) => c.url)).toContain('/api/tv/sources/1/confirm'))
    await waitFor(() => expect(screen.queryByText(/Пропало 412/)).toBeNull())
  })

  it('never echoes the stored address and sends none unless typed', async () => {
    at('/tv/manage/sources/1')
    await screen.findByRole('table')
    fireEvent.click(screen.getByRole('button', { name: 'Ещё' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Настройки источника…' }))
    const field = await screen.findByLabelText('Ссылка на плейлист')
    expect(field).toHaveValue('')
    expect(field).toHaveAttribute('placeholder', 'http://example.test/…***')
    fireEvent.click(screen.getByRole('button', { name: 'Сохранить' }))
    await waitFor(() => expect(writes().some((c) => c.method === 'PUT' && c.url === '/api/tv/sources/1')).toBe(true))
    const put = writes().find((c) => c.method === 'PUT')!.body as Record<string, unknown>
    expect(put).not.toHaveProperty('url')
    expect(JSON.stringify(put)).not.toContain('…***')
    expect(put.epg).toEqual([{ id: 1, refreshHours: 24, refreshOnOpen: false }])
  })

  it('binds a channel’s guide by hand', async () => {
    at('/tv/manage/sources/1')
    await screen.findByRole('table')
    fireEvent.click(screen.getAllByRole('button', { name: /^Программа:/ })[0]!)
    fireEvent.click(await screen.findByRole('button', { name: 'Привязать…' }))
    fireEvent.click(await screen.findByRole('button', { name: 'Привязать к «Новости 24»' }, { timeout: 2000 }))
    await waitFor(() => expect(writes()).toContainEqual({ method: 'PUT', url: '/api/tv/channels/1000/epg', body: { epgUrlId: 1, xmlId: 'news24.example' } }))
  })
})

describe('the sources page', () => {
  it('keeps the old list from the source’s row', async () => {
    at('/tv/manage/sources')
    await screen.findByText('Пропало 412 каналов из 1 300 — подтвердить?')
    fireEvent.click(screen.getByRole('button', { name: 'Оставить старый список' }))
    await waitFor(() => expect(writes().map((c) => c.url)).toContain('/api/tv/sources/1/reject'))
  })

  it('assigns a set to a device', async () => {
    at('/tv/manage/sources')
    await screen.findByText('Гостиная')
    fireEvent.click(screen.getAllByRole('button', { name: /^Набор: Все мои каналы/ })[0]!)
    fireEvent.click(await screen.findByRole('menuitemradio', { name: 'Подписка А' }))
    await waitFor(() => expect(writes()).toContainEqual({ method: 'PUT', url: '/api/tv/devices/assign', body: { userId: 1, deviceId: 'dev-1', setId: 2 } }))
  })

  it('deletes an IPTV link after asking', async () => {
    at('/tv/manage/sources')
    await screen.findByText('Кухня')
    fireEvent.click(screen.getByRole('button', { name: 'Ссылка «Кухня»' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Удалить ссылку' }))
    expect(await screen.findByText('Удалить ссылку «Кухня»?')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Удалить ссылку' }))
    await waitFor(() => expect(writes()).toContainEqual({ method: 'DELETE', url: '/api/tv/links/ab12', body: undefined }))
    await waitFor(() => expect(screen.queryByText('Кухня')).toBeNull())
  })

  it('leaves a category and a single channel out of a set', async () => {
    at('/tv/manage/sources')
    await screen.findByText('Подписка А')
    fireEvent.click(screen.getByRole('button', { name: 'Набор «Подписка А»' }))
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Изменить' }))
    fireEvent.click(await screen.findByRole('switch', { name: 'Категория «Спорт» в наборе' }))
    fireEvent.change(screen.getByRole('searchbox', { name: 'Поиск канала' }), { target: { value: 'Канал 3 HD' } })
    fireEvent.click(await screen.findByRole('switch', { name: '«Канал 3 HD» в наборе' }))
    fireEvent.click(screen.getByRole('button', { name: 'Сохранить' }))
    await waitFor(() =>
      expect(writes()).toContainEqual({ method: 'PUT', url: '/api/tv/sets/2', body: { name: 'Подписка А', sources: [1], hiddenGroups: ['Спорт'], hiddenChannels: [1002] } }),
    )
  })
})
