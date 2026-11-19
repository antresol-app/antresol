// «Внешние сервисы»: a switch per service with what it sends and gives and
// a link to it; a switch saves at once; a service the server's environment
// sets is locked. «Состояние» shows a one-time notice while nothing was
// chosen, and either of its buttons closes it for good.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import type { ExternalView } from '../../../api/external'
import { ExternalServicesSection } from '../ExternalServicesSettings'
import { Health } from '../Health'

vi.mock('../../../state/session', async (orig) => ({ ...(await orig<object>()), useSession: () => ({ user: { isAdmin: true } }) }))

const json = (b: unknown) => new Response(JSON.stringify(b), { status: 200, headers: { 'Content-Type': 'application/json' } })

const view = (over: Partial<ExternalView> = {}): ExternalView => ({
  services: [
    { id: 'wikidata', on: true, default: true, locked: false },
    { id: 'imdb', on: false, default: false, locked: false },
    { id: 'lyrics', on: false, default: false, locked: true },
    { id: 'segments', on: true, default: true, locked: false },
  ],
  chosen: false,
  notice: true,
  ...over,
})

function serve(start: ExternalView) {
  const puts: unknown[] = []
  let current = start
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const u = String(input)
      if (u === '/api/settings/external' && init?.method === 'PUT') {
        const body = JSON.parse(String(init.body)) as { services?: Record<string, boolean>; dismissNotice?: boolean }
        puts.push(body)
        current = {
          services: current.services.map((s) => (body.services && s.id in body.services && !s.locked ? { ...s, on: body.services[s.id]! } : s)),
          chosen: current.chosen || Boolean(body.services),
          notice: false,
        }
        return json(current)
      }
      if (u === '/api/settings/external') return json(current)
      return new Response('{}', { status: 404, headers: { 'Content-Type': 'application/json' } })
    }),
  )
  return puts
}

describe('«Внешние сервисы»', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('lists every service with what it sends and gives, its link, and saves a switch at once', async () => {
    const puts = serve(view())
    render(
      <MemoryRouter>
        <ExternalServicesSection />
      </MemoryRouter>,
    )
    const imdb = await screen.findByRole('switch', { name: 'Рейтинги IMDb' })
    expect(imdb).toHaveAttribute('aria-checked', 'false')
    expect(screen.getByRole('switch', { name: 'Wikidata' })).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByText(/Получает номера фильмов и сериалов в TMDB и IMDb/)).toBeInTheDocument()
    expect(screen.getByText(/личного некоммерческого использования/)).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'wikidata.org' })).toHaveAttribute('href', 'https://www.wikidata.org')
    expect(screen.getByText(/без перезапуска/)).toBeInTheDocument()
    await userEvent.click(imdb)
    await waitFor(() => expect(puts).toEqual([{ services: { imdb: true } }]))
    expect(screen.getByRole('switch', { name: 'Рейтинги IMDb' })).toHaveAttribute('aria-checked', 'true')
  })

  it('shows a service the environment sets as locked', async () => {
    serve(view())
    render(
      <MemoryRouter>
        <ExternalServicesSection />
      </MemoryRouter>,
    )
    const lyrics = await screen.findByRole('switch', { name: 'Тексты песен (LRCLIB)' })
    expect(lyrics).toBeDisabled()
    expect(screen.getByText('Выключено на сервере (VAULT_LRCLIB_URL)')).toBeInTheDocument()
  })

  it('«Состояние» shows the one-time notice while nothing was chosen; «Скрыть» closes it for good', async () => {
    const puts = serve(view())
    render(
      <MemoryRouter>
        <Health />
      </MemoryRouter>,
    )
    expect(await screen.findByText('Выберите внешние сервисы')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Открыть' })).toHaveAttribute('href', '/manage/settings/external')
    await userEvent.click(screen.getByRole('button', { name: 'Скрыть' }))
    expect(screen.queryByText('Выберите внешние сервисы')).toBeNull()
    await waitFor(() => expect(puts).toEqual([{ dismissNotice: true }]))
  })

  it('«Состояние» has no notice once the services were chosen', async () => {
    serve(view({ chosen: true, notice: false }))
    render(
      <MemoryRouter>
        <Health />
      </MemoryRouter>,
    )
    await screen.findByText('Состояние')
    await new Promise((r) => setTimeout(r, 20))
    expect(screen.queryByText('Выберите внешние сервисы')).toBeNull()
  })
})
