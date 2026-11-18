// «Настройки сервера → Медиатека → Папки на сервере»: the wizard's checks,
// kept for later. A pick is checked at once and applied on request (the
// server restarts); «Проверить» runs the write test again; a changed setup
// can go back to the startup settings.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { FOLDER_KINDS, restartTiming, type FolderKind, type FolderRow, type StorageView } from '../../../api/storage'
import { StorageSection } from '../StorageSettings'
import { renderSettings } from './settingsHarness'

const json = (b: unknown, status = 200) => new Response(JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } })
const PATHS: Record<FolderKind, string> = { movies: 'movies', series: 'series', music: 'music', ebooks: 'books/ebooks', audiobooks: 'books/audiobooks', comics: 'books/manga' }

function view(over: Partial<StorageView> = {}, picked: Partial<Record<FolderKind, string>> = {}): StorageView {
  const have = ['movies', 'tv', 'music']
  const folders: FolderRow[] = FOLDER_KINDS.map((kind) => {
    const path = picked[kind] ?? PATHS[kind]
    const found = have.includes(path)
    return { kind, path, abs: `/media/${path}`, default: PATHS[kind], status: found ? 'ok' : 'missing', items: found ? 4 : 0, ...(kind === 'series' && !found ? { suggest: 'tv' } : {}) }
  })
  return {
    root: { path: '/media', status: 'ok', items: 4 },
    entries: have.map((name) => ({ name, items: 4 })),
    folders,
    transcode: { path: '/media/tmp/antresol-hls', status: 'ok', items: 0 },
    custom: false,
    server: { uid: 1000, gid: 1000 },
    offline: false,
    canCreate: true,
    restarts: true,
    ...over,
  }
}

interface Call {
  method: string
  path: string
  body: unknown
}

function serve(first: StorageView, restarting = true) {
  const calls: Call[] = []
  let current = first
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = new URL(String(input), 'http://x').pathname
      const method = init?.method ?? 'GET'
      const body = init?.body ? JSON.parse(String(init.body)) : undefined
      calls.push({ method, path, body })
      if (path === '/api/settings/storage' && method === 'GET') return json(current)
      if (path === '/api/settings/storage/check') return json(view({ custom: current.custom, restarts: current.restarts }, body.folders))
      if (path === '/api/settings/storage' && method === 'PUT') {
        current = body.reset ? view({ restarts: current.restarts }) : view({ custom: true, restarts: current.restarts }, body.folders)
        return json({ ...current, restarting })
      }
      if (path === '/api/health') return json({ ok: true })
      return json({})
    }),
  )
  return calls
}

describe('«Папки на сервере» in the server settings', () => {
  const timing = { ...restartTiming }
  beforeEach(() => {
    restartTiming.first = 0
    restartTiming.every = 1
    restartTiming.max = 50
  })
  afterEach(() => {
    Object.assign(restartTiming, timing)
    vi.unstubAllGlobals()
  })

  it('is on the library screen of the server settings, with the folders and the write test', async () => {
    serve(view())
    renderSettings('library')
    expect(await screen.findByRole('button', { name: 'Проверить' })).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'Папки на сервере' })).toBeInTheDocument()
    // the pop-ups say what is under /media; the wizard lists it
    expect(screen.queryByRole('heading', { name: 'Что лежит в /media' })).toBeNull()
    expect(screen.getByText('Папка перекодирования')).toBeInTheDocument()
  })

  it('«Проверить» runs the checks again', async () => {
    const calls = serve(view())
    render(<StorageSection />)
    await userEvent.click(await screen.findByRole('button', { name: 'Проверить' }))
    await waitFor(() => expect(calls.some((c) => c.path === '/api/settings/storage/check')).toBe(true))
    expect(calls.find((c) => c.path === '/api/settings/storage/check')?.body).toEqual({ folders: PATHS })
  })

  it('a pick is applied on request: saved, the server restarts, the view is read again', async () => {
    const calls = serve(view())
    render(<StorageSection />)
    // (the write test below lists the kinds again)
    const series = (await screen.findAllByText('Сериалы'))[0]!.closest('li')!
    // nothing to apply before a pick
    expect(screen.queryByRole('button', { name: 'Применить и перезапустить' })).toBeNull()
    await userEvent.click(within(series).getByRole('button', { name: 'Использовать «tv»' }))
    const apply = await screen.findByRole('button', { name: 'Применить и перезапустить' })
    expect(calls.some((c) => c.method === 'PUT')).toBe(false)
    await userEvent.click(apply)
    await waitFor(() => expect(calls.find((c) => c.method === 'PUT')?.body).toEqual({ folders: { ...PATHS, series: 'tv' } }))
    // restarted and read again: the pick is the saved one now, with a way back
    expect(await screen.findByRole('button', { name: 'Как в настройках запуска' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Применить и перезапустить' })).toBeNull()
    expect(calls.map((c) => c.path)).toContain('/api/health')
    await userEvent.click(screen.getByRole('button', { name: 'Как в настройках запуска' }))
    await waitFor(() => expect(calls.filter((c) => c.method === 'PUT').at(-1)?.body).toEqual({ reset: true }))
  })

  it('a server that cannot restart itself says to restart it', async () => {
    serve(view({ restarts: false }), false)
    render(<StorageSection />)
    // (the write test below lists the kinds again)
    const series = (await screen.findAllByText('Сериалы'))[0]!.closest('li')!
    await userEvent.click(within(series).getByRole('button', { name: 'Использовать «tv»' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Сохранить' }))
    expect(await screen.findByText('Перезапустите сервер, чтобы выбранные папки вступили в силу.')).toBeInTheDocument()
  })

  it('a server without the checks shows nothing but a spinner', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json({})))
    render(<StorageSection />)
    expect(await screen.findByRole('status')).toBeInTheDocument()
    expect(screen.queryByText('Что лежит в /media')).toBeNull()
  })
})
