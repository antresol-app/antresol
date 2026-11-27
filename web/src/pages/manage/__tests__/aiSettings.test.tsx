import { afterEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { Settings as Values } from '../../../api/types'
import { chosenProvider, providerOf } from '../AISettings'
import { settingsSummary } from '../settingsSummary'
import { renderSettings } from './settingsHarness'

const base: Values = {
  quality: 1080, voice: 'dub', remoteQuality: '1080',
  remoteMusic: 'high', autoSubtitles: true, subtitleLangs: ['ru'], subtitlesAvailable: false,
}
const json = (b: unknown) => new Response(JSON.stringify(b), { status: 200, headers: { 'Content-Type': 'application/json' } })

function stub(v: Values) {
  const puts: Values[] = []
  const tests: unknown[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      if (url.endsWith('/api/settings/ai/test')) {
        tests.push(JSON.parse(String(init!.body)))
        return json({ ok: true, ms: 480, reply: 'ok' })
      }
      if (url.endsWith('/api/settings') && init?.method === 'PUT') {
        const b = JSON.parse(String(init.body)) as Values
        puts.push(b)
        return json({ ...b, ai: b.ai && { ...b.ai, key: undefined }, aiKeySet: Boolean(b.ai?.key) || v.aiKeySet })
      }
      if (url.endsWith('/api/settings')) return json(v)
      return json({})
    }),
  )
  return { puts, tests }
}

afterEach(() => vi.unstubAllGlobals())

describe('«ИИ-помощник»', () => {
  it('a provider fills its address and model; saved with its key; Enter saves the key and checks it', async () => {
    const { puts, tests } = stub(base)
    renderSettings('ai')
    await userEvent.click(await screen.findByRole('button', { name: /^Модель/ }))
    await userEvent.click(screen.getByRole('menuitemradio', { name: 'Google Gemini' }))
    expect(screen.getByLabelText('Адрес')).toHaveValue('https://generativelanguage.googleapis.com/v1beta/openai')
    // nothing saved before the key: no «needs a key» error on choosing
    expect(puts).toHaveLength(0)
    await userEvent.type(screen.getByLabelText('Ключ API'), 'AIza-secret{Enter}')
    await waitFor(() => expect(puts.at(-1)?.ai).toMatchObject({ url: 'https://generativelanguage.googleapis.com/v1beta/openai', model: 'gemini-3.5-flash-lite', key: 'AIza-secret' }))
    expect(await screen.findByText('Работает: ответ за 480 мс')).toBeInTheDocument()
    expect(tests.at(-1)).toMatchObject({ url: 'https://generativelanguage.googleapis.com/v1beta/openai', model: 'gemini-3.5-flash-lite', key: 'AIza-secret' })
    expect(screen.getByLabelText('Ключ API')).toHaveValue('')
  })

  it('«Проверить» right after typing a key checks that key, not the stored one', async () => {
    const { puts, tests } = stub(base)
    renderSettings('ai')
    await userEvent.click(await screen.findByRole('button', { name: /^Модель/ }))
    await userEvent.click(screen.getByRole('menuitemradio', { name: 'Groq' }))
    await userEvent.type(screen.getByLabelText('Ключ API'), 'gsk-typed')
    await userEvent.click(screen.getByRole('button', { name: 'Проверить' }))
    await waitFor(() => expect(tests.at(-1)).toMatchObject({ url: 'https://api.groq.com/openai/v1', key: 'gsk-typed' }))
    await waitFor(() => expect(puts.at(-1)?.ai).toMatchObject({ url: 'https://api.groq.com/openai/v1', key: 'gsk-typed' }))
  })

  it('a stored key shows «сохранён» only for its own provider', async () => {
    stub({ ...base, ai: { url: 'https://api.groq.com/openai/v1', model: 'm' }, aiKeySet: true })
    renderSettings('ai')
    expect(await screen.findByLabelText('Ключ API')).toHaveAttribute('placeholder', 'Сохранён')
    await userEvent.click(screen.getByRole('button', { name: /^Модель/ }))
    await userEvent.click(screen.getByRole('menuitemradio', { name: 'Google Gemini' }))
    expect(screen.getByLabelText('Ключ API')).not.toHaveAttribute('placeholder')
  })

  it('the server’s own model is the current choice, «Проверить» asks it, «Выключено» saves off', async () => {
    const { puts, tests } = stub({ ...base, aiFromEnv: 'gemini-flash-lite' })
    renderSettings('ai')
    expect(await screen.findByRole('button', { name: /^Модель/ })).toHaveTextContent('Сервер — gemini-flash-lite')
    expect(screen.queryByLabelText('Адрес')).toBeNull()
    await userEvent.click(screen.getByRole('button', { name: 'Проверить' }))
    expect(await screen.findByText('Работает: ответ за 480 мс')).toBeInTheDocument()
    expect(tests.at(-1)).toMatchObject({ url: '' })
    await userEvent.click(screen.getByRole('button', { name: /^Модель/ }))
    await userEvent.click(screen.getByRole('menuitemradio', { name: 'Выключено' }))
    await waitFor(() => expect(puts.at(-1)?.ai).toMatchObject({ url: '', off: true }))
    expect(screen.queryByRole('button', { name: /^Проверить/ })).toBeNull()
    // the server’s own stays in the list: back to it saves «not off»
    await userEvent.click(screen.getByRole('button', { name: /^Модель/ }))
    await userEvent.click(screen.getByRole('menuitemradio', { name: 'Сервер — gemini-flash-lite' }))
    await waitFor(() => expect(puts.at(-1)?.ai).toMatchObject({ url: '', off: false }))
    expect(screen.getByRole('button', { name: 'Проверить' })).toBeInTheDocument()
  })

  it('a stored «Выключено» opens off although the server has a model; a server without one has no «Сервер» choice', async () => {
    stub({ ...base, aiFromEnv: 'gemini-flash-lite', ai: { url: '', model: '', off: true } })
    const first = renderSettings('ai')
    expect(await screen.findByRole('button', { name: /^Модель/ })).toHaveTextContent('Выключено')
    expect(screen.queryByRole('button', { name: /^Проверить/ })).toBeNull()
    first.unmount()
    stub(base)
    renderSettings('ai')
    await userEvent.click(await screen.findByRole('button', { name: /^Модель/ }))
    expect(screen.queryByRole('menuitemradio', { name: /^Сервер/ })).toBeNull()
  })

  it('the choice and the summary: stored address, off, the server’s', () => {
    expect(chosenProvider({ url: 'http://127.0.0.1:4000/v1', model: 'm' }, 'env-model')).toBe('litellm')
    expect(chosenProvider({ url: '', model: '', off: true }, 'env-model')).toBe('off')
    expect(chosenProvider({ url: '', model: '' }, 'env-model')).toBe('server')
    expect(chosenProvider(undefined, undefined)).toBe('off')
    expect(settingsSummary('ai', { ...base, aiFromEnv: 'env-model' })).toBe('env-model')
    expect(settingsSummary('ai', { ...base, aiFromEnv: 'env-model', ai: { url: '', model: '', off: true } })).toBe('Выключено')
    expect(settingsSummary('ai', { ...base, ai: { url: 'http://x/v1', model: 'mine' }, aiFromEnv: 'env-model' })).toBe('mine')
  })

  it('providerOf', () => {
    expect(providerOf('')).toBe('off')
    expect(providerOf('https://api.groq.com/openai/v1')).toBe('groq')
    expect(providerOf('http://127.0.0.1:4000/v1')).toBe('litellm')
    expect(providerOf('http://host:11434')).toBe('ollama')
    expect(providerOf('https://api.anthropic.com')).toBe('anthropic')
  })
})
