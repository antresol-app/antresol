// «Франшизы» against a fake server (synthetic titles): the list with its
// source and channel; the order sheet saves the new order with a removed
// title hidden; a manual order can be reset; without TMDB the page says so.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { FranchisesPage } from '../FranchisesPage'

const SAGA = {
  key: 'c10',
  name: 'Космическая сага',
  source: 'ai',
  manual: true,
  channelId: 7,
  items: [
    { id: 1, kind: 'movie', title: 'Эпизод первый', year: 1999 },
    { id: 2, kind: 'series', title: 'Войны', year: 2008 },
    { id: 3, kind: 'movie', title: 'Эпизод четвёртый', year: 1977 },
  ],
}
const PAIR = { key: 'c20', name: 'Пара', source: 'tmdb', manual: false, items: [{ id: 4, kind: 'movie', title: 'А' }, { id: 5, kind: 'movie', title: 'Б' }] }
const GONE = { key: 'c30', name: 'Удалённая', source: 'tmdb', manual: false, channelRemoved: true, items: [{ id: 8, kind: 'movie', title: 'Д' }, { id: 9, kind: 'movie', title: 'Е' }] }
const SHARED = { key: 'wQ1', name: 'Общая', source: 'wikidata', manual: false, channelId: 8, items: [{ id: 6, kind: 'movie', title: 'В' }, { id: 7, kind: 'series', title: 'Г' }] }

type Call = { method: string; url: string; body?: unknown }
let calls: Call[] = []
let available = true
let list: object[] = []
const json = (body: unknown, status = 200) => Promise.resolve(new Response(body === null ? null : JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }))

beforeEach(() => {
  calls = []
  available = true
  list = [SAGA, PAIR, SHARED]
  vi.spyOn(globalThis, 'fetch').mockImplementation((input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url
    const method = (init?.method ?? 'GET').toUpperCase()
    calls.push({ method, url, body: init?.body ? JSON.parse(String(init.body)) : undefined })
    if (method === 'GET' && url === '/api/channels/franchises')
      return json({ available, status: { running: false, model: 'http://llm|m', at: new Date().toISOString() }, franchises: available ? list : [] })
    if (method === 'PUT' || method === 'DELETE') return json(SAGA)
    return json(null, 202)
  })
})
afterEach(() => vi.restoreAllMocks())

const open = () =>
  render(
    <MemoryRouter>
      <FranchisesPage />
    </MemoryRouter>,
  )

describe('«Франшизы»', () => {
  it('lists the franchises with their source and channel', async () => {
    open()
    expect(await screen.findByText('Космическая сага')).toBeInTheDocument()
    expect(screen.getByText('ИИ')).toBeInTheDocument()
    expect(screen.getByText('TMDB')).toBeInTheDocument()
    expect(screen.getByText('Wikidata')).toBeInTheDocument()
    expect(screen.getByText('Свой порядок')).toBeInTheDocument()
    expect(screen.getByText('ваш порядок · 3 фильма или сериала · есть канал')).toBeInTheDocument()
    expect(screen.getByText('по выходу · 2 фильма или сериала · есть канал')).toBeInTheDocument()
    expect(screen.getByText('по выходу · 2 фильма или сериала · канала нет — мало для показа')).toBeInTheDocument()
    expect(screen.getByText(/Порядок по сюжету расставляет ИИ-помощник/)).toBeInTheDocument()
  })

  it('says a franchise channel was deleted, not that the library has too little for it', async () => {
    list = [GONE]
    open()
    expect(await screen.findByText('по выходу · 2 фильма или сериала · канал удалён — вернуть его можно на странице «Управление каналами»')).toBeInTheDocument()
    expect(screen.queryByText(/мало для показа/)).toBeNull()
  })

  it('saves the new order with a removed title hidden', async () => {
    open()
    await screen.findByText('Космическая сага')
    fireEvent.click(screen.getAllByRole('button', { name: 'Порядок' })[0]!)
    const dialog = await screen.findByRole('dialog')
    fireEvent.click(within(dialog).getByRole('button', { name: 'Выше: Эпизод четвёртый' }))
    fireEvent.click(within(dialog).getByRole('button', { name: 'Выше: Эпизод четвёртый' }))
    fireEvent.click(within(dialog).getByRole('button', { name: 'Убрать: Войны' }))
    expect(within(dialog).getByText('Убраны из канала')).toBeInTheDocument()
    fireEvent.click(within(dialog).getByRole('button', { name: 'Сохранить' }))
    await waitFor(() =>
      expect(calls.filter((c) => c.method === 'PUT')).toEqual([{ method: 'PUT', url: '/api/channels/franchises/c10/order', body: { order: [3, 1], hidden: [2] } }]),
    )
  })

  it('resets a manual order', async () => {
    open()
    await screen.findByText('Космическая сага')
    fireEvent.click(screen.getAllByRole('button', { name: 'Порядок' })[0]!)
    const dialog = await screen.findByRole('dialog')
    fireEvent.click(within(dialog).getByRole('button', { name: 'Сбросить порядок' }))
    await waitFor(() => expect(calls.some((c) => c.method === 'DELETE' && c.url === '/api/channels/franchises/c10/order')).toBe(true))
  })

  it('asks for a recompute', async () => {
    open()
    await screen.findByText('Космическая сага')
    fireEvent.click(screen.getByRole('button', { name: 'Пересчитать' }))
    await waitFor(() => expect(calls.some((c) => c.method === 'POST' && c.url === '/api/channels/franchises/refresh')).toBe(true))
  })

  it('says franchises need TMDB', async () => {
    available = false
    open()
    expect(await screen.findByText('Франшизы недоступны')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Пересчитать' })).toBeNull()
  })
})
