// «Настройки сервера → Медиатека → Папки на сервере»: the wizard's checks,
// kept for later. A pick is checked at once and applied on request (the
// server restarts); «Проверить» runs the write test again; a changed setup
// can go back to the startup settings.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { FOLDER_KINDS, restartTiming, type FolderKind, type FolderPick, type FolderRow, type StorageView } from '../../../api/storage'
import { StorageSection } from '../StorageSettings'
import { renderSettings } from './settingsHarness'

const json = (b: unknown, status = 200) => new Response(JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } })
const PATHS: Record<FolderKind, string> = { movies: 'movies', series: 'series', music: 'music', ebooks: 'books/ebooks', audiobooks: 'books/audiobooks', comics: 'books/manga' }

/** Each kind's folders as a save names them: one plain folder each, `over` in place. */
const picks = (over: Partial<Record<FolderKind, FolderPick[]>> = {}) =>
  Object.fromEntries(FOLDER_KINDS.map((k) => [k, over[k] ?? [{ path: PATHS[k], adult: false, unlisted: false }]]))

function view(over: Partial<StorageView> = {}, picked: Partial<Record<FolderKind, FolderPick[]>> = {}): StorageView {
  const have = ['movies', 'tv', 'music', '/mnt/adult']
  const folders: FolderRow[] = FOLDER_KINDS.flatMap((kind) =>
    (picked[kind] ?? [{ path: PATHS[kind] }]).map((p, i): FolderRow => {
      const found = have.includes(p.path)
      return {
        kind,
        path: p.path,
        abs: p.path.startsWith('/') ? p.path : `/media/${p.path}`,
        default: PATHS[kind],
        primary: i === 0,
        adult: Boolean(p.adult),
        unlisted: Boolean(p.unlisted),
        indexed: found ? 4 : 0,
        status: found ? 'ok' : 'missing',
        items: found ? 4 : 0,
        ...(kind === 'series' && !found && i === 0 ? { suggest: 'tv' } : {}),
      }
    }),
  )
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
    places: ['/media', '/mnt'],
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
      if (path === '/api/settings/storage/browse') {
        const at = new URL(String(input), 'http://x').searchParams.get('path') ?? '/media'
        return json({
          places: ['/media', '/mnt'],
          path: at,
          parent: at === '/media' || at === '/mnt' ? '' : at.slice(0, at.lastIndexOf('/')),
          dir: { path: at, status: 'ok', items: 2 },
          dirs: at === '/mnt' ? [{ name: 'adult', path: '/mnt/adult', items: 7 }] : [],
        })
      }
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
    expect(calls.find((c) => c.path === '/api/settings/storage/check')?.body).toEqual({ folders: picks() })
  })

  it('a pick is applied on request: saved, the server restarts, the view is read again', async () => {
    const calls = serve(view())
    render(<StorageSection />)
    // (the write test below lists the kinds again)
    const series = (await screen.findByRole('heading', { name: 'Сериалы' })).closest('section')!
    // nothing to apply before a pick
    expect(screen.queryByRole('button', { name: 'Применить и перезапустить' })).toBeNull()
    await userEvent.click(within(series).getByRole('button', { name: 'Использовать «tv»' }))
    const apply = await screen.findByRole('button', { name: 'Применить и перезапустить' })
    expect(calls.some((c) => c.method === 'PUT')).toBe(false)
    await userEvent.click(apply)
    await waitFor(() =>
      expect(calls.find((c) => c.method === 'PUT')?.body).toEqual({ folders: picks({ series: [{ path: 'tv', adult: false, unlisted: false }] }), confirmRemove: false }),
    )
    // restarted and read again: the pick is the saved one now, with a way back
    expect(await screen.findByRole('button', { name: 'Как в настройках запуска' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Применить и перезапустить' })).toBeNull()
    expect(calls.map((c) => c.path)).toContain('/api/health')
    await userEvent.click(screen.getByRole('button', { name: 'Как в настройках запуска' }))
    await waitFor(() => expect(calls.filter((c) => c.method === 'PUT').at(-1)?.body).toEqual({ reset: true, confirmRemove: false }))
  })

  it('a server that cannot restart itself says to restart it', async () => {
    serve(view({ restarts: false }), false)
    render(<StorageSection />)
    // (the write test below lists the kinds again)
    const series = (await screen.findByRole('heading', { name: 'Сериалы' })).closest('section')!
    await userEvent.click(within(series).getByRole('button', { name: 'Использовать «tv»' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Сохранить' }))
    expect(await screen.findByText('Перезапустите сервер, чтобы выбранные папки вступили в силу.')).toBeInTheDocument()
  })

  it('«Добавить папку…» browses the mounted folders and adds one; its marks are in «⋯»', async () => {
    const calls = serve(view())
    render(<StorageSection />)
    const movies = (await screen.findByRole('heading', { name: 'Фильмы' })).closest('section')!
    await userEvent.click(within(movies).getByRole('button', { name: 'Добавить папку…' }))
    const sheet = await screen.findByRole('dialog', { name: 'Папка: Фильмы' })
    // the other mount, then a folder in it
    await userEvent.click(within(sheet).getByRole('button', { name: /Где искать/ }))
    await userEvent.click(await screen.findByRole('menuitemradio', { name: /\/mnt/ }))
    await userEvent.click(await within(sheet).findByRole('button', { name: /adult/ }))
    expect(await within(sheet).findByText('Запись работает, 2 элемента')).toBeInTheDocument()
    await userEvent.click(within(sheet).getByRole('button', { name: 'Выбрать' }))
    await waitFor(() =>
      expect(calls.filter((c) => c.path === '/api/settings/storage/check').at(-1)?.body).toEqual({
        folders: picks({ movies: [{ path: 'movies', adult: false, unlisted: false }, { path: '/mnt/adult' }] }),
      }),
    )
    const row = (await screen.findByRole('heading', { name: 'Фильмы' })).closest('section')!.querySelectorAll('li')[1]!
    await userEvent.click(within(row).getByRole('button', { name: 'Действия с папкой /mnt/adult' }))
    await userEvent.click(await screen.findByRole('menuitemcheckbox', { name: /Только для взрослых/ }))
    await waitFor(() =>
      expect(calls.filter((c) => c.path === '/api/settings/storage/check').at(-1)?.body).toEqual({
        folders: picks({ movies: [{ path: 'movies', adult: false, unlisted: false }, { path: '/mnt/adult', adult: true, unlisted: false }] }),
      }),
    )
    expect(await within((await screen.findByRole('heading', { name: 'Фильмы' })).closest('section')!.querySelectorAll('li')[1]!).findByText('Только для взрослых')).toBeInTheDocument()
  })

  it('taking out a folder the library has titles from asks first, then saves with the confirmation', async () => {
    const two = { movies: [{ path: 'movies' }, { path: '/mnt/adult' }] }
    const calls = serve(view({}, two))
    render(<StorageSection />)
    const row = (await screen.findByRole('heading', { name: 'Фильмы' })).closest('section')!.querySelectorAll('li')[1]!
    await userEvent.click(within(row).getByRole('button', { name: 'Действия с папкой /mnt/adult' }))
    await userEvent.click(await screen.findByRole('menuitem', { name: /Убрать из медиатеки/ }))
    await userEvent.click(await screen.findByRole('button', { name: 'Применить и перезапустить' }))
    const ask = await screen.findByRole('alertdialog', { name: 'Убрать папки из медиатеки?' })
    expect(within(ask).getByText(/Файлы на диске останутся/)).toBeInTheDocument()
    expect(calls.some((c) => c.method === 'PUT')).toBe(false)
    await userEvent.click(within(ask).getByRole('button', { name: 'Убрать' }))
    await waitFor(() => expect((calls.find((c) => c.method === 'PUT')?.body as { confirmRemove: boolean }).confirmRemove).toBe(true))
  })

  it('a server without the checks shows nothing but a spinner', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json({})))
    render(<StorageSection />)
    expect(await screen.findByRole('status')).toBeInTheDocument()
    expect(screen.queryByText('Что лежит в /media')).toBeNull()
  })
})
