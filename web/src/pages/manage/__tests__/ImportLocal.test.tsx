// «Импорт» where the server downloads nothing (the core build, a
// library-only server): files only, no downloads' words, no retry.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import type { ImportGroup, ImportJob } from '../../../api/types'
import { Import } from '../Import'
import { ImportPreview } from '../import/ImportPreview'
import { ImportProgress } from '../import/ImportProgress'
import { ReviewList } from '../import/ReviewList'
import { pendingLabel } from '../../music/TrackList'

/** What useCanAdd answers in the tests that set it: the page must not follow it (the server's answer decides). */
const downloads = vi.hoisted(() => ({ value: null as boolean | null }))
vi.mock('../../../addons/slots', async (orig) => {
  const real = await orig<typeof import('../../../addons/slots')>()
  return { ...real, useCanAdd: () => (downloads.value ?? real.useCanAdd()) }
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
/** What GET /api/import/services says the server does beyond the library. */
let offers: { downloads?: boolean; catalog?: boolean } = {}
let uploaded: ImportJob = job()

beforeEach(() => {
  calls = []
  patchFails = false
  downloads.value = null
  offers = {}
  uploaded = job()
  vi.stubGlobal('EventSource', class { addEventListener() {} close() {} })
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      calls.push(`${init?.method ?? 'GET'} ${url}`)
      if (url === '/api/import/services') return json({ services: [], ...offers })
      if (url === '/api/import/jobs') return json({ jobs: [] })
      if (url === '/api/import/files') return json(uploaded, 201)
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
    expect(screen.getAllByRole('button', { name: 'Загрузить файл' })).toHaveLength(1)
    expect(screen.queryByRole('button', { name: 'Подключить' })).toBeNull()
  })

  it('the file row is a row of the list; the CSV columns are behind «Формат CSV», not in the row', async () => {
    renderImport()
    await screen.findByLabelText('Файл медиатеки')
    const row = screen.getByRole('button', { name: 'Загрузить файл' }).closest('li')!
    expect(row.textContent).toContain('Google Takeout')
    expect(row.textContent).not.toContain('isrc')
    fireEvent.click(screen.getByRole('button', { name: 'Формат CSV' }))
    const sheet = await screen.findByRole('alertdialog')
    expect(sheet.textContent).toContain('artist, title, album, isrc, playlist, position, dateAdded')
    fireEvent.click(within(sheet).getByRole('button', { name: 'Готово' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
  })

  it('an uploaded file shows the preview with no space line', async () => {
    renderImport()
    const input = (await screen.findByLabelText('Файл медиатеки')) as HTMLInputElement
    fireEvent.change(input, { target: { files: [new File(['#EXTM3U\n'], 'a.m3u')] } })
    expect(await screen.findByText('Песен: 2')).toBeTruthy()
    expect(screen.queryByText(/Свободно на сервере/)).toBeNull()
  })

  // runs only where the tree is the public one (no src/api/clientFull.ts: vite.config.ts
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

// The server alone decides whether it downloads (an addon that declares
// import.services with the imports permission) and whether the review can
// search a catalog (one that declares import.search): an addon that only
// adds titles (useCanAdd) promises neither.
describe('the page words its texts by the server\'s answer', () => {
  it('an addon that adds titles but takes no imports: local texts, «Нет в медиатеке», no size, no space, no catalog', async () => {
    downloads.value = true // useCanAdd: an addon adds titles
    offers = { downloads: false, catalog: false }
    uploaded = job({ estimatedBytes: 1 << 30, disk: { free: 2 ** 30, total: 10 * 2 ** 30 } })
    renderImport()
    expect(await screen.findByText(/В них попадут песни, которые уже есть в медиатеке/)).toBeTruthy()
    const input = (await screen.findByLabelText('Файл медиатеки')) as HTMLInputElement
    fireEvent.change(input, { target: { files: [new File(['#EXTM3U\n'], 'a.m3u')] } })
    expect(await screen.findByText('Песен: 2')).toBeTruthy()
    expect(screen.getByText(/Остальные будут отмечены как «Нет в медиатеке»/)).toBeTruthy()
    expect(screen.queryByText(/Примерно|Свободно на сервере/)).toBeNull()
  })

  it('a server that downloads: the size and the space, though no addon adds titles here', async () => {
    downloads.value = false
    offers = { downloads: true, catalog: true }
    uploaded = job({ estimatedBytes: 1 << 30, disk: { free: 4 * 2 ** 30, total: 10 * 2 ** 30 } })
    renderImport()
    const input = (await screen.findByLabelText('Файл медиатеки')) as HTMLInputElement
    expect(screen.queryByText(/В них попадут песни, которые уже есть в медиатеке/)).toBeNull()
    fireEvent.change(input, { target: { files: [new File(['#EXTM3U\n'], 'a.m3u')] } })
    expect(await screen.findByText('Примерно 1,0 ГБ')).toBeTruthy()
    expect(screen.getByText(/Свободно на сервере/)).toBeTruthy()
    expect(screen.queryByText(/Остальные будут отмечены как «Нет в медиатеке»/)).toBeNull()
  })

  it('an answer without the flags (an older server) promises nothing', async () => {
    downloads.value = true
    renderImport()
    expect(await screen.findByText(/В них попадут песни, которые уже есть в медиатеке/)).toBeTruthy()
  })
})

describe('where nothing is downloaded', () => {
  it('the preview says what happens to the songs the library lacks and shows no size or space', () => {
    render(<ImportPreview downloads={false} job={job({ estimatedBytes: 1 << 30, disk: { free: 0, total: 0 } })} onPatch={() => {}} onStart={() => {}} onCancel={() => {}} />)
    expect(screen.getByText(/Остальные будут отмечены как «Нет в медиатеке»/)).toBeTruthy()
    expect(screen.queryByText(/Примерно/)).toBeNull()
    expect(screen.queryByText(/Свободно/)).toBeNull()
  })

  it('an album the library lacks is «Нет в медиатеке» and has no «Повторить»', async () => {
    const g: ImportGroup = { id: 1, artist: 'Кино', album: 'Группа крови', year: 1988, songs: 3, state: 'not_found', bytes: 0, done: 0, error: '', infoHash: '' }
    render(<ImportProgress downloads={false} job={job({ status: 'done', progress: { ...progress, notFound: 3 } })} groups={[g]} onReview={() => {}} onCancel={() => {}} onDone={() => {}} />)
    await waitFor(() => expect(screen.getByTestId('import-group').textContent).toContain('Нет в медиатеке'))
    expect(screen.queryByRole('button', { name: 'Повторить' })).toBeNull()
  })

  it('a space line is not shown without downloads even when the server sends disk numbers', () => {
    render(<ImportPreview downloads={false} job={job({ disk: { free: 1 << 30, total: 10 << 30 } })} onPatch={() => {}} onStart={() => {}} onCancel={() => {}} />)
    expect(screen.queryByText(/Свободно/)).toBeNull()
  })

  it('the review offers the manual search only where there is a catalog', async () => {
    const { unmount } = render(<ReviewList jobId={3} catalog={false} onChanged={() => {}} />)
    expect(await screen.findByRole('button', { name: 'Пропустить' })).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'Найти вручную' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Выбрать альбом…' })).toBeNull()
    unmount()
    render(<ReviewList jobId={3} catalog onChanged={() => {}} />)
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
