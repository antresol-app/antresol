import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { NotifySheet } from '../NotifySheet'

const json = (b: unknown, status = 200) => new Response(JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } })
const empty = {
  email: '',
  verified: false,
  episodes: false,
  available: false,
  requests: false,
  downloads: false,
  quietHours: true,
  lang: '',
  topics: ['episodes', 'available'],
  mailReady: true,
  publicLinks: true,
  bounced: false,
}

type Call = { url: string; method: string; body?: unknown }
let calls: Call[]
let state: typeof empty

beforeEach(() => {
  calls = []
  state = { ...empty }
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      const method = init?.method ?? 'GET'
      const body = typeof init?.body === 'string' ? JSON.parse(init.body) : undefined
      calls.push({ url, method, body })
      if (url.endsWith('/verify')) return new Response(null, { status: 204 })
      if (url.endsWith('/confirm')) {
        if (body.code !== '123456') return json({ error: 'Неверный или устаревший код' }, 400)
        state = { ...state, verified: true }
        return json(state)
      }
      if (method === 'PUT') {
        state = { ...state, ...body, verified: body.email === state.email && state.verified }
        return json(state)
      }
      return json(state)
    }),
  )
})
afterEach(() => vi.unstubAllGlobals())

describe('NotifySheet', () => {
  it('everything is off at first; saves the address, the topics and the language', async () => {
    const onClose = vi.fn()
    render(<NotifySheet onClose={onClose} />)
    const episodes = await screen.findByRole('switch', { name: 'Новые серии сериалов, которые я смотрю' })
    expect(episodes).toHaveAttribute('aria-checked', 'false')
    expect(screen.getByRole('switch', { name: 'Появилось то, что я хотел посмотреть' })).toHaveAttribute('aria-checked', 'false')
    expect(screen.queryByRole('switch', { name: 'Загрузка завершилась / не удалась' })).toBeNull()
    expect(screen.getByRole('switch', { name: 'Не присылать ночью (22:00–08:00)' })).toHaveAttribute('aria-checked', 'true')
    await userEvent.type(screen.getByLabelText('Адрес почты'), 'anya@example.com')
    await userEvent.click(episodes)
    await userEvent.click(screen.getByRole('button', { name: 'Сохранить' }))
    await waitFor(() => expect(onClose).toHaveBeenCalled())
    const put = calls.find((c) => c.method === 'PUT')!
    expect(put.url).toBe('/api/me/notify')
    expect(put.body).toEqual({ email: 'anya@example.com', episodes: true, available: false, requests: false, downloads: false, quietHours: true, lang: 'ru' })
  })

  it('sends the verification letter and confirms the code', async () => {
    render(<NotifySheet onClose={() => {}} />)
    await userEvent.type(await screen.findByLabelText('Адрес почты'), 'anya@example.com')
    expect(screen.getByText(/не подтверждён/)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Отправить проверочное письмо' }))
    // the typed address is saved first, then the letter goes
    await waitFor(() => expect(calls.some((c) => c.url === '/api/me/notify/verify')).toBe(true))
    const i = calls.findIndex((c) => c.method === 'PUT')
    expect(i).toBeGreaterThanOrEqual(0)
    expect(i).toBeLessThan(calls.findIndex((c) => c.url === '/api/me/notify/verify'))
    const code = await screen.findByLabelText('Код из письма')
    await userEvent.type(code, '000000')
    await userEvent.click(screen.getByRole('button', { name: 'Подтвердить' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Неверный или устаревший код')
    await userEvent.clear(code)
    await userEvent.type(code, '123456')
    await userEvent.click(screen.getByRole('button', { name: 'Подтвердить' }))
    expect(await screen.findByText('Адрес подтверждён')).toBeInTheDocument()
  })

  it('says when the address refused a letter', async () => {
    state = { ...empty, email: 'gone@example.com', bounced: true }
    render(<NotifySheet onClose={() => {}} />)
    expect(await screen.findByText(/Адрес не принимает письма/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Отправить проверочное письмо' })).toBeEnabled()
  })

  it('an administrator edits a kid’s; says when the server has no mail', async () => {
    state = { ...empty, topics: ['episodes', 'available', 'requests'], mailReady: false }
    render(<NotifySheet userId={7} name="Вася" onClose={() => {}} />)
    expect(await screen.findByRole('dialog', { name: 'Уведомления на почту: Вася' })).toBeInTheDocument()
    expect(await screen.findByText(/Почта сервера не настроена/)).toBeInTheDocument()
    expect(screen.getByRole('switch', { name: 'Мои просьбы одобрены / в медиатеке' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Отправить проверочное письмо' })).toBeDisabled()
    await userEvent.click(screen.getByRole('button', { name: 'Сохранить' }))
    await waitFor(() => expect(calls.some((c) => c.method === 'PUT' && c.url === '/api/users/7/notify')).toBe(true))
  })
})
