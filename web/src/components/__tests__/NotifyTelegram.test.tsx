import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import type { NotifyPrefs } from '../../api/types'
import { NotifyTelegram } from '../NotifyTelegram'

const json = (b: unknown, status = 200) => new Response(JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } })
const base: NotifyPrefs = {
  email: '',
  verified: false,
  episodes: false,
  available: false,
  requests: false,
  quietHours: true,
  lang: '',
  topics: ['episodes'],
  mailReady: true,
  publicLinks: true,
  bounced: false,
  emailOn: true,
  telegramOn: true,
  telegramReady: true,
  telegramBot: 'home_bot',
  telegramLinked: false,
  telegramName: '',
}

type Call = { url: string; method: string }
let calls: Call[]
let server: NotifyPrefs
let linkAnswer: () => Response

beforeEach(() => {
  calls = []
  server = { ...base }
  linkAnswer = () => json({ code: 'abc123', url: 'https://t.me/home_bot?start=abc123', bot: 'home_bot', expires: new Date(Date.now() + 60_000).toISOString() })
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      const method = init?.method ?? 'GET'
      calls.push({ url, method })
      if (url === '/api/me/notify/telegram/link') return linkAnswer()
      if (url === '/api/me/notify/telegram' && method === 'DELETE') {
        server = { ...server, telegramLinked: false, telegramName: '' }
        return json(server)
      }
      return json(server)
    }),
  )
})
afterEach(() => vi.unstubAllGlobals())

/** The sheet's side of it: it holds the prefs and takes what the section reports. */
function Host({ initial, onToggle = () => {}, onError = () => {} }: { initial: NotifyPrefs; onToggle?: (on: boolean) => void; onError?: (m: string) => void }) {
  const [prefs, setPrefs] = useState(initial)
  return <NotifyTelegram prefs={prefs} watchMs={20} onLinked={(p) => setPrefs((s) => ({ ...s, ...p }))} onToggle={onToggle} onError={onError} />
}

describe('NotifyTelegram', () => {
  it('stays out of the way where the server has no bot', () => {
    const { container } = render(<Host initial={{ ...base, telegramReady: false }} />)
    expect(container).toBeEmptyDOMElement()
  })

  it('connects: shows the link and the command, then notices the linked chat by itself', async () => {
    render(<Host initial={base} />)
    expect(screen.getByText('Те же уведомления придут в чат с ботом @home_bot.')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Подключить Telegram' }))
    const open = await screen.findByRole('link', { name: 'Открыть Telegram' })
    expect(open).toHaveAttribute('href', 'https://t.me/home_bot?start=abc123')
    expect(open).toHaveAttribute('target', '_blank')
    expect(open.getAttribute('rel')).toContain('noopener')
    expect(screen.getByText('Или отправьте боту @home_bot сообщение: /start abc123')).toBeInTheDocument()
    expect(calls.filter((c) => c.url === '/api/me/notify/telegram/link' && c.method === 'POST')).toHaveLength(1)

    // the person presses «Запустить» in Telegram: the server links the chat
    server = { ...server, telegramLinked: true, telegramName: '@anya' }
    expect(await screen.findByText('Подключено: @anya')).toBeInTheDocument()
    expect(screen.getByRole('switch', { name: 'Присылать в Telegram' })).toHaveAttribute('aria-checked', 'true')
    expect(screen.queryByRole('link', { name: 'Открыть Telegram' })).toBeNull()
    // nothing keeps asking once it is linked
    const asked = calls.length
    await new Promise((r) => setTimeout(r, 80))
    expect(calls.length).toBe(asked)
  })

  it('a code that ran out says so and offers a new one', async () => {
    linkAnswer = () => json({ code: 'old', url: 'https://t.me/home_bot?start=old', bot: 'home_bot', expires: new Date(Date.now() + 30).toISOString() })
    render(<Host initial={base} />)
    await userEvent.click(screen.getByRole('button', { name: 'Подключить Telegram' }))
    expect(await screen.findByText('Ссылка устарела. Нажмите «Подключить Telegram» ещё раз.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Подключить Telegram' })).toBeInTheDocument()
  })

  it('«Отмена» drops the code', async () => {
    render(<Host initial={base} />)
    await userEvent.click(screen.getByRole('button', { name: 'Подключить Telegram' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Отмена' }))
    expect(screen.getByRole('button', { name: 'Подключить Telegram' })).toBeInTheDocument()
  })

  it('says why when no code could be made', async () => {
    linkAnswer = () => json({ error: 'Telegram не настроен: бота подключает администратор в настройках сервера' }, 409)
    const onError = vi.fn()
    render(<Host initial={base} onError={onError} />)
    await userEvent.click(screen.getByRole('button', { name: 'Подключить Telegram' }))
    await waitFor(() => expect(onError).toHaveBeenLastCalledWith('Telegram не настроен: бота подключает администратор в настройках сервера'))
  })

  it('linked: the channel switch, and «Отключить» forgets the chat', async () => {
    const onToggle = vi.fn()
    render(<Host initial={{ ...base, telegramLinked: true, telegramName: '@anya' }} onToggle={onToggle} />)
    expect(screen.getByText('Подключено: @anya')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('switch', { name: 'Присылать в Telegram' }))
    expect(onToggle).toHaveBeenCalledWith(false)
    await userEvent.click(screen.getByRole('button', { name: 'Отключить' }))
    expect(await screen.findByRole('button', { name: 'Подключить Telegram' })).toBeInTheDocument()
    expect(calls.some((c) => c.method === 'DELETE' && c.url === '/api/me/notify/telegram')).toBe(true)
  })

  it('a chat stays shown when the administrator turned the bot off', () => {
    render(<Host initial={{ ...base, telegramReady: false, telegramLinked: true, telegramName: '@anya' }} />)
    expect(screen.getByText('Подключено: @anya')).toBeInTheDocument()
    expect(screen.getByText(/Администратор отключил бота/)).toBeInTheDocument()
  })
})
