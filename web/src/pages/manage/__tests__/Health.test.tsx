// «Состояние» (was «Обновления»): an addon whose update check failed or is
// still running is never shown as «Последняя версия»; «Проверить снова»
// asks the server for a fresh check. The other parts: the GPU, the network,
// the folders and the library, each from its own endpoint.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { Health } from '../Health'

vi.mock('../../../state/session', async (orig) => ({ ...(await orig<object>()), useSession: () => ({ user: { addons: true, isAdmin: true } }) }))

const json = (b: unknown) => new Response(JSON.stringify(b), { status: 200, headers: { 'Content-Type': 'application/json' } })
const base = { description: '', source: 'github.com/a/x', enabled: true, status: 'running', pinned: false, pinFailed: false, helpers: [] }
const core = { version: '0.8.11', commit: '', latest: 'v0.8.11', releaseUrl: '', checked: true, newer: false, docker: true }

const transcoding = {
  settings: { mode: 'auto', device: '', tonemap: true, hevc: false },
  defaults: { mode: 'auto', device: '', tonemap: true, hevc: false },
  custom: false,
  devices: ['/dev/dri/renderD128'],
  status: { mode: 'vaapi', name: 'VA-API', requested: 'auto', hevc: false, tonemapGpu: true, tonemapCpu: false, probes: [{ mode: 'vaapi', ok: true }], probedAt: '2026-11-19T10:00:00Z' },
  fingerprint: true,
}
const helpers = [
  { addon: 'online', name: 'helper-a', address: 'http://helper-a:9118', ok: false, error: 'no such host' },
  { addon: 'online', name: 'cache', address: 'http://cache:80', ok: true },
]
const network = [
  { id: 'github', name: 'GitHub', ok: true, ms: 120 },
  { id: 'tmdb', name: 'TMDB', ok: false, ms: 6000, error: 'context deadline exceeded' },
]
const library = { running: false, queued: false, finishedAt: new Date().toISOString(), movies: 12, episodes: 340, albums: 0, tracks: 0, books: 0, audiobooks: 0, comics: 0, files: 0, skipped: 0, removed: 0 }
const dir = (path: string, status = 'ok') => ({ path, status, items: 3 })
const storage = {
  root: dir('/media'),
  folders: [{ ...dir('movies'), kind: 'movies', abs: '/media/movies', default: 'movies', primary: true, adult: false, unlisted: false, indexed: 3 }],
  transcode: dir('/transcode', 'readonly'),
  entries: [],
  custom: false,
  restarts: true,
  canCreate: true,
}

function serve(list: () => unknown[]) {
  const urls: string[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const u = String(input)
      urls.push(u)
      if (u === '/api/updates') return json(core)
      if (u === '/api/addons/helpers') return json(helpers)
      if (u.startsWith('/api/addons')) return json(list())
      if (u === '/api/settings/transcoding') return json(transcoding)
      if (u === '/api/admin/health/network') return json(network)
      if (u === '/api/library/status') return json(library)
      if (u === '/api/settings/storage') return json(storage)
      return json({})
    }),
  )
  return urls
}

describe('«Обновления»: addons', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('says why a check failed, then checks again fresh', async () => {
    let failed = true
    const urls = serve(() => [
      failed
        ? { ...base, id: 'online', name: 'Онлайн', version: '0.1.0', update: null, updateState: 'failed', updateError: 'GitHub не ответил вовремя' }
        : { ...base, id: 'online', name: 'Онлайн', version: '0.1.0', update: '0.1.2', updateState: '' },
      { ...base, id: 'fetcher', name: 'Загрузчик', version: '0.4.2', update: null, updateState: 'needsCore', updateError: 'Версии 0.5.0 нужен более новый сервер: обновите сервер' },
    ])
    render(<MemoryRouter><Health /></MemoryRouter>)
    expect(await screen.findByText('Не удалось проверить: GitHub не ответил вовремя')).toBeInTheDocument()
    expect(screen.getByText('Версии 0.5.0 нужен более новый сервер: обновите сервер')).toBeInTheDocument()
    // only the server's own row is «Последняя версия»
    expect(screen.getAllByText('Последняя версия')).toHaveLength(1)
    failed = false
    await userEvent.click(screen.getByRole('button', { name: 'Проверить «Онлайн» снова' }))
    expect(await screen.findByText('Версия 0.1.0 → 0.1.2')).toBeInTheDocument()
    expect(urls).toContain('/api/addons?fresh=1')
    expect(screen.getByRole('button', { name: 'Обновить «Онлайн»' })).toBeInTheDocument()
  })

  it('shows a running check and asks again until it answers', async () => {
    let n = 0
    serve(() => [
      n++ === 0
        ? { ...base, id: 'online', name: 'Онлайн', version: '0.1.0', update: null, updateState: 'checking' }
        : { ...base, id: 'online', name: 'Онлайн', version: '0.1.0', update: '0.1.2', updateState: '' },
    ])
    render(<MemoryRouter><Health /></MemoryRouter>)
    expect(await screen.findByText('Проверяется…')).toBeInTheDocument()
    await waitFor(() => expect(screen.getByText('Версия 0.1.0 → 0.1.2')).toBeInTheDocument(), { timeout: 5000 })
  }, 10_000)

  it('says whether the GPU, the network, the folders and the library work', async () => {
    serve(() => [])
    render(<MemoryRouter><Health /></MemoryRouter>)
    expect(await screen.findByText(/^Видеокарта VA-API: аппаратное декодирование/)).toBeInTheDocument()
    expect(screen.getByText('Доступен · 120 мс')).toBeInTheDocument()
    expect(screen.getByText('Недоступен: context deadline exceeded')).toBeInTheDocument()
    expect(await screen.findByText(/^Последнее .*: 12 фильмов, 340 серий$/)).toBeInTheDocument()
    // the folders: the media folder writes, the transcoding folder does not
    expect(await screen.findByText('/transcode')).toBeInTheDocument()
    expect(screen.getByText('Нет прав на запись')).toBeInTheDocument()
  })

  it('shows each addon running or not, its helper services, and a web part that did not load', async () => {
    const loader = await import('../../../addons/loader')
    vi.spyOn(loader, 'addonWebFailures').mockReturnValue(new Map([['online', 'timeout']]))
    serve(() => [
      { ...base, id: 'online', name: 'Онлайн', version: '0.1.2', update: null, updateState: '' },
      { ...base, id: 'trailers', name: 'Трейлеры', version: '0.1.0', status: 'crashed', update: null, updateState: '' },
    ])
    render(<MemoryRouter><Health /></MemoryRouter>)
    expect(await screen.findByText('Остановилось с ошибкой')).toBeInTheDocument()
    expect(screen.getAllByText('Работает').length).toBeGreaterThan(0)
    expect(await screen.findByText('helper-a не отвечает: no such host')).toBeInTheDocument()
    expect(screen.getByText('cache · Онлайн')).toBeInTheDocument()
    expect(screen.getByText('Отвечает')).toBeInTheDocument()
    expect(await screen.findByText('Веб-часть не загрузилась: timeout')).toBeInTheDocument()
  })
})
