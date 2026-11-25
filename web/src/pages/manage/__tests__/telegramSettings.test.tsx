import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { TelegramBot } from '../../../api/types'
import { TelegramSection } from '../TelegramSettings'

const json = (b: unknown, status = 200) => new Response(JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } })

type Call = { url: string; method: string; body?: unknown }
let calls: Call[]
let bot: TelegramBot
let putAnswer: (token: string) => Response

beforeEach(() => {
  calls = []
  bot = { set: false, polling: false }
  putAnswer = () => json({ set: true, username: 'home_bot', name: 'Home Media', polling: true })
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      const method = init?.method ?? 'GET'
      const body = typeof init?.body === 'string' ? JSON.parse(init.body) : undefined
      calls.push({ url, method, body })
      if (url === '/api/settings/telegram' && method === 'PUT') {
        const r = putAnswer(body.token)
        if (r.ok) bot = (await r.clone().json()) as TelegramBot
        return r
      }
      if (url === '/api/settings/telegram/check') return json(bot)
      if (url === '/api/settings/telegram' && method === 'DELETE') {
        bot = { set: false, polling: false }
        return json(bot)
      }
      return json(bot)
    }),
  )
})
afterEach(() => vi.unstubAllGlobals())

describe('«Бот Telegram» в настройках сервера', () => {
  it('not set up: says so; the token is pasted, checked, kept, and the bot’s name shown', async () => {
    render(<TelegramSection />)
    expect(await screen.findByText('Не подключён')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: /^Бот Telegram/ }))
    const sheet = screen.getByRole('dialog', { name: 'Бот Telegram' })
    // the steps are first, until the bot is set up
    expect(within(sheet).getByText(/@BotFather/, { selector: 'li' })).toBeInTheDocument()
    expect(within(sheet).getByRole('link', { name: 'Открыть @BotFather' })).toHaveAttribute('href', 'https://t.me/BotFather')
    const field = within(sheet).getByLabelText('Токен бота')
    expect(field).toHaveAttribute('type', 'password')
    expect(within(sheet).getByRole('button', { name: 'Проверить' })).toBeDisabled()
    await userEvent.type(field, '123:test-token')
    await userEvent.click(within(sheet).getByRole('button', { name: 'Проверить' }))
    expect(await within(sheet).findByText('Бот найден: @home_bot')).toBeInTheDocument()
    expect(calls.find((c) => c.method === 'PUT')?.body).toEqual({ token: '123:test-token' })
    // kept by the server: the field is empty again and the token is nowhere on the page
    expect(within(sheet).getByLabelText('Токен бота')).toHaveValue('')
    expect(document.body.textContent).not.toContain('test-token')
    await userEvent.click(within(sheet).getByRole('button', { name: 'Готово' }))
    expect(await screen.findByText('@home_bot')).toBeInTheDocument()
  })

  it('a token Telegram refuses shows its answer and keeps nothing', async () => {
    putAnswer = () => json({ error: 'Telegram не принял токен — проверьте, что он скопирован целиком и бот не удалён' }, 400)
    render(<TelegramSection />)
    await userEvent.click(await screen.findByRole('button', { name: /^Бот Telegram/ }))
    const sheet = screen.getByRole('dialog', { name: 'Бот Telegram' })
    await userEvent.type(within(sheet).getByLabelText('Токен бота'), '999:wrong-token')
    await userEvent.click(within(sheet).getByRole('button', { name: 'Проверить' }))
    expect(await within(sheet).findByRole('alert')).toHaveTextContent('Telegram не принял токен')
    expect(screen.queryByText(/Бот найден/)).toBeNull()
  })

  it('set up: the row names the bot; «Проверить» asks again without a token; «Отключить бота» forgets it', async () => {
    bot = { set: true, username: 'home_bot', name: 'Home Media', polling: true }
    render(<TelegramSection />)
    expect(await screen.findByText('@home_bot')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: /^Бот Telegram/ }))
    const sheet = screen.getByRole('dialog', { name: 'Бот Telegram' })
    expect(within(sheet).getByText('Токен сохранён. Вставьте новый, чтобы заменить его.')).toBeInTheDocument()
    // the steps are on demand now
    expect(within(sheet).queryByRole('link', { name: 'Открыть @BotFather' })).toBeNull()
    await userEvent.click(within(sheet).getByRole('button', { name: 'Проверить' }))
    await waitFor(() => expect(calls.some((c) => c.url === '/api/settings/telegram/check' && c.method === 'POST')).toBe(true))
    expect(calls.some((c) => c.method === 'PUT')).toBe(false)
    await userEvent.click(within(sheet).getByRole('button', { name: 'Отключить бота' }))
    await waitFor(() => expect(calls.some((c) => c.method === 'DELETE')).toBe(true))
    await userEvent.click(await within(sheet).findByRole('button', { name: 'Готово' }))
    expect(await screen.findByText('Не подключён')).toBeInTheDocument()
  })

  it('says what is in the way when the server cannot poll', async () => {
    bot = { set: true, username: 'home_bot', polling: true, problem: 'conflict' }
    render(<TelegramSection />)
    await userEvent.click(await screen.findByRole('button', { name: /^Бот Telegram/ }))
    expect(await screen.findByText(/уже забирает другая программа/)).toBeInTheDocument()
  })
})
