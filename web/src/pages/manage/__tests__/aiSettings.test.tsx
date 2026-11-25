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
  it('a provider fills its address and model; the key is written once; «Проверить» asks', async () => {
    const { puts, tests } = stub(base)
    renderSettings('ai')
    await userEvent.click(await screen.findByRole('button', { name: /^Модель/ }))
    await userEvent.click(screen.getByRole('menuitemradio', { name: 'Google Gemini' }))
    await waitFor(() => expect(puts.at(-1)?.ai).toMatchObject({ url: 'https://generativelanguage.googleapis.com/v1beta/openai', model: 'gemini-3.5-flash-lite' }))
    await userEvent.type(screen.getByLabelText('Ключ API'), 'AIza-secret')
    await userEvent.click(screen.getByLabelText('Средняя модель'))
    await waitFor(() => expect(puts.at(-1)?.ai?.key).toBe('AIza-secret'))
    expect(screen.getByLabelText('Ключ API')).toHaveValue('')
    await userEvent.click(screen.getByRole('button', { name: 'Проверить' }))
    expect(await screen.findByText('Работает: ответ за 480 мс')).toBeInTheDocument()
    expect(tests.at(-1)).toMatchObject({ url: 'https://generativelanguage.googleapis.com/v1beta/openai', model: 'gemini-3.5-flash-lite' })
  })

  it('three tiers: Anthropic picks Haiku, Sonnet, Opus; a server of one model is that model on each', async () => {
    const { puts } = stub({ ...base, ai: { url: 'http://127.0.0.1:4000/v1', model: 'local' } })
    renderSettings('ai')
    expect(await screen.findByLabelText('Лёгкая модель')).toHaveValue('local')
    expect(screen.getByLabelText('Мощная модель')).toHaveValue('local')
    await userEvent.click(screen.getByRole('button', { name: /^Модель/ }))
    await userEvent.click(screen.getByRole('menuitemradio', { name: 'Anthropic Claude' }))
    await waitFor(() =>
      expect(puts.at(-1)?.ai).toMatchObject({ url: 'https://api.anthropic.com', model: 'claude-sonnet-5-5', models: { light: 'claude-haiku-5-5', medium: 'claude-sonnet-5-5', powerful: 'claude-opus-5-5' } }),
    )
    await userEvent.click(screen.getByRole('button', { name: /^Лёгкая модель/ }))
    await userEvent.click(screen.getByRole('menuitemradio', { name: 'claude-sonnet-5-5' }))
    await waitFor(() => expect(puts.at(-1)?.ai?.models?.light).toBe('claude-sonnet-5-5'))
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
