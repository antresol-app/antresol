import { afterEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { Settings as Values } from '../../../api/types'
import { providerOf } from '../AISettings'
import { renderSettings } from './settingsHarness'

const base: Values = {
  quality: 1080, voice: 'dub', maxSizeGB: 0, uploadLimited: false, uploadLimitKBs: 0, streamCacheGB: 50, remoteQuality: '1080',
  remoteMusic: 'high', preferLossless: true, autoSubtitles: true, subtitleLangs: ['ru'], subtitlesAvailable: false,
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
    await userEvent.click(screen.getByLabelText('Название модели'))
    await waitFor(() => expect(puts.at(-1)?.ai?.key).toBe('AIza-secret'))
    expect(screen.getByLabelText('Ключ API')).toHaveValue('')
    await userEvent.click(screen.getByRole('button', { name: 'Проверить' }))
    expect(await screen.findByText('Работает: ответ за 480 мс')).toBeInTheDocument()
    expect(tests.at(-1)).toMatchObject({ url: 'https://generativelanguage.googleapis.com/v1beta/openai', model: 'gemini-3.5-flash-lite' })
  })

  it('off, the server’s own model is named', async () => {
    stub({ ...base, aiFromEnv: 'gemini-flash-lite' })
    renderSettings('ai')
    expect(await screen.findByText('Сейчас сервер использует свою модель: gemini-flash-lite.')).toBeInTheDocument()
    expect(screen.queryByLabelText('Адрес')).toBeNull()
  })

  it('providerOf', () => {
    expect(providerOf('')).toBe('off')
    expect(providerOf('https://api.groq.com/openai/v1')).toBe('groq')
    expect(providerOf('http://127.0.0.1:4000/v1')).toBe('litellm')
    expect(providerOf('http://host:11434')).toBe('ollama')
  })
})
