// «Импорт» where the server downloads nothing (the core build, a
// library-only server): files only, no downloads' words, no retry.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import type { ImportGroup, ImportJob } from '../../../api/types'
import { Import } from '../Import'
import { ImportPreview } from '../import/ImportPreview'
import { ImportProgress } from '../import/ImportProgress'
import { ReviewList } from '../import/ReviewList'
import { pendingLabel } from '../../music/TrackList'

/** What useCanDownload answers in the tests that set it (the page tests use the real one). */
const downloads = vi.hoisted(() => ({ value: null as boolean | null }))
vi.mock('../../../state/canDownload', async (orig) => {
  const real = await orig<typeof import('../../../state/canDownload')>()
  return { useCanDownload: () => (downloads.value ?? real.useCanDownload()) }
})

const json = (b: unknown, status = 200) => new Response(JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } })

const progress = { total: 2, matched: 0, review: 0, notFound: 0, have: 0, queued: 0, downloading: 0, done: 0, failed: 0, skipped: 0 }

/** A job as a server with no disk numbers sends it (a library-only server shows nobody its disk). */
const job = (o: Partial<ImportJob> = {}): ImportJob => ({
  id: 3,
  service: 'file',
  status: 'preview',
  createdAt: '2026-11-04T10:00:00Z',
  counts: { songs: 2, liked: 0, albums: 1, playlists: 1, savedAlbums: 0 },
  estimatedBytes: 0,
  disk: { free: 0, total: 0 },
  favorites: true,
  playlists: [{ id: 1, name: 'Дорога', songs: 2, readable: true, selected: true }],
  progress,
  error: '',
  report: { savedAlbumsMissing: 0, unresolved: 0 },
  ...o,
})

let calls: string[] = []
/** The next PATCH of the job fails with no answer from the server. */
let patchFails = false

beforeEach(() => {
  calls = []
  patchFails = false
  downloads.value = null
  vi.stubGlobal('EventSource', class { addEventListener() {} close() {} })
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      calls.push(`${init?.method ?? 'GET'} ${url}`)
      if (url === '/api/import/services') return json({ services: [] })
      if (url === '/api/import/jobs') return json({ jobs: [] })
      if (url === '/api/import/files') return json(job(), 201)
      if (url === '/api/import/jobs/3' && init?.method === 'PATCH' && patchFails) throw new TypeError('network')
      if (url === '/api/import/jobs/3/review')
        return json({ tracks: [{ id: 1, artist: 'Кино', title: 'Кукушка', album: '', reason: 'Нет точного совпадения', candidates: [] }] })
      return json({ error: 'Не найдено' }, 404)
    }),
  )
})
afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

const renderImport = () =>
  render(
    <MemoryRouter initialEntries={['/manage/import']}>
      <Import />
    </MemoryRouter>,
  )

describe('Import without services', () => {
  it('a server that lists no services offers the file input only', async () => {
    renderImport()
    expect(await screen.findByLabelText('Файл медиатеки')).toBeTruthy()
    expect(screen.getAllByTestId('import-tile')).toHaveLength(1)
    expect(screen.getByRole('button', { name: 'Загрузить файл' })).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'Подключить' })).toBeNull()
  })

  it('an uploaded file shows the preview with no space line', async () => {
    renderImport()
    const input = (await screen.findByLabelText('Файл медиатеки')) as HTMLInputElement
    fireEvent.change(input, { target: { files: [new File(['#EXTM3U\n'], 'a.m3u')] } })
    expect(await screen.findByText('Песен: 2')).toBeTruthy()
    expect(screen.queryByText(/Свободно на сервере/)).toBeNull()
  })

  // runs only where the tree is the public one (no src/app/routesFull.tsx: vite.config.ts
  // makes __LIBRARY_EDITION__ true), i.e. in the exported tree
  it.runIf(__LIBRARY_EDITION__)('core build: files only, no sign-ins, no download words; a .m3u shows the preview', async () => {
    renderImport()
    const input = (await screen.findByLabelText('Файл медиатеки')) as HTMLInputElement
    expect(screen.queryByText(/Spotify|Яндекс|Deezer/)).toBeNull()
    expect(document.body.textContent).not.toMatch(/[Рр]аздач|Spotify/)
    fireEvent.change(input, { target: { files: [new File(['#EXTM3U\n#EXTINF:1,A - B\nb.mp3\n'], 'a.m3u')] } })
    expect(await screen.findByText('Песен: 2')).toBeTruthy()
    expect(screen.getByText(/Нет в медиатеке/)).toBeTruthy()
    expect(screen.queryByText(/Свободно на сервере|Примерно/)).toBeNull()
    expect(document.body.textContent).not.toMatch(/[Рр]аздач|Spotify|скача/i)
    expect(calls).toContain('POST /api/import/files')
  })

  // every text on the way a core build takes is in a catalog it has (a missing key shows as its own name)
  it.runIf(__LIBRARY_EDITION__)('core build: no raw catalog key on the first paint, in the preview or after a failed save', async () => {
    const rawKey = /\b(sources|common|import|music|manage)\.[a-zA-Z]+/
    renderImport()
    expect(document.body.textContent).not.toMatch(rawKey) // loading
    const input = (await screen.findByLabelText('Файл медиатеки')) as HTMLInputElement
    expect(document.body.textContent).not.toMatch(rawKey)
    fireEvent.change(input, { target: { files: [new File(['#EXTM3U\n'], 'a.m3u')] } })
    await screen.findByText('Песен: 2')
    patchFails = true
    fireEvent.click(screen.getByRole('switch'))
    expect(await screen.findByText(/Нет связи с сервером/)).toBeTruthy()
    expect(document.body.textContent).not.toMatch(rawKey)
  })
})

describe('where nothing is downloaded', () => {
  it('the preview says what happens to the songs the library lacks and shows no size or space', () => {
    downloads.value = false
    render(<ImportPreview job={job({ estimatedBytes: 1 << 30, disk: { free: 0, total: 0 } })} onPatch={() => {}} onStart={() => {}} onCancel={() => {}} />)
    expect(screen.getByText(/Остальные будут отмечены как «Нет в медиатеке»/)).toBeTruthy()
    expect(screen.queryByText(/Примерно/)).toBeNull()
    expect(screen.queryByText(/Свободно/)).toBeNull()
  })

  it('an album the library lacks is «Нет в медиатеке» and has no «Повторить»', async () => {
    downloads.value = false
    const g: ImportGroup = { id: 1, artist: 'Кино', album: 'Группа крови', year: 1988, songs: 3, state: 'not_found', bytes: 0, done: 0, error: '', infoHash: '' }
    render(<ImportProgress job={job({ status: 'done', progress: { ...progress, notFound: 3 } })} groups={[g]} onReview={() => {}} onCancel={() => {}} onDone={() => {}} />)
    await waitFor(() => expect(screen.getByTestId('import-group').textContent).toContain('Нет в медиатеке'))
    expect(screen.queryByRole('button', { name: 'Повторить' })).toBeNull()
  })

  it('a space line is not shown without downloads even when the server sends disk numbers', () => {
    downloads.value = false
    render(<ImportPreview job={job({ disk: { free: 1 << 30, total: 10 << 30 } })} onPatch={() => {}} onStart={() => {}} onCancel={() => {}} />)
    expect(screen.queryByText(/Свободно/)).toBeNull()
  })

  it('the review offers the manual search only where there is a catalog', async () => {
    downloads.value = false
    const { unmount } = render(<ReviewList jobId={3} onChanged={() => {}} />)
    expect(await screen.findByRole('button', { name: 'Пропустить' })).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'Найти вручную' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Выбрать альбом…' })).toBeNull()
    unmount()
    downloads.value = true
    render(<ReviewList jobId={3} onChanged={() => {}} />)
    expect(await screen.findByRole('button', { name: 'Найти вручную' })).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Выбрать альбом…' })).toBeTruthy()
  })

  it('a pending playlist row says «Нет в медиатеке», except for a skipped song', () => {
    expect(pendingLabel('matched', undefined, true)).toBe('Нет в медиатеке')
    expect(pendingLabel('not_found', undefined, true)).toBe('Нет в медиатеке')
    expect(pendingLabel('skipped', undefined, true)).toBe('Пропущено')
    expect(pendingLabel('failed', undefined, true)).toBe('Ошибка')
  })
})
