// «Настройки сервера → ИИ-помощник → Распознавание речи»: the service that
// makes audiobooks' transcripts — off, the server's own, or a provider with
// its key, a server of one's own with its address and model; «Проверить».
import { afterEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { Settings as Values } from '../../../api/types'
import { chosenSTT } from '../STTSettings'
import { renderSettings } from './settingsHarness'

const base: Values = {
  quality: 1080, voice: 'dub', remoteQuality: '1080',
  remoteMusic: 'high', autoSubtitles: true, subtitleLangs: ['ru'], subtitlesAvailable: false,
}
const json = (b: unknown) => new Response(JSON.stringify(b), { status: 200, headers: { 'Content-Type': 'application/json' } })

function stub(v: Values, answer: unknown = { ok: true, ms: 640, model: 'whisper-large-v3-turbo' }) {
  const puts: Values[] = []
  const tests: unknown[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      if (url.endsWith('/api/settings/stt/test')) {
        tests.push(JSON.parse(String(init!.body)))
        return json(answer)
      }
      if (url.endsWith('/api/settings') && init?.method === 'PUT') {
        const b = JSON.parse(String(init.body)) as Values
        puts.push(b)
        return json({ ...b, stt: b.stt && { ...b.stt, key: undefined }, sttKeySet: Boolean(b.stt?.key) })
      }
      if (url.endsWith('/api/settings')) return json(v)
      return json({})
    }),
  )
  return { puts, tests }
}

const section = async () => within((await screen.findByRole('heading', { name: 'Распознавание речи' })).closest('section')!)

afterEach(() => vi.unstubAllGlobals())

describe('«Распознавание речи»', () => {
  it('is off by default; a provider saves, takes its key once and is checked', async () => {
    const { puts, tests } = stub(base)
    renderSettings('ai')
    const s = await section()
    const pick = s.getByRole('button', { name: /^Сервис/ })
    expect(pick).toHaveTextContent('Выключено')
    expect(s.queryByRole('button', { name: 'Проверить' })).toBeNull()
    expect(s.queryByLabelText('Ключ API')).toBeNull()
    await userEvent.click(pick)
    await userEvent.click(screen.getByRole('menuitemradio', { name: 'Groq' }))
    await waitFor(() => expect(puts.at(-1)?.stt).toEqual({ provider: 'groq', url: '', model: '', key: '' }))
    // the usual model is shown, not typed in; a provider has its own address
    expect(s.getByLabelText('Модель')).toHaveValue('')
    expect(s.getByLabelText('Модель')).toHaveAttribute('placeholder', 'whisper-large-v3-turbo')
    expect(s.queryByLabelText('Адрес')).toBeNull()
    await userEvent.type(s.getByLabelText('Ключ API'), 'gsk-secret')
    await userEvent.tab()
    await waitFor(() => expect(puts.at(-1)?.stt?.key).toBe('gsk-secret'))
    expect(s.getByLabelText('Ключ API')).toHaveValue('')
    await userEvent.click(s.getByRole('button', { name: 'Проверить' }))
    expect(await s.findByText('Работает: ответ за 640 мс')).toBeInTheDocument()
    expect(tests.at(-1)).toEqual({ provider: 'groq', url: '', model: '' })
    // off again: saved as «off», nothing left to fill in
    await userEvent.click(s.getByRole('button', { name: /^Сервис/ }))
    await userEvent.click(screen.getByRole('menuitemradio', { name: 'Выключено' }))
    await waitFor(() => expect(puts.at(-1)?.stt).toEqual({ provider: 'off', url: '', model: '', key: '' }))
    expect(s.queryByRole('button', { name: 'Проверить' })).toBeNull()
  })

  it('a server of one’s own is saved once its address and model are there', async () => {
    const { puts } = stub({ ...base, stt: { provider: 'openai', url: '', model: '' }, sttKeySet: true })
    renderSettings('ai')
    const s = await section()
    expect(s.getByLabelText('Ключ API')).toHaveAttribute('placeholder', 'Сохранён')
    await userEvent.click(s.getByRole('button', { name: /^Сервис/ }))
    await userEvent.click(screen.getByRole('menuitemradio', { name: 'LiteLLM или свой сервер' }))
    // the other provider's key is not this one's
    expect(s.getByLabelText('Ключ API')).not.toHaveAttribute('placeholder', 'Сохранён')
    await userEvent.type(s.getByLabelText('Адрес'), 'http://proxy:4000/v1')
    await userEvent.tab()
    expect(puts).toHaveLength(0)
    await userEvent.type(s.getByLabelText('Модель'), 'speech')
    await userEvent.tab()
    await waitFor(() => expect(puts.at(-1)?.stt).toEqual({ provider: 'custom', url: 'http://proxy:4000/v1', model: 'speech', key: '' }))
  })

  it('a key typed before the address is saved with it, then cleared from the field', async () => {
    const { puts } = stub({ ...base, stt: { provider: 'off', url: '', model: '' } })
    renderSettings('ai')
    const s = await section()
    await userEvent.click(s.getByRole('button', { name: /^Сервис/ }))
    await userEvent.click(screen.getByRole('menuitemradio', { name: 'LiteLLM или свой сервер' }))
    await userEvent.type(s.getByLabelText('Ключ API'), 'sk-early')
    await userEvent.tab()
    expect(puts).toHaveLength(0)
    expect(s.getByLabelText('Ключ API')).toHaveValue('sk-early')
    await userEvent.type(s.getByLabelText('Адрес'), 'http://proxy:4000/v1')
    await userEvent.type(s.getByLabelText('Модель'), 'speech')
    await userEvent.tab()
    await waitFor(() => expect(puts.at(-1)?.stt).toEqual({ provider: 'custom', url: 'http://proxy:4000/v1', model: 'speech', key: 'sk-early' }))
    expect(puts).toHaveLength(1)
    expect(s.getByLabelText('Ключ API')).toHaveValue('')
  })

  it('the server’s own service is the choice until another is made; a failed check says why', async () => {
    const { puts, tests } = stub({ ...base, sttFromEnv: 'server-whisper' }, { ok: false, ms: 20, model: 'server-whisper', error: 'stt: 401 Unauthorized' })
    renderSettings('ai')
    const s = await section()
    expect(s.getByRole('button', { name: /^Сервис/ })).toHaveTextContent('Сервер — server-whisper')
    expect(s.queryByLabelText('Модель')).toBeNull()
    await userEvent.click(s.getByRole('button', { name: 'Проверить' }))
    expect(await s.findByText('Не отвечает: stt: 401 Unauthorized')).toBeInTheDocument()
    expect(tests.at(-1)).toEqual({ provider: '', url: '', model: '' })
    await userEvent.click(s.getByRole('button', { name: /^Сервис/ }))
    await userEvent.click(screen.getByRole('menuitemradio', { name: 'Выключено' }))
    await waitFor(() => expect(puts.at(-1)?.stt?.provider).toBe('off'))
    await userEvent.click(s.getByRole('button', { name: /^Сервис/ }))
    await userEvent.click(screen.getByRole('menuitemradio', { name: 'Сервер — server-whisper' }))
    await waitFor(() => expect(puts.at(-1)?.stt?.provider).toBe(''))
  })

  it('the current choice', () => {
    expect(chosenSTT(undefined, undefined)).toBe('off')
    expect(chosenSTT({ provider: '', url: '', model: '' }, undefined)).toBe('off')
    expect(chosenSTT({ provider: '', url: '', model: '' }, 'm')).toBe('server')
    expect(chosenSTT({ provider: 'off', url: '', model: '' }, 'm')).toBe('off')
    expect(chosenSTT({ provider: 'gemini', url: '', model: '' }, 'm')).toBe('gemini')
  })
})
