// «Обновления»: an addon whose update check failed or is still running is
// never shown as «Последняя версия»; «Проверить снова» asks the server for
// a fresh check.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { Updates } from '../Updates'

vi.mock('../../../state/session', () => ({ useSession: () => ({ user: { addons: true, isAdmin: true } }) }))

const json = (b: unknown) => new Response(JSON.stringify(b), { status: 200, headers: { 'Content-Type': 'application/json' } })
const base = { description: '', source: 'github.com/a/x', enabled: true, status: 'running', pinned: false, pinFailed: false, helpers: [] }
const core = { version: '0.8.11', commit: '', latest: 'v0.8.11', releaseUrl: '', checked: true, newer: false, docker: true }

function serve(list: () => unknown[]) {
  const urls: string[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const u = String(input)
      urls.push(u)
      if (u === '/api/updates') return json(core)
      if (u.startsWith('/api/addons')) return json(list())
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
    render(<MemoryRouter><Updates /></MemoryRouter>)
    expect(await screen.findByText('Не удалось проверить: GitHub не ответил вовремя')).toBeInTheDocument()
    expect(screen.getByText('Версии 0.5.0 нужен более новый сервер: обновите сервер')).toBeInTheDocument()
    // only the server's own row is «Последняя версия»
    expect(screen.getAllByText('Последняя версия')).toHaveLength(1)
    failed = false
    await userEvent.click(screen.getByRole('button', { name: 'Проверить «Онлайн» снова' }))
    expect(await screen.findByText('Доступна 0.1.2')).toBeInTheDocument()
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
    render(<MemoryRouter><Updates /></MemoryRouter>)
    expect(await screen.findByText('Проверяется…')).toBeInTheDocument()
    await waitFor(() => expect(screen.getByText('Доступна 0.1.2')).toBeInTheDocument(), { timeout: 5000 })
  }, 10_000)
})
