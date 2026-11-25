// «Состояние»: one list of checks, a ring and a short value each; a row with
// something to do opens the sheet «Как починить» with its one action or
// command. «Проверить снова» asks everything again (fresh); the key of TMDB
// and the mailbox are tested only by the button in their sheet.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router'
import type { ServerCheck } from '../../../api/selfcheck'
import { formatSize } from '../../../state/format'
import { Health } from '../Health'
import { addon, core, healthyChecks, library, network, readonlyStorage, storage, transcoding } from './healthFixtures'

vi.mock('../../../state/session', async (orig) => ({ ...(await orig<object>()), useSession: () => ({ user: { addons: true, isAdmin: true } }) }))
// the update sheet is the addons page's own and has its own tests
vi.mock('../Addons', () => ({
  AddSheet: ({ updating }: { updating?: { name: string } }) => <div role="dialog" aria-label="update sheet">{`update ${updating?.name}`}</div>,
  reloadSlots: vi.fn(),
}))

const json = (b: unknown, status = 200) => new Response(JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } })

interface World {
  checks: ServerCheck[]
  addons: ReturnType<typeof addon>[]
  helpers: unknown[]
  net: typeof network
  storage: typeof storage
  hw: typeof transcoding
  selfcheck: 'ok' | 'fail'
  testAnswer: ServerCheck | null
}

function serve(over: Partial<World> = {}) {
  const w: World = { checks: healthyChecks, addons: [], helpers: [], net: network, storage, hw: transcoding, selfcheck: 'ok', testAnswer: null, ...over }
  const urls: string[] = []
  const posts: string[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const u = String(input)
      if (init?.method === 'POST') {
        posts.push(u)
        return u.startsWith('/api/admin/selfcheck/') && w.testAnswer ? json(w.testAnswer) : json({})
      }
      urls.push(u)
      if (u.startsWith('/api/admin/selfcheck')) return w.selfcheck === 'fail' ? json({ error: 'Что-то пошло не так' }, 500) : json({ checkedAt: '2026-11-25T14:02:00Z', checks: w.checks })
      if (u === '/api/updates') return json(core)
      if (u === '/api/addons/helpers') return json(w.helpers)
      if (u.startsWith('/api/addons')) return json(w.addons)
      if (u === '/api/settings/transcoding') return json(w.hw)
      if (u === '/api/admin/health/network') return json(w.net)
      if (u === '/api/library/status') return json(library)
      if (u === '/api/settings/storage') return json(w.storage)
      if (u === '/api/settings/external') return json({ services: [], chosen: true, notice: false })
      return json({})
    }),
  )
  return { urls, posts, world: w }
}

const page = () =>
  render(
    <MemoryRouter initialEntries={['/manage/health']}>
      <Routes>
        <Route path="/manage/health" element={<Health />} />
        <Route path="/manage/settings/address" element={<p>address settings</p>} />
        <Route path="/manage/addons" element={<p>addons page</p>} />
      </Routes>
    </MemoryRouter>,
  )

/** The row with this name (a plain row or a chevron one). */
const rowOf = (name: string) => screen.getByText(name, { selector: 'div, span' }).closest('li') as HTMLElement

/** The row once it has its answer (`value`), not while it is still «Проверяем…». */
const answered = (name: string, value: string | RegExp) =>
  waitFor(() => {
    const r = rowOf(name)
    within(r).getByText(value)
    return r
  })

afterEach(() => vi.unstubAllGlobals())

describe('«Состояние»', () => {
  it('a healthy server: the groups and their rows with a short value each, nothing to read', async () => {
    serve()
    page()
    expect(await screen.findByText('Всё работает')).toBeInTheDocument()
    for (const group of ['Доступ', 'Диск и папки', 'Видео', 'Каталог и интернет', 'ТВ и почта', 'Версии и обновления']) {
      expect(screen.getByRole('heading', { name: group })).toBeInTheDocument()
    }
    expect(within(rowOf('Соединение')).getByText('Из дома')).toBeInTheDocument()
    expect(within(rowOf('Свободное место')).getByText(`${formatSize(410 * 2 ** 30)} свободно`)).toBeInTheDocument()
    expect(within(rowOf('Видеокарта')).getByText('VA-API')).toBeInTheDocument()
    expect(within(rowOf('Почта')).getByText('Не настроена')).toBeInTheDocument()
    expect(within(rowOf('Antresol')).getByText('0.8.28')).toBeInTheDocument()
    // a row that is fine opens nothing: no chevron, no button
    expect(within(rowOf('Соединение')).queryByRole('button')).toBeNull()
    expect(screen.getByText('Проверено в 14:02')).toBeInTheDocument()
    // each state is named for a screen reader
    expect(within(rowOf('Соединение')).getByRole('img', { name: 'В порядке' })).toBeInTheDocument()
    expect(within(rowOf('Почта')).getByRole('img', { name: 'Не настроено' })).toBeInTheDocument()
  })

  it('what is broken is counted in the title and opens a sheet with the fix and the command to copy', async () => {
    serve({
      checks: healthyChecks.map((c) =>
        c.id === 'connection' ? { id: 'connection', state: 'warn', code: 'proxy', params: { peer: '172.18.0.1' } } : c.id === 'tmdb' ? { id: 'tmdb', state: 'bad', code: 'rejected' } : c,
      ),
    })
    page()
    expect(await screen.findByText('Сломано: 1 · Поправить: 1')).toBeInTheDocument()
    const row = rowOf('Соединение')
    expect(within(row).getByText('Чужой прокси')).toBeInTheDocument()
    expect(within(row).getByRole('img', { name: 'Стоит поправить' })).toBeInTheDocument()
    await userEvent.click(within(row).getByRole('button'))
    const sheet = await screen.findByRole('dialog')
    expect(within(sheet).getByRole('heading', { name: 'Соединение' })).toBeInTheDocument()
    expect(within(sheet).getByText(/Запрос пришёл через 172\.18\.0\.1/)).toBeInTheDocument()
    expect(within(sheet).getByText('Как починить')).toBeInTheDocument()
    expect(within(sheet).getByText(/Добавьте адрес прокси в файл \.env/)).toBeInTheDocument()
    expect(within(sheet).getByText('VAULT_TRUSTED_PROXIES=172.18.0.1')).toBeInTheDocument()
    expect(within(sheet).getByRole('button', { name: 'Скопировать' })).toBeInTheDocument()
    await userEvent.click(within(sheet).getByRole('button', { name: 'Готово' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  })

  it('an action that leads to a page of the settings goes there', async () => {
    serve({ checks: healthyChecks.map((c) => (c.id === 'address' ? { id: 'address', state: 'off', code: 'unset' } : c)) })
    page()
    await userEvent.click(within(await answered('Внешний адрес', 'Не задан')).getByRole('button'))
    const sheet = await screen.findByRole('dialog')
    expect(within(sheet).getByText('Без внешнего адреса приложения работают только дома.')).toBeInTheDocument()
    await userEvent.click(within(sheet).getByRole('button', { name: 'Задать адрес' }))
    expect(await screen.findByText('address settings')).toBeInTheDocument()
  })

  it('the folders the server may not write to: the chown line from the server\'s own fix', async () => {
    serve({ storage: readonlyStorage })
    page()
    await userEvent.click(within(await answered('Папки медиатеки', 'Нет записи')).getByRole('button'))
    const sheet = await screen.findByRole('dialog')
    expect(within(sheet).getByText(/Сервер не может писать в: \/media\/movies/)).toBeInTheDocument()
    expect(within(sheet).getByText('sudo chown -R 1000:1000 /srv/media/movies')).toBeInTheDocument()
  })

  it('TMDB: the key is tested only by its button, the answer replaces the row, nothing secret is on the page', async () => {
    const s = serve({ testAnswer: { id: 'tmdb', state: 'bad', code: 'rejected', at: '2026-11-25T14:05:00Z' } })
    page()
    const row = await answered('TMDB', 'Ключ задан')
    expect(s.posts).toEqual([])
    await userEvent.click(within(row).getByRole('button'))
    const sheet = await screen.findByRole('dialog')
    expect(within(sheet).getByText(/Ключ задан\. Принимает ли его TMDB/)).toBeInTheDocument()
    await userEvent.click(within(sheet).getByRole('button', { name: 'Проверить ключ' }))
    await waitFor(() => expect(s.posts).toEqual(['/api/admin/selfcheck/tmdb']))
    expect(await within(await screen.findByRole('dialog')).findByText(/TMDB не принял ключ \(ответ 401\)/)).toBeInTheDocument()
    expect(within(rowOf('TMDB')).getByText('Ключ не принят')).toBeInTheDocument()
    expect(screen.getByText('Сломано: 1')).toBeInTheDocument()
    // asking again is the same button, now «Проверить снова»
    expect(within(screen.getByRole('dialog')).getByRole('button', { name: 'Проверить снова' })).toBeInTheDocument()
  })

  it('the mailbox: set up is «Настроена», tested by its button', async () => {
    const s = serve({
      checks: healthyChecks.map((c) => (c.id === 'mail' ? { id: 'mail', state: 'ok', code: 'set', params: { host: 'smtp.example.com' } } : c)),
      testAnswer: { id: 'mail', state: 'ok', code: 'works', params: { host: 'smtp.example.com' }, at: '2026-11-25T14:05:00Z' },
    })
    page()
    const row = await answered('Почта', 'Настроена')
    await userEvent.click(within(row).getByRole('button'))
    await userEvent.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Проверить вход' }))
    await waitFor(() => expect(s.posts).toEqual(['/api/admin/selfcheck/mail']))
    expect(await within(await screen.findByRole('dialog')).findByText('Почтовый сервер smtp.example.com принял вход.')).toBeInTheDocument()
    expect(within(rowOf('Почта')).getByText('Работает')).toBeInTheDocument()
  })

  it('«Проверить снова» asks everything again, fresh, with what the page knows of itself', async () => {
    const s = serve()
    page()
    await screen.findByText('Всё работает')
    const first = s.urls.filter((u) => u.startsWith('/api/admin/selfcheck'))
    expect(first).toHaveLength(1)
    // its clock and zone are sent, https is not (a plain test page)
    expect(first[0]).toMatch(/^\/api\/admin\/selfcheck\?now=\d+&tz=-?\d+$/)
    await userEvent.click(screen.getByRole('button', { name: 'Проверить снова' }))
    await waitFor(() => expect(s.urls.filter((u) => u.startsWith('/api/admin/selfcheck'))).toHaveLength(2))
    expect(s.urls.filter((u) => u.startsWith('/api/admin/selfcheck'))[1]).toMatch(/&fresh=1$/)
    expect(s.urls).toContain('/api/addons?fresh=1')
  })

  it('the server\'s own checks failing is one row, the rest of the page still answers', async () => {
    serve({ selfcheck: 'fail' })
    page()
    const row = await answered('Проверки сервера', 'Не удалось проверить')
    expect(await screen.findByText('Запись работает')).toBeInTheDocument()
    await userEvent.click(within(row).getByRole('button'))
    expect(within(await screen.findByRole('dialog')).getByText('Что-то пошло не так')).toBeInTheDocument()
  })

  it('a check still under way shows it, not a green ring', async () => {
    let release: (r: Response) => void = () => {}
    const gate = new Promise<Response>((r) => (release = r))
    serve()
    const inner = globalThis.fetch
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => (String(input).startsWith('/api/admin/selfcheck') ? gate : inner(input, init))))
    page()
    expect(await screen.findAllByText('Проверяем…')).not.toHaveLength(0)
    expect(screen.getAllByRole('img', { name: 'Проверяем…' }).length).toBeGreaterThan(0)
    release(json({ checkedAt: '2026-11-25T14:02:00Z', checks: healthyChecks }))
    expect(await screen.findByText('Из дома')).toBeInTheDocument()
  })
})

describe('«Состояние»: the addons', () => {
  it('a newer release of an addon: its row offers «Обновить», which opens the update sheet', async () => {
    serve({ addons: [addon({ id: 'one', name: 'Онлайн', version: '0.1.0', update: '0.1.2' }), addon({ id: 'two', name: 'Загрузчик', version: '0.4.2' })] })
    page()
    const row = await answered('Онлайн', 'Доступна 0.1.2')
    expect(within(rowOf('Загрузчик')).getByText('0.4.2')).toBeInTheDocument()
    await userEvent.click(within(row).getByRole('button'))
    const sheet = await screen.findByRole('dialog')
    expect(within(sheet).getByText('Версия 0.1.0 → 0.1.2')).toBeInTheDocument()
    await userEvent.click(within(sheet).getByRole('button', { name: 'Обновить' }))
    expect(await screen.findByText('update Онлайн')).toBeInTheDocument()
  })

  it('a stopped addon and one whose helper service is down; a web part that did not load', async () => {
    const loader = await import('../../../addons/loader')
    vi.spyOn(loader, 'addonWebFailures').mockReturnValue(new Map([['one', 'timeout']]))
    serve({
      addons: [addon({ id: 'one', name: 'Онлайн' }), addon({ id: 'two', name: 'Трейлеры', status: 'crashed' })],
      helpers: [
        { addon: 'one', name: 'solver', title: 'Решатель', address: 'http://solver:8191', ok: false, problem: 'refused' },
        { addon: 'one', name: 'cache', address: 'http://cache:80', ok: true },
        { addon: 'two', name: 'warp', address: '', source: 'none', ok: false, problem: 'unset' },
      ],
    })
    page()
    await answered('Трейлеры', 'Остановлено')
    expect(within(rowOf('Онлайн')).getByText('Не загрузилось')).toBeInTheDocument()
    const helper = await answered('Решатель · Онлайн', 'Не отвечает')
    expect(within(rowOf('cache · Онлайн')).getByText('Отвечает')).toBeInTheDocument()
    expect(within(rowOf('Не настроены')).getByText('warp')).toBeInTheDocument()
    await userEvent.click(within(helper).getByRole('button'))
    const sheet = await screen.findByRole('dialog')
    expect(within(sheet).getByText(/по этому адресу ничего не слушает/)).toBeInTheDocument()
    expect(within(sheet).getByText(/mem_limit/)).toBeInTheDocument()
    expect(within(sheet).getByText('docker compose up -d solver')).toBeInTheDocument()
  })

  it('a check the server still runs is asked again quietly until it has an answer', async () => {
    let n = 0
    serve()
    const inner = globalThis.fetch
    vi.stubGlobal(
      'fetch',
      vi.fn((input: RequestInfo | URL, init?: RequestInit) =>
        String(input) === '/api/addons'
          ? Promise.resolve(json([n++ === 0 ? addon({ id: 'one', name: 'Онлайн', updateState: 'checking' }) : addon({ id: 'one', name: 'Онлайн', update: '0.1.2' })]))
          : inner(input, init),
      ),
    )
    page()
    expect(await screen.findByText('Проверяется…')).toBeInTheDocument()
    await waitFor(() => expect(screen.getByText('Доступна 0.1.2')).toBeInTheDocument(), { timeout: 5000 })
  }, 10_000)
})
