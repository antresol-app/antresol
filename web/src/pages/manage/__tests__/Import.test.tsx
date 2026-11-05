import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import type { ImportGroup, ImportJob, ImportService } from '../../../api/types'
import { Import } from '../Import'
import { unreadableHint } from '../import/ImportPreview'
import { groupStateLabel } from '../import/ImportProgress'
import { setLanguagePref } from '../../../i18n'

const json = (b: unknown, status = 200) =>
  status === 204 ? new Response(null, { status }) : new Response(JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } })

const GB = 1 << 30
const MB = 1 << 20

const services: ImportService[] = [
  { id: 'spotify', configured: false, connect: 'redirect', setup: ['Откройте developer.spotify.com/dashboard и нажмите «Create app».', 'Скопируйте Client ID в переменную VAULT_SPOTIFY_CLIENT_ID на сервере и перезапустите Vault.'], files: ['csv'] },
  { id: 'apple', configured: true, connect: 'file', setup: [], files: ['xml'] },
  { id: 'yandex', configured: false, connect: 'device', setup: ['Шаг Яндекса'], files: ['csv'] },
  { id: 'youtube', configured: false, connect: 'redirect', setup: ['Шаг Google'], files: ['zip', 'csv'] },
  { id: 'deezer', configured: false, connect: 'redirect', setup: ['Шаг Deezer'], files: ['csv'] },
  { id: 'file', configured: true, connect: 'file', setup: [], files: ['csv', 'm3u', 'xml', 'zip'] },
]

const progress = { total: 12, matched: 0, review: 0, notFound: 0, have: 0, queued: 0, downloading: 0, done: 0, failed: 0, skipped: 0 }

function job(o: Partial<ImportJob> = {}): ImportJob {
  return {
    id: 5,
    service: 'file',
    status: 'preview',
    createdAt: '2026-10-27T10:00:00Z',
    counts: { songs: 12, liked: 8, albums: 4, playlists: 2, savedAlbums: 0 },
    estimatedBytes: 12 * 30 * MB,
    disk: { free: 120 * GB, total: 500 * GB },
    favorites: true,
    playlists: [
      { id: 7, name: 'Дорога', songs: 5, readable: true, selected: true },
      { id: 8, name: 'Чужой плейлист', songs: 0, readable: false, selected: false },
    ],
    progress,
    error: '',
    report: { savedAlbumsMissing: 0, unresolved: 0 },
    ...o,
  }
}

/** A stand-in for the one shared EventSource (jsdom has none). */
class FakeSource {
  static last: FakeSource | null = null
  listeners = new Map<string, ((m: MessageEvent) => void)[]>()
  onmessage: ((m: MessageEvent) => void) | null = null
  constructor() {
    FakeSource.last = this
  }
  addEventListener(type: string, fn: (m: MessageEvent) => void) {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), fn])
  }
  close() {}
  emit(type: string, data: unknown) {
    for (const fn of this.listeners.get(type) ?? []) fn(new MessageEvent(type, { data: JSON.stringify(data) }))
  }
}

type Call = { url: string; method: string; body: unknown; headers?: HeadersInit }
let calls: Call[] = []
let jobs: ImportJob[] = []
let groups: ImportGroup[] = []
let current: ImportJob = job()
let uploadStatus = 201
/** The upload answers a reading job (a Takeout whose videos are being named). */
let uploadReads = false
/** The next job GET fails (no answer from the server). */
let jobFails = 0
let serviceList: ImportService[] = services
/** What «Подключить» answers. */
let connectMode: 'redirect' | 'paste' = 'paste'
/** What the Yandex device polls answer, in turn (then the job). */
let devicePolls: ('pending' | 'expired')[] = []

beforeEach(() => {
  calls = []
  jobs = []
  groups = []
  current = job()
  uploadStatus = 201
  uploadReads = false
  jobFails = 0
  serviceList = services
  connectMode = 'paste'
  devicePolls = []
  vi.stubGlobal('EventSource', FakeSource)
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      const method = init?.method ?? 'GET'
      const body = init?.body instanceof FormData ? init.body : init?.body ? JSON.parse(String(init.body)) : undefined
      calls.push({ url, method, body, headers: init?.headers })
      if (url === '/api/import/services') return json({ services: serviceList })
      if (url === '/api/import/connect/spotify' && method === 'POST') {
        const redirectUri = connectMode === 'paste' ? 'http://127.0.0.1:8765/vault-import/spotify' : 'https://vault.example.org/api/import/oauth/spotify/callback'
        return json({ authorizeUrl: 'https://accounts.spotify.com/authorize?state=s1', mode: connectMode, redirectUri })
      }
      if (url === '/api/import/connect/youtube' && method === 'POST') {
        return json({ authorizeUrl: 'https://accounts.google.com/o/oauth2/v2/auth?state=g1', mode: 'paste', redirectUri: 'http://127.0.0.1:8765/vault-import/youtube' })
      }
      if (url === '/api/import/oauth/youtube/paste' && method === 'POST') {
        current = job({ id: 13, service: 'youtube', status: 'reading' })
        jobs = [current]
        return json(current, 201)
      }
      if (url === '/api/import/connect/yandex' && method === 'POST') {
        return json({ pollId: 'p1', userCode: 'ABCD1234', verificationUrl: 'https://ya.ru/device', expiresIn: 300, interval: 5 })
      }
      if (url === '/api/import/connect/yandex/poll' && method === 'POST') {
        const next = devicePolls.shift()
        if (next === 'pending') return json({ pending: true }, 202)
        if (next === 'expired') return json({ error: 'Ссылка устарела. Подключитесь заново.' }, 400)
        current = job({ id: 10, service: 'yandex', status: 'reading' })
        jobs = [current]
        return json(current, 201)
      }
      if (url === '/api/import/deezer/public' && method === 'POST') {
        const u = (body as { url: string }).url
        if (u.includes('404404')) return json({ error: 'Не найдено' }, 404)
        current = job({ id: 15, service: 'deezer', status: 'reading' })
        jobs = [current]
        return json(current, 201)
      }
      if (url === '/api/import/connect/yandex/token' && method === 'POST') {
        current = job({ id: 11, service: 'yandex', status: 'reading' })
        jobs = [current]
        return json(current, 201)
      }
      if (url === '/api/import/oauth/spotify/paste' && method === 'POST') {
        const u = (body as { url: string }).url
        if (u.includes('error=access_denied')) return json({ error: 'Не удалось подключиться к сервису. Попробуйте ещё раз.' }, 400)
        current = job({ id: 9, service: 'spotify', status: 'reading' })
        jobs = [current]
        return json(current, 201)
      }
      if (url === '/api/import/jobs' && method === 'GET') return json({ jobs })
      if (url === '/api/import/files') {
        const st = uploadStatus
        uploadStatus = 201 // a second try goes through
        if (st === 201 && uploadReads) {
          current = job({ id: 14, service: 'youtube', status: 'reading' })
          jobs = [current]
          return json(current, 201)
        }
        return st === 201 ? json({ ...current, id: 6, status: 'preview' }, 201) : json({ error: 'Импорт уже идёт' }, st)
      }
      if (url === `/api/import/jobs/${current.id}` && method === 'PATCH') {
        const b = body as { favorites?: boolean; playlists?: Record<string, boolean> }
        current = {
          ...current,
          favorites: b.favorites ?? current.favorites,
          playlists: current.playlists.map((p) => (b.playlists && String(p.id) in b.playlists ? { ...p, selected: b.playlists[String(p.id)]! } : p)),
        }
        return json(current)
      }
      if (url === `/api/import/jobs/${current.id}` && method === 'GET') {
        if (jobFails > 0) {
          jobFails--
          return json({ error: 'Что-то пошло не так' }, 500)
        }
        return json(current)
      }
      if (url === `/api/import/jobs/${current.id}/start`) {
        current = { ...current, status: 'matching' }
        return json(undefined, 204)
      }
      if (url === `/api/import/jobs/${current.id}/groups`) return json({ groups })
      if (url === `/api/import/jobs/${current.id}/review`) return json({ tracks: [] })
      if (/\/api\/import\/groups\/\d+\/retry$/.test(url)) return json(undefined, 204)
      if (/\/cancel$/.test(url)) {
        jobs = jobs.map((j) => ({ ...j, status: 'cancelled' as const }))
        return json(undefined, 204)
      }
      return json({ error: 'Не найдено' }, 404)
    }),
  )
})
afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
  vi.useRealTimers()
})

function renderImport(at = '/manage/import') {
  return render(
    <MemoryRouter initialEntries={[at]}>
      <Import />
    </MemoryRouter>,
  )
}

describe('Import', () => {
  it('«Отменить текущий импорт и загрузить новый» cancels the open job, then uploads again', async () => {
    uploadStatus = 409
    renderImport()
    const input = (await screen.findByLabelText('Файл медиатеки')) as HTMLInputElement
    jobs = [job({ status: 'downloading' })]
    current = jobs[0]!
    fireEvent.change(input, { target: { files: [new File(['x'], 'a.csv')] } })
    const sheet = await screen.findByRole('alertdialog', { name: 'Импорт уже идёт' })
    fireEvent.click(within(sheet).getByRole('button', { name: 'Отменить текущий импорт и загрузить новый' }))
    await waitFor(() => expect(calls.filter((c) => c.url === '/api/import/files')).toHaveLength(2))
    expect(calls.some((c) => c.url === '/api/import/jobs/5/cancel' && c.method === 'POST')).toBe(true)
    expect(await screen.findByText('Песен: 12')).toBeTruthy()
  })

  it('previewEdits: playlist and «Избранное» changes are saved; «Начать импорт» starts', async () => {
    current = { ...current, service: 'spotify' }
    jobs = [current]
    renderImport()
    const road = await screen.findByRole('checkbox', { name: /Дорога/ })
    expect(screen.getByRole('checkbox', { name: /Чужой плейлист/ })).toHaveProperty('disabled', true)
    expect(screen.getByText('Spotify не отдаёт песни чужих плейлистов. Сохраните копию плейлиста в Spotify или загрузите CSV.')).toBeTruthy()
    fireEvent.click(road)
    await waitFor(() => expect(calls.some((c) => c.method === 'PATCH')).toBe(true))
    expect(calls.find((c) => c.method === 'PATCH')!.body).toEqual({ playlists: { '7': false } })
    await waitFor(() => expect(road).toHaveProperty('checked', false))

    fireEvent.click(screen.getByRole('switch', { name: 'Добавить понравившиеся песни в «Избранное»' }))
    await waitFor(() => expect(calls.filter((c) => c.method === 'PATCH')).toHaveLength(2))
    expect(calls.filter((c) => c.method === 'PATCH')[1]!.body).toEqual({ favorites: false })

    fireEvent.click(screen.getByRole('button', { name: 'Начать импорт' }))
    await waitFor(() => expect(calls.some((c) => c.url === '/api/import/jobs/5/start' && c.method === 'POST')).toBe(true))
    expect(await screen.findByText(/Готово песен:/)).toBeTruthy()
  })

  it('shows a MusicBrainz pause and the songs waiting for a decision', async () => {
    current = job({ status: 'matching', progress: { ...progress, review: 2 }, matchPausedUntil: new Date(Date.now() + 5 * 60_000).toISOString() })
    jobs = [current]
    renderImport()
    expect(await screen.findByText(/MusicBrainz не отвечает/)).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Нужно ваше решение: 2' })).toBeTruthy()
  })

  it('an uploaded Takeout is read first while its videos are named', async () => {
    uploadReads = true
    renderImport()
    await screen.findAllByTestId('import-tile')
    const input = screen.getByLabelText('Файл медиатеки') as HTMLInputElement
    fireEvent.change(input, { target: { files: [new File(['PK'], 'takeout-001.zip', { type: 'application/zip' })] } })
    expect(await screen.findByText('Читаем медиатеку…')).toBeTruthy()
    // a long naming can be given up: the job is cancelled (the server stops it)
    fireEvent.click(screen.getByRole('button', { name: 'Отменить импорт' }))
    await waitFor(() => expect(calls.some((c) => c.url === '/api/import/jobs/14/cancel' && c.method === 'POST')).toBe(true))
  })

  it('back from the sign-in: ?job= shows that job, ?error= says what went wrong', async () => {
    current = job({ id: 9, service: 'spotify', status: 'reading' })
    // a newer open job of another service would be shown otherwise
    jobs = [current, job({ id: 12, status: 'preview' })]
    renderImport('/manage/import?job=9')
    expect(await screen.findByText('Читаем медиатеку…')).toBeTruthy()
    expect(screen.queryByTestId('import-preview')).toBeNull()
  })

  it('?error= shows only the server\'s own texts', async () => {
    renderImport('/manage/import?error=' + encodeURIComponent('Ваш аккаунт заблокирован, позвоните по номеру…'))
    const alert = await screen.findByRole('alert')
    expect(alert.textContent).toContain('Не удалось подключиться к сервису. Попробуйте ещё раз.')
    expect(alert.textContent).not.toContain('заблокирован')
  })
})

describe('unreadableHint', () => {
  it('names Spotify only for Spotify; other services get a neutral line and their own file route', () => {
    expect(unreadableHint('spotify')).toBe('Spotify не отдаёт песни чужих плейлистов. Сохраните копию плейлиста в Spotify или загрузите CSV.')
    expect(unreadableHint('youtube')).toBe('Сервис не отдал песни этого плейлиста. Загрузите архив Google Takeout.')
    expect(unreadableHint('yandex')).toBe('Сервис не отдал песни этого плейлиста. Загрузите CSV.')
    expect(unreadableHint('deezer')).toBe('Сервис не отдал песни этого плейлиста. Загрузите CSV.')
    for (const s of ['apple', 'file', 'other']) expect(unreadableHint(s)).toBe('Сервис не отдал песни этого плейлиста.')
  })

  it('speaks English when the interface is English', () => {
    setLanguagePref('en')
    expect(unreadableHint('youtube')).toBe('The service didn’t share this playlist’s songs. Upload a Google Takeout archive.')
    expect(groupStateLabel({ state: 'waiting_download', waitingFor: 'Kino' })).toBe('Waiting for “Kino”')
    expect(groupStateLabel({ state: 'waiting' })).toBe('Queued')
  })
})
