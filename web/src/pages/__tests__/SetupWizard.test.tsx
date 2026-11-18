// The first-run wizard after the administrator: media folders (what the
// server sees, which folder is which, «Создать»), write access with the fix
// to copy, the graphics card, then «Готово» starts the scan. Every step can
// be skipped, and a wizard that is over is not reachable.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router'
import { FOLDER_KINDS, restartTiming, type DirCheck, type FolderKind, type FolderRow, type StorageView } from '../../api/storage'
import type { TranscodingView } from '../../api/types'
import { SetupWizard } from '../SetupWizard'

const json = (b: unknown, status = 200) => new Response(JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } })

const PATHS: Record<FolderKind, string> = { movies: 'movies', series: 'series', music: 'music', ebooks: 'books/ebooks', audiobooks: 'books/audiobooks', comics: 'books/manga' }

const dir = (path: string, over: Partial<DirCheck> = {}): DirCheck => ({ path, status: 'ok', items: 3, ...over })

/** A server whose «series» folder is called «tv». */
function storageView(over: Partial<StorageView> = {}, picked: Partial<Record<FolderKind, string>> = {}): StorageView {
  const have = ['movies', 'tv', 'music', 'books/ebooks']
  const folders: FolderRow[] = FOLDER_KINDS.map((kind) => {
    const path = picked[kind] ?? PATHS[kind]
    const found = have.includes(path)
    return {
      ...dir(path, found ? { items: path === 'tv' ? 12 : 3 } : { status: 'missing', items: 0 }),
      kind,
      abs: `/media/${path}`,
      default: PATHS[kind],
      ...(kind === 'series' && !found ? { suggest: 'tv' } : {}),
    }
  })
  return {
    root: dir('/media', { items: 5 }),
    entries: have.map((name) => ({ name, items: name === 'tv' ? 12 : 3 })),
    folders,
    transcode: dir('/media/tmp/antresol-hls', { items: 0 }),
    custom: false,
    server: { uid: 1000, gid: 1000 },
    offline: false,
    canCreate: true,
    restarts: true,
    ...over,
  }
}

const tc = (over: Partial<TranscodingView['status']> = {}, devices: string[] = ['/dev/dri/renderD128']): TranscodingView => ({
  settings: { mode: 'auto', device: '', tonemap: true, hevc: true },
  defaults: { mode: 'auto', device: '', tonemap: true, hevc: true },
  custom: false,
  devices,
  status: { mode: 'qsv', name: 'Intel Quick Sync', requested: 'auto', hevc: true, tonemapGpu: true, tonemapCpu: true, probes: [{ mode: 'qsv', ok: true }], probedAt: '2026-11-18T10:00:00Z', ...over },
})
const noGpu = () => tc({ mode: 'none', name: 'software', hevc: false, tonemapGpu: false, probes: [] }, [])

interface Call {
  method: string
  path: string
  body: unknown
}

interface Opts {
  pending?: boolean | 403
  view?: StorageView
  /** the view a check of the picked folders answers */
  check?: (body: { folders: Record<FolderKind, string> }) => StorageView
  put?: (body: unknown) => StorageView & { restarting: boolean }
  transcoding?: TranscodingView
  created?: DirCheck
  /** «Готово» is refused: another tab ended the wizard first */
  finish?: 403
}

function serve(o: Opts = {}) {
  const calls: Call[] = []
  let current = o.view ?? storageView()
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = new URL(String(input), 'http://x').pathname
      const method = init?.method ?? 'GET'
      const body = init?.body ? JSON.parse(String(init.body)) : undefined
      calls.push({ method, path, body })
      const key = `${method} ${path}`
      switch (key) {
        case 'GET /api/setup/wizard':
          return o.pending === 403 ? json({ error: 'Недостаточно прав' }, 403) : json({ pending: o.pending ?? true })
        case 'GET /api/settings/storage':
          return json(current)
        case 'POST /api/settings/storage/check':
          return json(o.check ? o.check(body) : current)
        case 'PUT /api/settings/storage': {
          const out = o.put ? o.put(body) : { ...current, restarting: false }
          const { restarting: _r, ...v } = out
          current = v
          return json(out)
        }
        case 'POST /api/settings/storage/create':
          return json(o.created ?? dir(body.path))
        case 'GET /api/health':
          return json({ ok: true })
        case 'GET /api/settings/transcoding':
          return json(o.transcoding ?? tc())
        case 'POST /api/settings/transcoding/probe':
          return json(o.transcoding ?? tc())
        case 'POST /api/setup/wizard/finish':
          return o.finish === 403 ? json({ error: 'Первая настройка уже завершена' }, 403) : json({ pending: false })
      }
      return json({}, 404)
    }),
  )
  return calls
}

function open() {
  render(
    <MemoryRouter initialEntries={['/setup/wizard']}>
      <Routes>
        <Route path="/setup/wizard" element={<SetupWizard />} />
        <Route path="/" element={<h1>Главная</h1>} />
      </Routes>
    </MemoryRouter>,
  )
}

const button = (name: string | RegExp) => screen.getByRole('button', { name })
const finished = (calls: Call[]) => calls.some((c) => c.path === '/api/setup/wizard/finish' && c.method === 'POST')

describe('the first-run wizard', () => {
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

  it('is not reachable once it is over, or for anyone but an administrator', async () => {
    serve({ pending: false })
    open()
    expect(await screen.findByRole('heading', { name: 'Главная' })).toBeInTheDocument()
    vi.unstubAllGlobals()
    cleanup()
    serve({ pending: 403 })
    open()
    expect(await screen.findByRole('heading', { name: 'Главная' })).toBeInTheDocument()
  })

  it('step 1 shows what the server sees under the media folder and the status of each library folder', async () => {
    serve()
    open()
    expect(await screen.findByRole('heading', { name: 'Медиа-папки' })).toBeInTheDocument()
    expect(screen.getByText('Шаг 1 из 3')).toBeInTheDocument()
    // the folders under /media, with their counts
    const root = screen.getByRole('heading', { name: 'Что лежит в /media' }).parentElement!
    expect(within(root).getByText('tv')).toBeInTheDocument()
    expect(within(root).getByText('12 элементов')).toBeInTheDocument()
    expect(within(root).getByText('books/ebooks')).toBeInTheDocument()
    // found / missing, with a count and a way out
    const movies = screen.getByText('Фильмы').closest('li')!
    expect(within(movies).getByText('Найдена, 3 элемента')).toBeInTheDocument()
    const series = screen.getByText('Сериалы').closest('li')!
    expect(within(series).getByText('Такой папки нет. Похожая: tv')).toBeInTheDocument()
    expect(within(series).getByRole('button', { name: 'Создать' })).toBeInTheDocument()
    expect(within(series).getByRole('button', { name: 'Использовать «tv»' })).toBeInTheDocument()
    expect(within(movies).queryByRole('button', { name: 'Создать' })).toBeNull()
  })

  it('a folder that is not writable is said so in its row', async () => {
    const v = storageView()
    v.folders[2] = { ...v.folders[2]!, status: 'readonly', why: 'denied', error: 'permission denied' }
    serve({ view: v })
    open()
    const music = (await screen.findByText('Музыка')).closest('li')!
    expect(within(music).getByText('Найдена, но сервер не может писать в неё')).toBeInTheDocument()
  })

  it('picking the folder that looks like it checks it at once, and «Далее» saves the picks and waits for the restart', async () => {
    const calls = serve({
      check: (b) => storageView({}, b.folders),
      put: (b) => ({ ...storageView({ custom: true }, (b as { folders: Record<FolderKind, string> }).folders), restarting: true }),
    })
    open()
    const series = (await screen.findByText('Сериалы')).closest('li')!
    await userEvent.click(within(series).getByRole('button', { name: 'Использовать «tv»' }))
    // checked, nothing saved yet
    await waitFor(() => expect(within(screen.getByText('Сериалы').closest('li')!).getByText('Найдена, 12 элементов')).toBeInTheDocument())
    expect(calls.find((c) => c.path === '/api/settings/storage/check')?.body).toEqual({ folders: { ...PATHS, series: 'tv' } })
    expect(calls.some((c) => c.method === 'PUT')).toBe(false)
    await userEvent.click(button('Далее'))
    expect(await screen.findByRole('heading', { name: 'Права на запись' })).toBeInTheDocument()
    expect(calls.find((c) => c.method === 'PUT')?.body).toEqual({ folders: { ...PATHS, series: 'tv' } })
    // it waited for the server to answer again, then read it
    const health = calls.findIndex((c) => c.path === '/api/health')
    const reread = calls.findIndex((c, i) => i > health && c.path === '/api/settings/storage' && c.method === 'GET')
    expect(health).toBeGreaterThan(calls.findIndex((c) => c.method === 'PUT'))
    expect(reread).toBeGreaterThan(health)
    expect(screen.getByText('Шаг 2 из 3')).toBeInTheDocument()
  })

  it('«Далее» with nothing changed saves nothing', async () => {
    const calls = serve()
    open()
    await screen.findByRole('heading', { name: 'Медиа-папки' })
    await userEvent.click(button('Далее'))
    expect(await screen.findByRole('heading', { name: 'Права на запись' })).toBeInTheDocument()
    expect(calls.some((c) => c.method === 'PUT')).toBe(false)
  })

  it('a folder picked and then skipped stays unsaved', async () => {
    const calls = serve({ check: (b) => storageView({}, b.folders) })
    open()
    const series = (await screen.findByText('Сериалы')).closest('li')!
    await userEvent.click(within(series).getByRole('button', { name: 'Использовать «tv»' }))
    await waitFor(() => expect(within(screen.getByText('Сериалы').closest('li')!).getByText('Найдена, 12 элементов')).toBeInTheDocument())
    await userEvent.click(button('Пропустить'))
    expect(await screen.findByRole('heading', { name: 'Права на запись' })).toBeInTheDocument()
    expect(calls.some((c) => c.method === 'PUT')).toBe(false)
    // the view went back to the saved folders
    const checks = calls.filter((c) => c.path === '/api/settings/storage/check')
    expect(checks.at(-1)?.body).toEqual({ folders: PATHS })
  })

  it('«Создать» makes the missing folder and checks again', async () => {
    const calls = serve()
    open()
    const series = (await screen.findByText('Сериалы')).closest('li')!
    await userEvent.click(within(series).getByRole('button', { name: 'Создать' }))
    await waitFor(() => expect(calls.some((c) => c.path === '/api/settings/storage/create')).toBe(true))
    expect(calls.find((c) => c.path === '/api/settings/storage/create')?.body).toEqual({ path: 'series' })
    await waitFor(() => expect(calls.some((c) => c.path === '/api/settings/storage/check')).toBe(true))
  })

  it('a folder that could not be made says why', async () => {
    serve({ created: { path: 'series', status: 'readonly', items: 0, why: 'denied', error: 'mkdir /media/series: permission denied' } })
    open()
    const series = (await screen.findByText('Сериалы')).closest('li')!
    await userEvent.click(within(series).getByRole('button', { name: 'Создать' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Не удалось создать папку: mkdir /media/series: permission denied')
  })

  it('step 2 is a real write test: a failure comes with who the server runs as and commands to copy', async () => {
    const v = storageView()
    v.root = dir('/media', { status: 'readonly', why: 'denied', error: 'open /media/.antresol-write-test-1: permission denied' })
    v.fix = {
      paths: ['/media'],
      docker: true,
      uid: 1000,
      gid: 1000,
      owner: '1001:1002',
      chown: 'docker compose run --rm --no-deps --user root --entrypoint chown antresol -R 1000:1000 /media',
      composeUser: 'user: "1001:1002"',
      configChown: 'docker compose run --rm --no-deps --user root --entrypoint chown antresol -R 1001:1002 /config',
    }
    const writeText = vi.fn(async () => undefined)
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
    const calls = serve({ view: v })
    open()
    await screen.findByRole('heading', { name: 'Медиа-папки' })
    await userEvent.click(button('Далее'))
    expect(await screen.findByRole('heading', { name: 'Права на запись' })).toBeInTheDocument()
    const root = screen.getByText('Медиа-папка').closest('li')!
    expect(within(root).getByText('Нет прав на запись')).toBeInTheDocument()
    expect(screen.getByText('Сервер работает от пользователя 1000:1000, владелец папки — 1001:1002.')).toBeInTheDocument()
    expect(screen.getByText(v.fix.chown!)).toBeInTheDocument()
    expect(screen.getByText('user: "1001:1002"')).toBeInTheDocument()
    expect(screen.getByText(v.fix.configChown!)).toBeInTheDocument()
    expect(screen.queryByText('Всё в порядке: сервер может писать в свои папки.')).toBeNull()
    // copy-paste ready: the button copies the command as it is
    const copy = screen.getAllByRole('button', { name: 'Скопировать' })
    await userEvent.click(copy[0]!)
    expect(writeText).toHaveBeenCalledWith(v.fix.chown)
    expect(await screen.findByRole('button', { name: 'Скопировано' })).toBeInTheDocument()
    // after the fix: «Проверить» runs the test again
    await userEvent.click(button('Проверить'))
    await waitFor(() => expect(calls.filter((c) => c.path === '/api/settings/storage/check').length).toBe(1))
  })

  it('a read-only mount is told to drop :ro, with no chown', async () => {
    const v = storageView()
    v.root = dir('/media', { status: 'readonly', why: 'readonlyFS', error: 'read-only file system' })
    v.fix = { paths: ['/media'], docker: true, uid: 1000, gid: 1000 }
    serve({ view: v })
    open()
    await screen.findByRole('heading', { name: 'Медиа-папки' })
    await userEvent.click(button('Далее'))
    expect(await screen.findByText('Папка подключена только для чтения')).toBeInTheDocument()
    expect(screen.getByText(/Уберите «:ro»/)).toBeInTheDocument()
    expect(screen.queryByText(/entrypoint chown/)).toBeNull()
  })

  it('step 2 with everything writable says so', async () => {
    serve()
    open()
    await screen.findByRole('heading', { name: 'Медиа-папки' })
    await userEvent.click(button('Далее'))
    expect(await screen.findByText('Всё в порядке: сервер может писать в свои папки.')).toBeInTheDocument()
    expect(screen.getByText('Папка перекодирования')).toBeInTheDocument()
    // a folder that is not there yet is not a failure
    expect(within(screen.getByText('Сериалы').closest('li')!).getByText('Папки пока нет')).toBeInTheDocument()
  })

  it('step 3 shows the hardware status and, with no GPU passed, what to uncomment; «Готово» starts the scan', async () => {
    const calls = serve({ transcoding: noGpu() })
    open()
    await screen.findByRole('heading', { name: 'Медиа-папки' })
    await userEvent.click(button('Пропустить'))
    await userEvent.click(button('Пропустить'))
    expect(await screen.findByRole('heading', { name: 'Видеокарта' })).toBeInTheDocument()
    expect(screen.getByText('Шаг 3 из 3')).toBeInTheDocument()
    expect(await screen.findByText('Процессор: контейнер не видит видеокарту — откройте /dev/dri в compose.yml')).toBeInTheDocument()
    expect(screen.getByText(/Видеокарту в контейнер не передали/)).toBeInTheDocument()
    expect(screen.getByText(/devices:\s+- \/dev\/dri:\/dev\/dri/)).toBeInTheDocument()
    expect(screen.getByText('getent group render | cut -d: -f3')).toBeInTheDocument()
    expect(finished(calls)).toBe(false)
    // «Проверить заново» asks the server to test again
    await userEvent.click(button('Проверить заново'))
    await waitFor(() => expect(calls.some((c) => c.path === '/api/settings/transcoding/probe')).toBe(true))
    await userEvent.click(button('Готово'))
    expect(await screen.findByRole('heading', { name: 'Главная' })).toBeInTheDocument()
    expect(finished(calls)).toBe(true)
  })

  it('step 3 with a working GPU shows its status and no hint', async () => {
    serve({ transcoding: tc() })
    open()
    await screen.findByRole('heading', { name: 'Медиа-папки' })
    await userEvent.click(button('Пропустить'))
    await userEvent.click(button('Пропустить'))
    expect(await screen.findByText('Видеокарта Intel Quick Sync: аппаратное декодирование и кодирование работают · HEVC · тонмаппинг на видеокарте')).toBeInTheDocument()
    expect(screen.queryByText(/Видеокарту в контейнер не передали/)).toBeNull()
  })

  it('every step is skippable: skipping them all finishes and starts the scan', async () => {
    const calls = serve()
    open()
    await screen.findByRole('heading', { name: 'Медиа-папки' })
    await userEvent.click(button('Пропустить'))
    await screen.findByRole('heading', { name: 'Права на запись' })
    await userEvent.click(button('Пропустить'))
    await screen.findByRole('heading', { name: 'Видеокарта' })
    expect(finished(calls)).toBe(false)
    await userEvent.click(button('Пропустить'))
    expect(await screen.findByRole('heading', { name: 'Главная' })).toBeInTheDocument()
    expect(finished(calls)).toBe(true)
    expect(calls.some((c) => c.method === 'PUT')).toBe(false)
  })

  it('«Пропустить настройку» ends it from the first step', async () => {
    const calls = serve()
    open()
    await screen.findByRole('heading', { name: 'Медиа-папки' })
    await userEvent.click(button('Пропустить настройку'))
    expect(await screen.findByRole('heading', { name: 'Главная' })).toBeInTheDocument()
    expect(finished(calls)).toBe(true)
  })

  it('«Назад» goes back a step', async () => {
    serve()
    open()
    await screen.findByRole('heading', { name: 'Медиа-папки' })
    await userEvent.click(button('Далее'))
    await screen.findByRole('heading', { name: 'Права на запись' })
    await userEvent.click(button('Назад'))
    expect(await screen.findByRole('heading', { name: 'Медиа-папки' })).toBeInTheDocument()
  })

  it('a server that does not come back after a restart is said so, and the step stays', async () => {
    restartTiming.max = 3
    let health = 0
    serve({ put: (b) => ({ ...storageView({}, (b as { folders: Record<FolderKind, string> }).folders), restarting: true }), check: (b) => storageView({}, b.folders) })
    const f = globalThis.fetch as unknown as ReturnType<typeof vi.fn>
    const inner = f.getMockImplementation() as (i: RequestInfo | URL, n?: RequestInit) => Promise<Response>
    f.mockImplementation(async (input: RequestInfo | URL, init?: RequestInit) => {
      if (String(input).endsWith('/api/health')) {
        health++
        throw new TypeError('down')
      }
      return inner(input, init)
    })
    open()
    const series = (await screen.findByText('Сериалы')).closest('li')!
    await userEvent.click(within(series).getByRole('button', { name: 'Использовать «tv»' }))
    await waitFor(() => expect(within(screen.getByText('Сериалы').closest('li')!).getByText('Найдена, 12 элементов')).toBeInTheDocument())
    await userEvent.click(button('Далее'))
    expect(await screen.findByText(/Сервер отвечает дольше обычного/)).toBeInTheDocument()
    expect(health).toBeGreaterThan(1)
    expect(screen.getByRole('heading', { name: 'Медиа-папки' })).toBeInTheDocument()
  })
  it('waits for a NEW process after a restart: the old one may go on answering while it shuts down', async () => {
    const calls = serve({
      put: (b) => ({ ...storageView({}, (b as { folders: Record<FolderKind, string> }).folders), restarting: true, bootId: 'old-run' }) as never,
      check: (b) => storageView({}, b.folders),
    })
    const f = globalThis.fetch as unknown as ReturnType<typeof vi.fn>
    const inner = f.getMockImplementation() as (i: RequestInfo | URL, n?: RequestInit) => Promise<Response>
    let health = 0
    f.mockImplementation(async (input: RequestInfo | URL, init?: RequestInit) => {
      if (String(input).endsWith('/api/health')) {
        health++
        // three answers from the old process, then the new one
        return json({ ok: true, bootId: health <= 3 ? 'old-run' : 'new-run' })
      }
      return inner(input, init)
    })
    open()
    const series = (await screen.findByText('Сериалы')).closest('li')!
    await userEvent.click(within(series).getByRole('button', { name: 'Использовать «tv»' }))
    await waitFor(() => expect(within(screen.getByText('Сериалы').closest('li')!).getByText('Найдена, 12 элементов')).toBeInTheDocument())
    await userEvent.click(button('Далее'))
    expect(await screen.findByRole('heading', { name: 'Права на запись' })).toBeInTheDocument()
    expect(health).toBe(4)
    // the page was read again only after the new process answered
    const put = calls.findIndex((c) => c.method === 'PUT')
    const reread = calls.findIndex((c, i) => i > put && c.path === '/api/settings/storage' && c.method === 'GET')
    const lastHealth = calls.map((c) => c.path).lastIndexOf('/api/health')
    expect(reread).toBeGreaterThan(lastHealth)
  })

  it('a server that only ever answers as the old process is said to be slow', async () => {
    restartTiming.max = 3
    serve({
      put: (b) => ({ ...storageView({}, (b as { folders: Record<FolderKind, string> }).folders), restarting: true, bootId: 'old-run' }) as never,
      check: (b) => storageView({}, b.folders),
    })
    const f = globalThis.fetch as unknown as ReturnType<typeof vi.fn>
    const inner = f.getMockImplementation() as (i: RequestInfo | URL, n?: RequestInit) => Promise<Response>
    f.mockImplementation(async (input: RequestInfo | URL, init?: RequestInit) =>
      String(input).endsWith('/api/health') ? json({ ok: true, bootId: 'old-run' }) : inner(input, init),
    )
    open()
    const series = (await screen.findByText('Сериалы')).closest('li')!
    await userEvent.click(within(series).getByRole('button', { name: 'Использовать «tv»' }))
    await waitFor(() => expect(within(screen.getByText('Сериалы').closest('li')!).getByText('Найдена, 12 элементов')).toBeInTheDocument())
    await userEvent.click(button('Далее'))
    expect(await screen.findByText(/Сервер отвечает дольше обычного/)).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'Медиа-папки' })).toBeInTheDocument()
  })

  it('on a disk that looks unmounted «Создать» is not offered, and the page says the disk is not connected', async () => {
    serve({ view: storageView({ offline: true, canCreate: false }) })
    open()
    const series = (await screen.findByText('Сериалы')).closest('li')!
    expect(screen.getByText(/Диск не подключён/)).toBeInTheDocument()
    expect(within(series).queryByRole('button', { name: 'Создать' })).toBeNull()
    // picking a folder that is there is not making one
    expect(within(series).getByRole('button', { name: 'Использовать «tv»' })).toBeInTheDocument()
  })

  it('with no media folder «Создать» is not offered either', async () => {
    const v = storageView({ canCreate: false })
    v.root = dir('/media', { status: 'missing', items: 0 })
    serve({ view: v })
    open()
    const series = (await screen.findByText('Сериалы')).closest('li')!
    expect(within(series).queryByRole('button', { name: 'Создать' })).toBeNull()
  })

  it('an empty default folder is «пусто», not a green find, and the folder with the files is offered', async () => {
    const v = storageView()
    v.folders[0] = { ...v.folders[0]!, path: 'movies', status: 'ok', items: 0, suggest: 'Фильмы' }
    v.entries = [...v.entries, { name: 'Фильмы', items: 45 }]
    serve({ view: v })
    open()
    const movies = (await screen.findByRole('button', { name: 'Использовать «Фильмы»' })).closest('li')!
    expect(within(movies).getByText('Найдена, пусто. Есть папка с файлами: Фильмы')).toBeInTheDocument()
    expect(within(movies).getByRole('button', { name: 'Использовать «Фильмы»' })).toBeInTheDocument()
    // a folder that has files is just found
    expect(within(screen.getByText('Музыка').closest('li')!).getByText('Найдена, 3 элемента')).toBeInTheDocument()
  })

  it('folders whose place is missing are told to be mounted, with no chown', async () => {
    const v = storageView()
    v.transcode = dir('/mnt/scratch/hls', { status: 'readonly', why: 'denied', error: 'mkdir /mnt/scratch: permission denied', items: 0 })
    v.fix = { paths: [], mount: ['/mnt/scratch/hls'], docker: true, uid: 1000, gid: 1000 }
    serve({ view: v })
    open()
    await screen.findByRole('heading', { name: 'Медиа-папки' })
    await userEvent.click(button('Далее'))
    expect(await screen.findByText(/Сервер не видит эти папки, и дело не в правах/)).toBeInTheDocument()
    expect(screen.getByText('/mnt/scratch/hls', { selector: 'p' })).toBeInTheDocument()
    expect(screen.queryByText(/chown/)).toBeNull()
  })

  it('with no media folder step 2 says the volume is missing instead of a fix', async () => {
    const v = storageView({ canCreate: false })
    v.root = dir('/media', { status: 'missing', items: 0 })
    v.transcode = dir('/media/tmp/antresol-hls', { status: 'missing', items: 0 })
    serve({ view: v })
    open()
    await screen.findByRole('heading', { name: 'Медиа-папки' })
    await userEvent.click(button('Далее'))
    expect(await screen.findByRole('heading', { name: 'Права на запись' })).toBeInTheDocument()
    expect(screen.getByText(/Сервер не видит папку \/media/)).toBeInTheDocument()
    expect(screen.queryByText(/chown/)).toBeNull()
    expect(screen.queryByText('Всё в порядке: сервер может писать в свои папки.')).toBeNull()
  })

  it('«Готово» refused because another tab ended the wizard leaves the page, with no error', async () => {
    serve({ finish: 403 })
    open()
    await screen.findByRole('heading', { name: 'Медиа-папки' })
    await userEvent.click(button('Пропустить настройку'))
    expect(await screen.findByRole('heading', { name: 'Главная' })).toBeInTheDocument()
  })
})
