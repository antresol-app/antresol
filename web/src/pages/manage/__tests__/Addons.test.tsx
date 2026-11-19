// «Расширения» (admin): the installed addons from the server, «Добавить
// расширение» with its preview and warning, the switch, «Обновить»,
// «Удалить» (with «Удалить и данные») and «Журнал»; every change reloads
// the addons of this tab.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { Addons } from '../Addons'
import * as loader from '../../../addons/loader'

// reloadAddons is called after the render, not with it: reloadSlots() imports
// the loader chunk first, so every `reload` check below waits for the call.
const json = (b: unknown, status = 200) =>
  new Response(b === undefined ? null : JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } })

const hello = { id: 'hello', name: 'Привет', description: 'Пример расширения', version: '1.0.0', source: 'https://github.com/a/hello', enabled: true, status: 'running', update: '1.2.0' }
const notes = { id: 'notes', name: 'Заметки', description: 'Заметки к фильмам', version: '0.3.1', source: 'https://github.com/a/notes', enabled: false, status: 'stopped', update: null }
const preview = {
  id: 'sub', name: 'Субтитры+', description: 'Ищет субтитры', version: '2.0.0', author: 'Иван', homepage: '', permissions: ['library.read', 'events'], hasWeb: true, installed: '',
}

type Call = { url: string; method: string; body?: unknown }
function serve(overrides: (c: Call) => Response | Promise<Response> | undefined = () => undefined) {
  const calls: Call[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const c: Call = { url: String(input), method: init?.method ?? 'GET', body: init?.body ? JSON.parse(String(init.body)) : undefined }
      calls.push(c)
      const o = overrides(c)
      if (o) return o
      if (c.url === '/api/addons' && c.method === 'GET') return json([hello, notes])
      if (c.url === '/api/addons/preview') return json(preview)
      if (c.url === '/api/addons' && c.method === 'POST') return json({ id: 'sub', name: 'Субтитры+', description: 'Ищет субтитры', version: '2.0.0', source: 'x', enabled: true, status: 'running', update: null })
      if (c.url === '/api/addons/notes/enable') return json({ ...notes, enabled: true, status: 'running' })
      if (c.url === '/api/addons/hello/disable') return json({ ...hello, enabled: false, status: 'stopped' })
      if (c.url === '/api/addons/hello/update') return json({ ...hello, version: '1.2.0', update: null })
      if (c.url.startsWith('/api/addons/hello?') && c.method === 'DELETE') return json(undefined, 204)
      if (c.url.startsWith('/api/addons/hello/log')) return json({ lines: ['started', 'listening on socket'] })
      return json({ error: 'нет' }, 404)
    }),
  )
  return calls
}

function open() {
  render(
    <MemoryRouter>
      <Addons />
    </MemoryRouter>,
  )
}

describe('«Расширения»', () => {
  let reload: ReturnType<typeof vi.spyOn>
  beforeEach(() => {
    reload = vi.spyOn(loader, 'reloadAddons').mockResolvedValue(undefined)
  })
  afterEach(() => vi.unstubAllGlobals())

  it('lists the installed addons with their version, status and update', async () => {
    serve()
    open()
    expect(await screen.findByText('Привет')).toBeInTheDocument()
    expect(screen.getByText('Заметки')).toBeInTheDocument()
    expect(screen.getByText(/Версия 1\.0\.0 · Работает/)).toBeInTheDocument()
    expect(screen.getByText(/Версия 0\.3\.1 · Выключено/)).toBeInTheDocument()
    expect(screen.getByText('Доступно обновление 1.2.0')).toBeInTheDocument()
    expect(screen.getByRole('switch', { name: 'Привет' })).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByRole('switch', { name: 'Заметки' })).toHaveAttribute('aria-checked', 'false')
  })

  it('previews an addon with its permissions and the warning, then installs it', async () => {
    const calls = serve()
    open()
    await screen.findByText('Привет')
    await userEvent.click(screen.getByRole('button', { name: 'Добавить расширение' }))
    const sheet = await screen.findByRole('dialog', { name: 'Добавить расширение' })
    await userEvent.type(within(sheet).getByLabelText('Ссылка на GitHub'), 'https://github.com/a/sub')
    await userEvent.click(within(sheet).getByRole('button', { name: 'Проверить' }))
    expect(await within(sheet).findByText('Субтитры+')).toBeInTheDocument()
    expect(within(sheet).getByText(/Иван/)).toBeInTheDocument()
    expect(within(sheet).getByText('Ищет субтитры')).toBeInTheDocument()
    expect(within(sheet).getByText('Читать медиатеку')).toBeInTheDocument()
    expect(within(sheet).getByText('Присылать события в приложение')).toBeInTheDocument()
    expect(
      within(sheet).getByText(
        'Расширение — это программа, которая работает на сервере с теми же правами, что и Antresol. Оно может читать данные сервера, включая базу. Ставьте только те, которым доверяете.',
      ),
    ).toBeInTheDocument()
    expect(calls.find((c) => c.url === '/api/addons/preview')?.body).toEqual({ url: 'https://github.com/a/sub' })
    await userEvent.click(within(sheet).getByRole('button', { name: 'Установить' }))
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Добавить расширение' })).toBeNull())
    expect(calls.find((c) => c.url === '/api/addons' && c.method === 'POST')?.body).toEqual({ url: 'https://github.com/a/sub', version: '2.0.0' })
    expect(await screen.findByText('Субтитры+')).toBeInTheDocument()
    await waitFor(() => expect(reload).toHaveBeenCalled())
  })

  it('a preview that fails says why and installs nothing', async () => {
    const calls = serve((c) => (c.url === '/api/addons/preview' ? json({ error: 'В релизе нет antresol-addon.json' }, 400) : undefined))
    open()
    await screen.findByText('Привет')
    await userEvent.click(screen.getByRole('button', { name: 'Добавить расширение' }))
    const sheet = await screen.findByRole('dialog', { name: 'Добавить расширение' })
    await userEvent.type(within(sheet).getByLabelText('Ссылка на GitHub'), 'https://github.com/a/bad')
    await userEvent.click(within(sheet).getByRole('button', { name: 'Проверить' }))
    expect(await within(sheet).findByText('В релизе нет antresol-addon.json')).toBeInTheDocument()
    expect(within(sheet).queryByRole('button', { name: 'Установить' })).toBeNull()
    expect(calls.some((c) => c.url === '/api/addons' && c.method === 'POST')).toBe(false)
  })

  it('switches an addon on and off', async () => {
    const calls = serve()
    open()
    await screen.findByText('Привет')
    await userEvent.click(screen.getByRole('switch', { name: 'Заметки' }))
    await waitFor(() => expect(screen.getByRole('switch', { name: 'Заметки' })).toHaveAttribute('aria-checked', 'true'))
    await userEvent.click(screen.getByRole('switch', { name: 'Привет' }))
    await waitFor(() => expect(screen.getByRole('switch', { name: 'Привет' })).toHaveAttribute('aria-checked', 'false'))
    expect(calls.filter((c) => c.method === 'POST').map((c) => c.url)).toEqual(['/api/addons/notes/enable', '/api/addons/hello/disable'])
    await waitFor(() => expect(reload).toHaveBeenCalledTimes(2))
  })

  it('a switch that fails says why and loads the list again', async () => {
    const calls = serve((c) => (c.url === '/api/addons/hello/disable' ? json({ error: 'Расширение не ответило' }, 400) : undefined))
    open()
    await screen.findByText('Привет')
    await userEvent.click(screen.getByRole('switch', { name: 'Привет' }))
    expect(await screen.findByText('Расширение не ответило')).toBeInTheDocument()
    await waitFor(() => expect(calls.filter((c) => c.url === '/api/addons' && c.method === 'GET')).toHaveLength(2))
    expect(await screen.findByRole('switch', { name: 'Привет' })).toHaveAttribute('aria-checked', 'true')
  })

  it('updates an addon after showing the new release', async () => {
    const calls = serve((c) =>
      c.url === '/api/addons/preview'
        ? json({ ...preview, id: 'hello', name: 'Привет', version: '1.2.0', permissions: ['library.read'], installed: '1.0.0' })
        : undefined,
    )
    open()
    await screen.findByText('Привет')
    await userEvent.click(screen.getByRole('button', { name: 'Обновить «Привет»' }))
    const sheet = await screen.findByRole('dialog', { name: 'Обновить «Привет»' })
    expect(await within(sheet).findByText('Читать медиатеку')).toBeInTheDocument()
    expect(within(sheet).getByText(/Ставьте только те, которым доверяете/)).toBeInTheDocument()
    expect(within(sheet).getByText('1.2.0')).toBeInTheDocument()
    expect(calls.find((c) => c.url === '/api/addons/preview')?.body).toEqual({ url: 'https://github.com/a/hello' })
    expect(calls.some((c) => c.url === '/api/addons/hello/update')).toBe(false)
    await userEvent.click(within(sheet).getByRole('button', { name: 'Обновить' }))
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Обновить «Привет»' })).toBeNull())
    await waitFor(() => expect(screen.getByText(/Версия 1\.2\.0/)).toBeInTheDocument())
    expect(screen.queryByText('Доступно обновление 1.2.0')).toBeNull()
    expect(calls.find((c) => c.url === '/api/addons/hello/update' && c.method === 'POST')?.body).toEqual({ version: '1.2.0' })
    await waitFor(() => expect(reload).toHaveBeenCalled())
  })

  it('removes an addon with its data after the confirmation', async () => {
    const calls = serve()
    open()
    await screen.findByText('Привет')
    await userEvent.click(screen.getByRole('button', { name: 'Ещё: Привет' }))
    await userEvent.click(await screen.findByRole('menuitem', { name: 'Удалить' }))
    const sheet = await screen.findByRole('alertdialog', { name: 'Удалить «Привет»?' })
    await userEvent.click(within(sheet).getByRole('switch', { name: 'Удалить и данные' }))
    await userEvent.click(within(sheet).getByRole('button', { name: 'Удалить' }))
    await waitFor(() => expect(screen.queryByText('Привет')).toBeNull())
    expect(calls.find((c) => c.method === 'DELETE')?.url).toBe('/api/addons/hello?data=1')
    await waitFor(() => expect(reload).toHaveBeenCalled())
  })

  it('removing without the switch keeps the data', async () => {
    const calls = serve()
    open()
    await screen.findByText('Привет')
    await userEvent.click(screen.getByRole('button', { name: 'Ещё: Привет' }))
    await userEvent.click(await screen.findByRole('menuitem', { name: 'Удалить' }))
    const sheet = await screen.findByRole('alertdialog', { name: 'Удалить «Привет»?' })
    await userEvent.click(within(sheet).getByRole('button', { name: 'Удалить' }))
    await waitFor(() => expect(screen.queryByText('Привет')).toBeNull())
    expect(calls.find((c) => c.method === 'DELETE')?.url).toBe('/api/addons/hello?data=0')
  })

  it('a pinned addon is locked: no update or remove, the switch and the log stay', async () => {
    const pinned = { ...hello, pinned: true, update: null }
    serve((c) => (c.url === '/api/addons' && c.method === 'GET' ? json([pinned, notes]) : undefined))
    open()
    expect(await screen.findByText('Привет')).toBeInTheDocument()
    expect(screen.getByText('Закреплено при развёртывании')).toBeInTheDocument()
    expect(screen.getAllByText('Закреплено при развёртывании')).toHaveLength(1)
    expect(screen.queryByRole('button', { name: 'Обновить «Привет»' })).toBeNull()
    expect(screen.getByRole('switch', { name: 'Привет' })).toBeEnabled()
    await userEvent.click(screen.getByRole('button', { name: 'Ещё: Привет' }))
    expect(await screen.findByRole('menuitem', { name: 'Журнал' })).toBeInTheDocument()
    expect(screen.queryByRole('menuitem', { name: 'Удалить' })).toBeNull()
  })

  it('a pin that failed says so and cannot be switched on', async () => {
    const failed = { ...notes, pinned: true, pinFailed: true }
    serve((c) => (c.url === '/api/addons' && c.method === 'GET' ? json([hello, failed]) : undefined))
    open()
    expect(await screen.findByText('Заметки')).toBeInTheDocument()
    expect(screen.getByText('Закреплённую версию не удалось установить, подробности в журнале сервера')).toBeInTheDocument()
    expect(screen.getByRole('switch', { name: 'Заметки' })).toBeDisabled()
    expect(screen.getByRole('switch', { name: 'Привет' })).toBeEnabled()
  })

  it('shows the last lines of the log', async () => {
    const calls = serve()
    open()
    await screen.findByText('Привет')
    await userEvent.click(screen.getByRole('button', { name: 'Ещё: Привет' }))
    await userEvent.click(await screen.findByRole('menuitem', { name: 'Журнал' }))
    const sheet = await screen.findByRole('dialog', { name: 'Журнал «Привет»' })
    expect(await within(sheet).findByText(/listening on socket/)).toBeInTheDocument()
    expect(calls.some((c) => c.url === '/api/addons/hello/log?n=200')).toBe(true)
  })

  it('follows an addon that is starting until it runs, then reloads the addons', async () => {
    let gets = 0
    serve((c) => {
      if (c.url !== '/api/addons' || c.method !== 'GET') return undefined
      gets++
      // starting (an import before it serves) for the first two answers
      return json([{ ...notes, enabled: true, status: gets <= 2 ? 'starting' : 'running' }])
    })
    open()
    expect(await screen.findByText(/Версия 0\.3\.1 · Запускается/)).toBeInTheDocument()
    expect(await screen.findByText(/Версия 0\.3\.1 · Работает/, undefined, { timeout: 5000 })).toBeInTheDocument()
    await waitFor(() => expect(reload).toHaveBeenCalled())
    // running: no more polling
    const after = gets
    await new Promise((r) => setTimeout(r, 1500))
    expect(gets).toBe(after)
  }, 10_000)
  it('a poll that fails keeps the list and goes on polling', async () => {
    let gets = 0
    serve((c) => {
      if (c.url !== '/api/addons' || c.method !== 'GET') return undefined
      gets++
      if (gets === 2) return json({ error: 'сервер занят' }, 500)
      return json([{ ...notes, enabled: true, status: gets <= 3 ? 'starting' : 'running' }])
    })
    open()
    expect(await screen.findByText(/Версия 0\.3\.1 · Запускается/)).toBeInTheDocument()
    expect(await screen.findByText(/Версия 0\.3\.1 · Работает/, undefined, { timeout: 6000 })).toBeInTheDocument()
    expect(gets).toBeGreaterThanOrEqual(4)
    expect(screen.queryByText('сервер занят')).not.toBeInTheDocument()
  }, 10_000)

  it('a poll answered after a change does not undo it', async () => {
    let gets = 0
    let answer: (r: Response) => void = () => {}
    serve((c) => {
      if (c.url === '/api/addons/notes/disable') return json({ ...notes, enabled: false, status: 'stopped' })
      if (c.url !== '/api/addons' || c.method !== 'GET') return undefined
      gets++
      if (gets === 1) return json([{ ...notes, enabled: true, status: 'starting' }])
      // the poll in flight while the switch is turned off: it answers late, stale
      return new Promise<Response>((r) => { answer = r })
    })
    open()
    expect(await screen.findByText(/Версия 0\.3\.1 · Запускается/)).toBeInTheDocument()
    await waitFor(() => expect(gets).toBe(2), { timeout: 3000 })
    await userEvent.click(screen.getByRole('switch'))
    expect(await screen.findByText(/Версия 0\.3\.1 · Остановлено|Версия 0\.3\.1 · Выключено/)).toBeInTheDocument()
    answer(json([{ ...notes, enabled: true, status: 'starting' }]))
    await new Promise((r) => setTimeout(r, 100))
    expect(screen.queryByText(/Запускается/)).not.toBeInTheDocument()
  }, 10_000)
})
