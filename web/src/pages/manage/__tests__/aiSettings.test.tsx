import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { AICheck, AIModel, AISettings, Settings as Values } from '../../../api/types'
import { providerOf } from '../AISettings'
import { byKind, entryHint, needsKey, statusDetail, statusView, tiersLine } from '../aiModels'
import { settingsSummary } from '../settingsSummary'
import { t } from '../../../i18n'
import { renderSettings } from './settingsHarness'

const base: Values = {
  quality: 1080, voice: 'dub', remoteQuality: '1080',
  remoteMusic: 'high', autoSubtitles: true, subtitleLangs: ['ru'], subtitlesAvailable: false,
}
const json = (b: unknown) => new Response(JSON.stringify(b), { status: 200, headers: { 'Content-Type': 'application/json' } })
const ALL = ['light', 'medium', 'powerful'] as const
const at = new Date(Date.now() - 5 * 60_000).toISOString()

const lite: AIModel = { id: 'a1', url: 'http://litellm:4000/v1', model: 'gemini-flash-lite', keySet: false, tiers: [...ALL], status: { state: 'ok', ms: 420, at, source: 'check' } }
const groq: AIModel = { id: 'b2', url: 'https://api.groq.com/openai/v1', model: 'openai/gpt-oss-120b', keySet: true, tiers: [...ALL], status: { state: 'error', kind: 'http', code: 401, error: '401 Unauthorized {"error":"bad key"}', at, source: 'task' } }
const claude: AIModel = { id: 'c3', url: 'https://api.anthropic.com', model: 'claude-haiku-5-5', keySet: false, tiers: ['medium', 'powerful'], status: { state: 'noKey' } }
const gemma: AIModel = { id: 'd4', url: 'http://localhost:11434', model: 'local-gemma', keySet: false, tiers: ['light'], status: { state: 'unchecked' } }
const server: AIModel = { id: 'env', env: true, url: 'http://litellm:4000/v1', model: 'srv-model', modelDefault: 'srv-model', keySet: true, tiers: [...ALL], status: { state: 'unchecked' } }

const whisper: AIModel = { id: 's1', kind: 'speech', url: 'https://api.groq.com/openai/v1', model: 'whisper-large-v3', keySet: true, tiers: [...ALL], status: { state: 'ok', ms: 900, at, source: 'check' } }
const speechServer: AIModel = { id: 'env-stt', kind: 'speech', env: true, url: 'https://api.groq.com/openai/v1', model: 'whisper-large-v3-turbo', modelDefault: 'whisper-large-v3-turbo', keySet: true, tiers: [...ALL], status: { state: 'unchecked' } }

const withAI = (ai: AISettings, extra: Partial<Values> = {}): Values => ({ ...base, ai, ...extra })

/** The server as a test double: it keeps the list, gives new entries ids, drops keys. */
function stub(start: Values, checks: Record<string, AICheck['status']> = {}) {
  let cur = start
  const puts: Values[] = []
  const tests: { ids?: string[]; entry?: AIModel }[] = []
  const lists: { url?: string; key?: string; id?: string; serverKey?: boolean; kind?: string }[] = []
  let models: { models: { id: string }[]; error?: string } = { models: [] }
  let n = 0
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      const body = init?.body ? JSON.parse(String(init.body)) : undefined
      if (url.endsWith('/api/settings/ai/test')) {
        tests.push(body)
        if (body.entry) return json({ ok: true, results: [{ id: body.entry.id, ok: true, ms: 480, status: checks[body.entry.id] ?? { state: 'ok', ms: 480, at: new Date().toISOString(), source: 'check' } }] })
        const ids: string[] = body.ids ?? (cur.ai?.list ?? []).filter((e) => !e.off).map((e) => e.id)
        const results = ids.map((id) => ({ id, ok: true, ms: 480, status: checks[id] ?? { state: 'ok' as const, ms: 480, at: new Date().toISOString(), source: 'check' as const } }))
        cur = { ...cur, ai: { ...cur.ai!, list: cur.ai!.list.map((e) => ({ ...e, status: results.find((r) => r.id === e.id)?.status ?? e.status })) } }
        return json({ ok: true, results })
      }
      if (url.endsWith('/api/settings/ai/models')) {
        lists.push(body)
        return json(models)
      }
      if (url.endsWith('/api/settings') && init?.method === 'PUT') {
        const b = body as Values
        puts.push(b)
        const old = new Map((cur.ai?.list ?? []).map((e) => [e.id, e]))
        const list = (b.ai?.list ?? []).map((e) => ({ ...e, id: e.id || `n${++n}`, key: undefined, keySet: !!e.key || (old.get(e.id)?.keySet ?? false), status: old.get(e.id)?.status ?? { state: 'unchecked' as const } }))
        cur = { ...b, ai: { ...b.ai!, list } }
        return json(cur)
      }
      if (url.endsWith('/api/settings')) return json(cur)
      return json({})
    }),
  )
  return { puts, tests, lists, setModels: (m: typeof models) => (models = m), current: () => cur }
}

afterEach(() => vi.unstubAllGlobals())

const rows = () => screen.getAllByRole('button').filter((b) => b.closest('li') && /·/.test(b.textContent ?? '') && !b.getAttribute('aria-label'))

describe('«ИИ-помощник»: the list', () => {
  it('a row a model: its name, «провайдер · адрес · задачи», its status before the chevron', async () => {
    stub(withAI({ list: [lite, groq, claude, gemma, server] }))
    renderSettings('ai')
    const row = (name: string) => screen.getByRole('button', { name: new RegExp(`^${name.replace(/[/.]/g, '\\$&')}`) })
    expect(await screen.findByRole('switch', { name: 'ИИ-помощник' })).toBeChecked()
    expect(row('gemini-flash-lite')).toHaveTextContent('LiteLLM · litellm:4000')
    expect(row('gemini-flash-lite')).toHaveTextContent('420 мс')
    expect(within(row('gemini-flash-lite')).getByText('420 мс')).toHaveAttribute('data-tone', 'success')
    expect(row('openai/gpt-oss-120b')).toHaveTextContent('Groq · api.groq.com')
    expect(within(row('openai/gpt-oss-120b')).getByText('Ошибка 401')).toHaveAttribute('data-tone', 'destructive')
    expect(row('claude-haiku-5-5')).toHaveTextContent('Anthropic · api.anthropic.com Задачи: средние, сложные')
    expect(within(row('claude-haiku-5-5')).getByText('Нет ключа')).toHaveAttribute('data-tone', 'warning')
    expect(row('local-gemma')).toHaveTextContent('Ollama · localhost:11434 Задачи: лёгкие')
    expect(within(row('local-gemma')).getByText('Не проверена')).toHaveAttribute('data-tone', 'neutral')
    expect(row('srv-model')).toHaveTextContent('LiteLLM · с сервера')
    expect(screen.getByRole('button', { name: 'Добавить модель…' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /^Проверить все/ })).toHaveTextContent('Проверено 5 мин назад: 1 работает, 1 с ошибкой, 1 без ключа, 2 не проверено')
  })

  it('the assistant switch saves «off»; the models stay', async () => {
    const { puts } = stub(withAI({ list: [lite] }))
    renderSettings('ai')
    await userEvent.click(await screen.findByRole('switch', { name: 'ИИ-помощник' }))
    await waitFor(() => expect(puts.at(-1)?.ai).toMatchObject({ off: true, list: [{ id: 'a1' }] }))
    expect(puts.at(-1)?.ai?.list[0]).not.toHaveProperty('key')
  })

  it('«Проверить все» asks every model that is on and says what it found', async () => {
    const { tests } = stub(withAI({ list: [lite, { ...groq, status: { state: 'unchecked' } }] }), { b2: { state: 'error', kind: 'timeout', error: 'timed out', at: new Date().toISOString(), source: 'check' } })
    renderSettings('ai')
    await userEvent.click(await screen.findByRole('button', { name: /^Проверить все/ }))
    await waitFor(() => expect(tests.at(-1)).toEqual({}))
    expect(await screen.findByText('Проверено только что: 1 работает, 1 с ошибкой')).toBeInTheDocument()
    expect(within(screen.getByRole('button', { name: /^openai\/gpt-oss-120b/ })).getByText('Нет ответа')).toBeInTheDocument()
  })

  it('the row opens its sheet: the full error, when and how it was learnt, the tasks', async () => {
    stub(withAI({ list: [lite, groq] }))
    renderSettings('ai')
    await userEvent.click(await screen.findByRole('button', { name: /^openai\/gpt-oss-120b/ }))
    const sheet = await screen.findByRole('dialog', { name: 'openai/gpt-oss-120b' })
    expect(within(sheet).getByText(/401 Unauthorized .*bad key/)).toHaveTextContent('· Обновлено 5 мин назад')
    expect(within(sheet).getByLabelText('Ключ API')).toHaveAttribute('placeholder', 'Сохранён')
    expect(within(sheet).getByRole('switch', { name: 'Использовать' })).toBeChecked()
    expect(within(sheet).getByRole('switch', { name: 'Сложные задачи' })).toBeChecked()
    expect(within(sheet).getByRole('button', { name: 'Удалить' })).toBeInTheDocument()
    expect(within(sheet).getByRole('button', { name: 'Готово' })).toBeInTheDocument()
  })

  it('the server’s own model: address and key read-only, can be switched off, cannot be deleted', async () => {
    const { puts } = stub(withAI({ list: [server] }))
    renderSettings('ai')
    await userEvent.click(await screen.findByRole('button', { name: /^srv-model/ }))
    const sheet = await screen.findByRole('dialog', { name: 'srv-model' })
    expect(within(sheet).queryByLabelText('Адрес')).toBeNull()
    expect(within(sheet).queryByLabelText('Ключ API')).toBeNull()
    expect(within(sheet).getByText('LiteLLM или свой сервер'.slice(0, 7))).toBeInTheDocument()
    expect(within(sheet).queryByRole('button', { name: 'Удалить' })).toBeNull()
    expect(within(sheet).getByText(/VAULT_LLM_\*/)).toHaveTextContent('по умолчанию — srv-model')
    await userEvent.click(within(sheet).getByRole('switch', { name: 'Использовать' }))
    await userEvent.click(within(sheet).getByRole('button', { name: 'Сохранить' }))
    await waitFor(() => expect(puts.at(-1)?.ai?.list[0]).toMatchObject({ id: 'env', env: true, off: true, model: 'srv-model' }))
  })

  it('the server’s own model is a LiteLLM among the others: its model is chosen from the endpoint’s list', async () => {
    const { puts, lists, setModels } = stub(withAI({ list: [server, lite] }))
    setModels({ models: [{ id: 'auto' }, { id: 'alt-model' }, { id: 'srv-model' }] })
    renderSettings('ai')
    // «LiteLLM · с сервера» under the name, the address is not shown
    expect(await screen.findByRole('button', { name: /^srv-model/ })).toHaveTextContent('LiteLLM · с сервера')
    await userEvent.click(screen.getByRole('button', { name: /^srv-model/ }))
    const sheet = await screen.findByRole('dialog', { name: 'srv-model' })
    await waitFor(() => expect(lists.at(-1)).toMatchObject({ id: 'env', url: 'http://litellm:4000/v1' }))
    expect(lists.at(-1)).not.toHaveProperty('key')
    await userEvent.click(await within(sheet).findByRole('button', { name: /^Модель/ }))
    expect(screen.getAllByRole('menuitemradio').map((m) => m.textContent)).toEqual(['auto', 'alt-model', 'srv-model', 'Другая…'])
    await userEvent.click(screen.getByRole('menuitemradio', { name: 'alt-model' }))
    await userEvent.click(within(sheet).getByRole('button', { name: 'Сохранить' }))
    await waitFor(() => expect(puts.at(-1)?.ai?.list[0]).toMatchObject({ id: 'env', env: true, model: 'alt-model' }))
    expect(puts.at(-1)?.ai?.list[0]).not.toHaveProperty('key')
  })

  it('the server’s own model: «Другая…» is a name typed; an endpoint that lists nothing asks for one', async () => {
    const { puts } = stub(withAI({ list: [server] }))
    renderSettings('ai')
    await userEvent.click(await screen.findByRole('button', { name: /^srv-model/ }))
    const sheet = await screen.findByRole('dialog', { name: 'srv-model' })
    const name = await within(sheet).findByLabelText('Название модели')
    await userEvent.clear(name)
    await userEvent.type(name, 'typed-model')
    await userEvent.click(within(sheet).getByRole('button', { name: 'Сохранить' }))
    await waitFor(() => expect(puts.at(-1)?.ai?.list[0]).toMatchObject({ env: true, model: 'typed-model' }))
  })
})

describe('«ИИ-помощник»: the server’s LiteLLM when adding', () => {
  it('a LiteLLM starts from the server’s address and takes its key: no key field, a note, models found with it', async () => {
    const { puts, lists, setModels } = stub(withAI({ list: [server] }))
    setModels({ models: [{ id: 'auto' }, { id: 'groq-gpt-oss' }] })
    renderSettings('ai')
    await userEvent.click(await screen.findByRole('button', { name: 'Добавить модель…' }))
    const sheet = await screen.findByRole('dialog', { name: 'Новая модель' })
    expect(within(sheet).getByLabelText('Адрес')).toHaveValue('http://litellm:4000/v1')
    expect(within(sheet).queryByLabelText('Ключ API')).toBeNull()
    expect(within(sheet).getByText(/у самого сервера/)).toBeInTheDocument()
    await waitFor(() => expect(lists.at(-1)).toMatchObject({ url: 'http://litellm:4000/v1', serverKey: true }))
    expect(lists.at(-1)).not.toHaveProperty('key')
    await userEvent.click(await within(sheet).findByRole('button', { name: /^Модель/ }))
    await userEvent.click(screen.getByRole('menuitemradio', { name: 'groq-gpt-oss' }))
    await userEvent.click(within(sheet).getByRole('button', { name: 'Сохранить' }))
    await waitFor(() => expect(puts.at(-1)?.ai?.list.at(-1)).toMatchObject({ url: 'http://litellm:4000/v1', model: 'groq-gpt-oss', keyFromEnv: true }))
    expect(puts.at(-1)?.ai?.list.at(-1)).not.toHaveProperty('key')
  })

  it('another address wants a key of its own; another provider too', async () => {
    const { lists } = stub(withAI({ list: [server] }))
    renderSettings('ai')
    await userEvent.click(await screen.findByRole('button', { name: 'Добавить модель…' }))
    const sheet = await screen.findByRole('dialog', { name: 'Новая модель' })
    const url = within(sheet).getByLabelText('Адрес')
    await userEvent.clear(url)
    await userEvent.type(url, 'http://other:4000/v1')
    expect(await within(sheet).findByLabelText('Ключ API')).toBeInTheDocument()
    expect(within(sheet).queryByText(/у самого сервера/)).toBeNull()
    await userEvent.clear(url)
    await userEvent.type(url, 'http://litellm:4000/v1')
    expect(within(sheet).queryByLabelText('Ключ API')).toBeNull()
    await userEvent.click(within(sheet).getByRole('button', { name: /^Провайдер/ }))
    await userEvent.click(screen.getByRole('menuitemradio', { name: 'Groq' }))
    expect(within(sheet).getByLabelText('Адрес')).toHaveValue('https://api.groq.com/openai/v1')
    expect(within(sheet).getByLabelText('Ключ API')).toBeInTheDocument()
    expect(lists.every((l) => !l.serverKey || l.url === 'http://litellm:4000/v1')).toBe(true)
  })

  it('without a LiteLLM of the server’s own, the address is the last one used and the key is typed', async () => {
    stub(withAI({ list: [lite] }))
    renderSettings('ai')
    await userEvent.click(await screen.findByRole('button', { name: 'Добавить модель…' }))
    const sheet = await screen.findByRole('dialog', { name: 'Новая модель' })
    expect(within(sheet).getByLabelText('Адрес')).toHaveValue('http://litellm:4000/v1')
    expect(within(sheet).getByLabelText('Ключ API')).toBeInTheDocument()
  })

  it('a direct provider’s models are listed once its key is typed', async () => {
    const { lists, setModels, puts } = stub(withAI({ list: [] }))
    setModels({ models: [{ id: 'claude-opus-5-5' }, { id: 'claude-haiku-5-5' }] })
    renderSettings('ai')
    await userEvent.click(await screen.findByRole('button', { name: 'Добавить модель…' }))
    const sheet = await screen.findByRole('dialog', { name: 'Новая модель' })
    await userEvent.click(within(sheet).getByRole('button', { name: /^Провайдер/ }))
    await userEvent.click(screen.getByRole('menuitemradio', { name: 'Anthropic' }))
    // no key, nothing asked
    expect(lists.filter((l) => l.url === 'https://api.anthropic.com')).toHaveLength(0)
    await userEvent.type(within(sheet).getByLabelText('Ключ API'), 'sk-ant-1')
    await userEvent.tab()
    await waitFor(() => expect(lists.at(-1), JSON.stringify(lists)).toMatchObject({ url: 'https://api.anthropic.com', key: 'sk-ant-1' }), { timeout: 3000 })
    await userEvent.click(await within(sheet).findByRole('button', { name: /^Модель/ }))
    await userEvent.click(screen.getByRole('menuitemradio', { name: 'claude-haiku-5-5' }))
    await userEvent.click(within(sheet).getByRole('button', { name: 'Сохранить' }))
    await waitFor(() => expect(puts.at(-1)?.ai?.list[0]).toMatchObject({ url: 'https://api.anthropic.com', model: 'claude-haiku-5-5', key: 'sk-ant-1' }))
  })
})

describe('«ИИ-помощник»: «Функции ИИ»', () => {
  const FEATURES = ['Описание канала', 'Порядок франшиз', 'Категории IPTV-каналов', 'Копии IPTV-каналов', 'Названия книг на других языках', 'Главы аудиокниг', 'Расшифровка аудиокниг']

  it('every feature is a row with a switch and a line under it; all on by default', async () => {
    stub(withAI({ list: [lite] }))
    renderSettings('ai')
    const group = (await screen.findByRole('heading', { name: 'Функции ИИ' })).closest('section')!
    const names = within(group).getAllByRole('switch').map((b) => b.getAttribute('aria-label'))
    expect(names).toEqual(FEATURES)
    for (const sw of within(group).getAllByRole('switch')) expect(sw).toBeChecked()
    expect(within(group).getByText(/советские комедии 70-х/)).toBeInTheDocument()
    expect(within(group).getByText('Выключенная функция не обращается к модели и работает без ИИ.')).toBeInTheDocument()
  })

  it('a switch saves at once; only what is off is kept', async () => {
    const { puts } = stub(withAI({ list: [lite] }))
    renderSettings('ai')
    await userEvent.click(await screen.findByRole('switch', { name: 'Главы аудиокниг' }))
    await waitFor(() => expect(puts.at(-1)?.ai).toMatchObject({ features: { chapters: false }, list: [{ id: 'a1' }] }))
    expect(screen.getByRole('switch', { name: 'Главы аудиокниг' })).not.toBeChecked()
    await userEvent.click(screen.getByRole('switch', { name: 'Расшифровка аудиокниг' }))
    await waitFor(() => expect(puts.at(-1)?.ai?.features).toEqual({ chapters: false, transcripts: false }))
    await userEvent.click(screen.getByRole('switch', { name: 'Главы аудиокниг' }))
    await waitFor(() => expect(puts.at(-1)?.ai?.features).toEqual({ transcripts: false }))
  })

  it('stored switches show; with the assistant off the list is disabled and says why', async () => {
    stub(withAI({ off: true, list: [lite], features: { describe: false } }))
    renderSettings('ai')
    const group = (await screen.findByRole('heading', { name: 'Функции ИИ' })).closest('section')!
    expect(within(group).getByRole('switch', { name: 'Описание канала' })).not.toBeChecked()
    for (const sw of within(group).getAllByRole('switch')) expect(sw).toBeDisabled()
    expect(within(group).getByText(/Пока ИИ-помощник выключен/)).toBeInTheDocument()
  })

  it('the master switch keeps the features it did not touch', async () => {
    const { puts } = stub(withAI({ list: [lite], features: { describe: false } }))
    renderSettings('ai')
    await userEvent.click(await screen.findByRole('switch', { name: 'ИИ-помощник' }))
    await waitFor(() => expect(puts.at(-1)?.ai).toMatchObject({ off: true, features: { describe: false } }))
  })

  it('the ids are the server’s: every feature the page lists has its words in both languages', async () => {
    const { AI_FEATURES } = await import('../aiFeatureList')
    const en = (await import('../../../i18n/en/manageSettings')).default as Record<string, unknown>
    const ru = (await import('../../../i18n/ru/manageSettings')).default as Record<string, unknown>
    for (const id of AI_FEATURES) {
      for (const k of [`settings.ai.feat.${id}`, `settings.ai.feat.${id}.hint`]) {
        expect(typeof en[k], `en ${k}`).toBe('string')
        expect(typeof ru[k], `ru ${k}`).toBe('string')
      }
    }
  })
})

describe('«ИИ-помощник»: a model’s sheet', () => {
  it('Enter in the key field saves the model with its key and checks it; the sheet stays with the answer', async () => {
    const { puts, tests } = stub(withAI({ list: [] }))
    renderSettings('ai')
    await userEvent.click(await screen.findByRole('button', { name: 'Добавить модель…' }))
    const sheet = await screen.findByRole('dialog', { name: 'Новая модель' })
    await userEvent.click(within(sheet).getByRole('button', { name: /^Провайдер/ }))
    await userEvent.click(screen.getByRole('menuitemradio', { name: 'Groq' }))
    expect(within(sheet).getByLabelText('Адрес')).toHaveValue('https://api.groq.com/openai/v1')
    expect(within(sheet).getByLabelText('Название модели')).toHaveValue('openai/gpt-oss-120b')
    // no key, no save
    expect(within(sheet).getByRole('button', { name: 'Сохранить' })).toBeDisabled()
    await userEvent.type(within(sheet).getByLabelText('Ключ API'), 'gsk-secret{Enter}')
    await waitFor(() => expect(puts.at(-1)?.ai?.list).toMatchObject([{ id: '', url: 'https://api.groq.com/openai/v1', model: 'openai/gpt-oss-120b', key: 'gsk-secret', tiers: ['light', 'medium', 'powerful'] }]))
    await waitFor(() => expect(tests.at(-1)).toEqual({ ids: ['n1'] }))
    expect(await within(sheet).findByText(/Работает: ответ за 480 мс/)).toBeInTheDocument()
    // still open, the key left the field and is «Сохранён», the title is the model's now
    expect(screen.getByRole('dialog', { name: 'openai/gpt-oss-120b' })).toBeInTheDocument()
    expect(within(sheet).getByLabelText('Ключ API')).toHaveValue('')
    expect(within(sheet).getByLabelText('Ключ API')).toHaveAttribute('placeholder', 'Сохранён')
    // a second Enter checks again, it does not add a second model
    await userEvent.type(within(sheet).getByLabelText('Ключ API'), '{Enter}')
    await waitFor(() => expect(tests.filter((x) => x.ids?.[0] === 'n1').length).toBeGreaterThan(1))
    expect(puts).toHaveLength(1)
  })

  it('«Сохранить» saves, closes and checks only that model', async () => {
    const { puts, tests } = stub(withAI({ list: [lite] }))
    renderSettings('ai')
    await userEvent.click(await screen.findByRole('button', { name: 'Добавить модель…' }))
    const sheet = await screen.findByRole('dialog', { name: 'Новая модель' })
    await userEvent.clear(within(sheet).getByLabelText('Адрес'))
    await userEvent.type(within(sheet).getByLabelText('Адрес'), 'http://litellm:4000/v1')
    await userEvent.type(within(sheet).getByLabelText('Название модели'), 'groq-gpt-oss')
    await userEvent.click(within(sheet).getByRole('switch', { name: 'Сложные задачи' }))
    await userEvent.click(within(sheet).getByRole('button', { name: 'Сохранить' }))
    await waitFor(() => expect(puts.at(-1)?.ai?.list).toMatchObject([{ id: 'a1' }, { id: '', url: 'http://litellm:4000/v1', model: 'groq-gpt-oss', tiers: ['light', 'medium'] }]))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    await waitFor(() => expect(tests.at(-1)).toEqual({ ids: ['n1'] }))
    expect(await screen.findByRole('button', { name: /^groq-gpt-oss/ })).toHaveTextContent('Задачи: лёгкие, средние')
  })

  it('the last task cannot be switched off', async () => {
    stub(withAI({ list: [{ ...gemma }] }))
    renderSettings('ai')
    await userEvent.click(await screen.findByRole('button', { name: /^local-gemma/ }))
    const sheet = await screen.findByRole('dialog', { name: 'local-gemma' })
    expect(within(sheet).getByRole('switch', { name: 'Лёгкие задачи' })).toBeDisabled()
    expect(within(sheet).getByRole('switch', { name: 'Сложные задачи' })).not.toBeChecked()
    expect(within(sheet).queryByLabelText('Ключ API')).toBeNull()
  })

  it('«Проверить» of an edited model asks it as typed and saves nothing; of a saved one asks it by id', async () => {
    const { puts, tests } = stub(withAI({ list: [groq] }))
    renderSettings('ai')
    await userEvent.click(await screen.findByRole('button', { name: /^openai\/gpt-oss-120b/ }))
    const sheet = await screen.findByRole('dialog', { name: 'openai/gpt-oss-120b' })
    await userEvent.click(within(sheet).getByRole('button', { name: 'Проверить' }))
    await waitFor(() => expect(tests.at(-1)).toEqual({ ids: ['b2'] }))
    await userEvent.type(within(sheet).getByLabelText('Ключ API'), 'gsk-new')
    await userEvent.click(within(sheet).getByRole('button', { name: 'Проверить' }))
    await waitFor(() => expect(tests.at(-1)?.entry).toMatchObject({ id: 'b2', model: 'openai/gpt-oss-120b', key: 'gsk-new' }))
    expect(puts).toHaveLength(0)
  })

  it('the models the endpoint lists are a pop-up; «Другая…» is a name typed', async () => {
    const { lists, setModels, puts } = stub(withAI({ list: [] }))
    setModels({ models: [{ id: 'auto' }, { id: 'groq-gpt-oss' }, { id: 'pin-b' }] })
    renderSettings('ai')
    await userEvent.click(await screen.findByRole('button', { name: 'Добавить модель…' }))
    const sheet = await screen.findByRole('dialog', { name: 'Новая модель' })
    await waitFor(() => expect(lists.at(-1)).toMatchObject({ url: 'http://127.0.0.1:4000/v1' }))
    await userEvent.click(await within(sheet).findByRole('button', { name: /^Модель/ }))
    await userEvent.click(screen.getByRole('menuitemradio', { name: 'groq-gpt-oss' }))
    expect(within(sheet).queryByLabelText('Название модели')).toBeNull()
    await userEvent.click(within(sheet).getByRole('button', { name: /^Модель/ }))
    await userEvent.click(screen.getByRole('menuitemradio', { name: 'Другая…' }))
    await userEvent.type(within(sheet).getByLabelText('Название модели'), '2')
    await userEvent.click(within(sheet).getByRole('button', { name: 'Сохранить' }))
    await waitFor(() => expect(puts.at(-1)?.ai?.list[0]).toMatchObject({ model: 'groq-gpt-oss2' }))
  })

  it('«Удалить» asks first, then the model leaves the list', async () => {
    const { puts } = stub(withAI({ list: [lite, groq] }))
    renderSettings('ai')
    await userEvent.click(await screen.findByRole('button', { name: /^openai\/gpt-oss-120b/ }))
    const sheet = await screen.findByRole('dialog', { name: 'openai/gpt-oss-120b' })
    await userEvent.click(within(sheet).getByRole('button', { name: 'Удалить' }))
    const ask = await screen.findByRole('alertdialog', { name: 'Удалить «openai/gpt-oss-120b»?' })
    expect(puts).toHaveLength(0)
    await userEvent.click(within(ask).getByRole('button', { name: 'Удалить' }))
    await waitFor(() => expect(puts.at(-1)?.ai?.list.map((e) => e.id)).toEqual(['a1']))
    expect(screen.queryByRole('dialog')).toBeNull()
  })
})

describe('«ИИ-помощник»: «Изменить»', () => {
  it('the same rows become edit rows: − and ≡ inline, the order saves at once, the server’s model has no −', async () => {
    const { puts } = stub(withAI({ list: [lite, groq, server] }))
    renderSettings('ai')
    await userEvent.click(await screen.findByRole('button', { name: 'Изменить' }))
    expect(screen.getByRole('button', { name: 'Готово' })).toBeInTheDocument()
    // no list inside the group: the rows are the group's own
    const group = screen.getByRole('button', { name: 'Добавить модель…' }).closest('ul')!
    expect(group.querySelectorAll(':scope > li[data-value]')).toHaveLength(3)
    expect(group.querySelector('ol')).toBeNull()
    expect(screen.getByRole('button', { name: 'Убрать: gemini-flash-lite' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Убрать: srv-model' })).toBeNull()
    expect(screen.getByRole('button', { name: 'Переместить: srv-model' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^Проверить все/ })).toBeInTheDocument()
    // the detail line is the provider only in edit mode
    expect(within(group).getAllByText('Groq')).toHaveLength(1)
    screen.getByRole('button', { name: 'Переместить: openai/gpt-oss-120b' }).focus()
    await userEvent.keyboard('{ArrowUp}')
    await waitFor(() => expect(puts.at(-1)?.ai?.list.map((e) => e.id)).toEqual(['b2', 'a1', 'env']))
    expect(screen.getByText(/Перетащите модель за ≡/)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Готово' }))
    expect(screen.getByRole('button', { name: 'Изменить' })).toBeInTheDocument()
    expect(rows().map((b) => b.textContent)[0]).toMatch(/^openai\/gpt-oss-120b/)
  })

  it('− asks first; the model leaves with its key', async () => {
    const { puts } = stub(withAI({ list: [lite, groq] }))
    renderSettings('ai')
    await userEvent.click(await screen.findByRole('button', { name: 'Изменить' }))
    await userEvent.click(screen.getByRole('button', { name: 'Убрать: gemini-flash-lite' }))
    const ask = await screen.findByRole('alertdialog', { name: 'Удалить «gemini-flash-lite»?' })
    expect(puts).toHaveLength(0)
    await userEvent.click(within(ask).getByRole('button', { name: 'Удалить' }))
    await waitFor(() => expect(puts.at(-1)?.ai?.list.map((e) => e.id)).toEqual(['b2']))
  })

  it('no «Изменить» without models', async () => {
    stub(withAI({ list: [] }))
    renderSettings('ai')
    await screen.findByRole('switch', { name: 'ИИ-помощник' })
    expect(screen.queryByRole('button', { name: 'Изменить' })).toBeNull()
    expect(screen.queryByRole('button', { name: /^Проверить все/ })).toBeNull()
  })
})

describe('«ИИ-помощник»: the words', () => {
  it('the summary of «Управление»: the first model that is on, else «Выключено»', () => {
    expect(settingsSummary('ai', withAI({ list: [lite, groq] }))).toBe('gemini-flash-lite')
    expect(settingsSummary('ai', withAI({ list: [{ ...lite, off: true }, groq] }))).toBe('openai/gpt-oss-120b')
    expect(settingsSummary('ai', withAI({ list: [claude] }))).toBe('Выключено')
    expect(settingsSummary('ai', withAI({ off: true, list: [lite] }))).toBe('Выключено')
    expect(settingsSummary('ai', base)).toBe('Выключено')
  })

  it('providerOf and the status words', () => {
    expect(providerOf('https://api.groq.com/openai/v1')).toBe('groq')
    expect(providerOf('http://127.0.0.1:4000/v1')).toBe('litellm')
    expect(providerOf('http://host:11434')).toBe('ollama')
    expect(providerOf('https://api.anthropic.com')).toBe('anthropic')
    expect(statusView(t, { state: 'error', kind: 'answer' })).toEqual({ text: 'Сбой ответа', tone: 'destructive' })
    expect(statusView(t, { state: 'error', kind: 'timeout' })).toEqual({ text: 'Нет ответа', tone: 'destructive' })
    expect(statusView(t, { state: 'error', kind: 'http', code: 429 })).toEqual({ text: 'Ошибка 429', tone: 'destructive' })
    expect(statusView(t, undefined)).toEqual({ text: 'Не проверена', tone: 'neutral' })
    expect(statusDetail(t, { state: 'error', kind: 'network', error: 'connection refused' })).toBe('Адрес недоступен: connection refused')
    expect(entryHint(t, gemma)).toBe('Ollama\u00A0·\u00A0localhost:11434')
    expect(tiersLine(t, { ...gemma, tiers: ['light', 'powerful'] })).toBe('Задачи: лёгкие, сложные')
    expect(tiersLine(t, lite)).toBe('')
  })
})

describe('«ИИ-помощник»: speech recognition in the list', () => {
  it('the text models, then the speech ones; a speech row says so, the server’s own is «Groq · с сервера»', async () => {
    stub(withAI({ list: [whisper, lite, speechServer, groq] }))
    renderSettings('ai')
    await screen.findByRole('switch', { name: 'ИИ-помощник' })
    // one group, the kinds in blocks (the list is stored in any order)
    expect(rows().map((b) => b.textContent?.split('Распознавание')[0]?.slice(0, 12))).toEqual(['gemini-flash', 'openai/gpt-o', 'whisper-larg', 'whisper-larg'])
    const row = (name: string) => screen.getByRole('button', { name: new RegExp(`^${name}(?![-\\w])`) })
    expect(row('whisper-large-v3')).toHaveTextContent('Распознавание речи · Groq · api.groq.com')
    expect(row('whisper-large-v3')).not.toHaveTextContent('Задачи')
    expect(row('whisper-large-v3-turbo')).toHaveTextContent('Распознавание речи · Groq · с сервера')
    expect(within(row('whisper-large-v3')).getByText('900 мс')).toHaveAttribute('data-tone', 'success')
    // the old section is gone
    expect(screen.queryByRole('heading', { name: 'Распознавание речи' })).toBeNull()
    // the first model of «Управление»'s summary is a text one
    expect(settingsSummary('ai', withAI({ list: [whisper, lite] }))).toBe('gemini-flash-lite')
  })

  it('«Расшифровка аудиокниг» warns while there is no speech model that can be asked', async () => {
    const { puts } = stub(withAI({ list: [lite, { ...speechServer, off: true, status: { state: 'off' } }] }))
    renderSettings('ai')
    const warn = 'Нет модели распознавания речи — добавьте её в «Модели»'
    const unusable = 'Ни одна модель распознавания речи не может работать: включите её или добавьте ключ в «Модели»'
    // some, but all off: it says what to do
    const note = await screen.findByText(unusable)
    expect(note).toHaveAttribute('role', 'status')
    expect(note.closest('li')).toHaveTextContent('Расшифровка аудиокниг')
    // a model without its key cannot be asked either
    cleanup()
    stub(withAI({ list: [lite, { ...whisper, keySet: false, status: { state: 'noKey' } }] }))
    renderSettings('ai')
    expect(await screen.findByText(unusable)).toBeInTheDocument()
    // a text model is no speech model
    cleanup()
    stub(withAI({ list: [lite] }))
    renderSettings('ai')
    expect(await screen.findByText(warn)).toBeInTheDocument()
    // one that works, or one not checked yet: no warning
    cleanup()
    stub(withAI({ list: [lite, whisper] }))
    renderSettings('ai')
    await screen.findByRole('switch', { name: 'Расшифровка аудиокниг' })
    expect(screen.queryByText(warn)).toBeNull()
    expect(screen.queryByText(unusable)).toBeNull()
    cleanup()
    // the feature switched off: nothing to warn about
    stub(withAI({ list: [lite], features: { transcripts: false } }))
    renderSettings('ai')
    await screen.findByRole('switch', { name: 'Расшифровка аудиокниг' })
    expect(screen.queryByText(warn)).toBeNull()
    expect(puts).toHaveLength(0)
  })

  it('the assistant off: the models are dimmed and disabled like the features', async () => {
    stub(withAI({ off: true, list: [lite, whisper] }))
    renderSettings('ai')
    await screen.findByRole('switch', { name: 'ИИ-помощник' })
    for (const name of [/^gemini-flash-lite/, /^whisper-large-v3/]) expect(screen.getByRole('button', { name })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Добавить модель…' })).toBeDisabled()
    expect(screen.getByRole('button', { name: /^Проверить все/ })).toBeDisabled()
    expect(screen.getByRole('switch', { name: 'Расшифровка аудиокниг' })).toBeDisabled()
    expect(screen.getByText(/Пока ИИ-помощник выключен, ни одна функция/)).toBeInTheDocument()
    // dimmed: the group of the models and the features' own
    expect(screen.getByRole('button', { name: /^gemini-flash-lite/ }).closest('section')?.className).toMatch(/dimmed/)
    expect(screen.getByRole('switch', { name: 'Расшифровка аудиокниг' }).closest('section')?.className).toMatch(/dimmed/)
    // no warning while everything is off, and no «Изменить»
    expect(screen.queryByText(/Нет модели распознавания речи|Ни одна модель распознавания/)).toBeNull()
    // «Изменить» stays in its place, disabled
    expect(screen.getByRole('button', { name: 'Изменить' })).toBeDisabled()
  })

  it('«Добавить модель…» asks the kind first; speech starts from Groq, has no tasks and lists speech models only', async () => {
    const { puts, lists, setModels } = stub(withAI({ list: [lite] }))
    setModels({ models: [{ id: 'whisper-large-v3' }, { id: 'whisper-large-v3-turbo' }] })
    renderSettings('ai')
    await userEvent.click(await screen.findByRole('button', { name: 'Добавить модель…' }))
    const sheet = await screen.findByRole('dialog', { name: 'Новая модель' })
    const kind = within(sheet).getByRole('radiogroup', { name: 'Тип' })
    expect(within(kind).getByRole('radio', { name: /Текст/ })).toBeChecked()
    expect(within(sheet).getByRole('switch', { name: 'Сложные задачи' })).toBeInTheDocument()
    expect(within(sheet).getByLabelText('Адрес')).toHaveValue('http://litellm:4000/v1')
    await userEvent.click(within(kind).getByRole('radio', { name: /Речь|Распознавание речи/ }))
    expect(within(sheet).queryByRole('switch', { name: 'Сложные задачи' })).toBeNull()
    expect(within(sheet).getByText(/Расшифровывает аудиокниги/)).toBeInTheDocument()
    expect(within(sheet).getByLabelText('Адрес')).toHaveValue('https://api.groq.com/openai/v1')
    await userEvent.type(within(sheet).getByLabelText('Ключ API'), 'gsk-1{Tab}')
    await waitFor(() => expect(lists.at(-1)).toMatchObject({ url: 'https://api.groq.com/openai/v1', key: 'gsk-1', kind: 'speech' }), { timeout: 3000 })
    expect(await within(sheet).findByRole('button', { name: /^Модель/ })).toHaveTextContent('whisper-large-v3-turbo')
    await userEvent.click(within(sheet).getByRole('button', { name: 'Сохранить' }))
    await waitFor(() => expect(puts.at(-1)?.ai?.list.map((e) => [e.model, e.kind])).toEqual([['gemini-flash-lite', undefined], ['whisper-large-v3-turbo', 'speech']]))
    expect(puts.at(-1)?.ai?.list[1]).toMatchObject({ url: 'https://api.groq.com/openai/v1', key: 'gsk-1' })
    // the new entry lands among the speech ones, marked
    expect(await screen.findByRole('button', { name: /^whisper-large-v3-turbo/ })).toHaveTextContent('Распознавание речи · Groq')
  })

  it('a text entry is added from the same sheet without a kind; switching the kind starts the provider afresh', async () => {
    const { puts, lists } = stub(withAI({ list: [] }))
    renderSettings('ai')
    await userEvent.click(await screen.findByRole('button', { name: 'Добавить модель…' }))
    const sheet = await screen.findByRole('dialog', { name: 'Новая модель' })
    const kind = within(sheet).getByRole('radiogroup', { name: 'Тип' })
    await userEvent.click(within(kind).getByRole('radio', { name: /Речь|Распознавание речи/ }))
    await userEvent.click(within(kind).getByRole('radio', { name: /Текст/ }))
    expect(within(sheet).getByLabelText('Адрес')).toHaveValue('http://127.0.0.1:4000/v1')
    await userEvent.type(within(sheet).getByLabelText('Название модели'), 'auto')
    await userEvent.click(within(sheet).getByRole('button', { name: 'Сохранить' }))
    await waitFor(() => expect(puts.at(-1)?.ai?.list).toHaveLength(1))
    expect(puts.at(-1)?.ai?.list[0]).not.toHaveProperty('kind', 'speech')
    expect(lists.every((l) => l.kind === undefined)).toBe(true)
  })

  it('the server’s own speech entry: Groq, address and key read-only, its model chosen from Groq’s speech list and stored', async () => {
    const { puts, lists, setModels } = stub(withAI({ list: [lite, speechServer] }))
    setModels({ models: [{ id: 'whisper-large-v3' }, { id: 'whisper-large-v3-turbo' }] })
    renderSettings('ai')
    await userEvent.click(await screen.findByRole('button', { name: /^whisper-large-v3-turbo/ }))
    const sheet = await screen.findByRole('dialog', { name: 'whisper-large-v3-turbo' })
    expect(within(sheet).getByText('Groq')).toBeInTheDocument()
    expect(within(sheet).getByText('Распознавание речи')).toBeInTheDocument()
    expect(within(sheet).queryByLabelText('Ключ API')).toBeNull()
    expect(within(sheet).queryByRole('button', { name: 'Удалить' })).toBeNull()
    expect(within(sheet).queryByRole('switch', { name: 'Сложные задачи' })).toBeNull()
    expect(within(sheet).getByText(/VAULT_STT_\*/)).toHaveTextContent('по умолчанию — whisper-large-v3-turbo')
    await userEvent.click(await within(sheet).findByRole('button', { name: /^Модель/ }))
    await userEvent.click(await screen.findByRole('menuitemradio', { name: 'whisper-large-v3' }))
    expect(lists.at(-1)).toMatchObject({ id: 'env-stt', kind: 'speech' })
    await userEvent.click(within(sheet).getByRole('button', { name: 'Сохранить' }))
    await waitFor(() => expect(puts.at(-1)?.ai?.list.find((e) => e.id === 'env-stt')).toMatchObject({ env: true, kind: 'speech', model: 'whisper-large-v3' }))
  })

  it('«Проверить» of a speech entry asks it by its id; «Изменить» keeps the kinds in blocks', async () => {
    const { puts, tests } = stub(withAI({ list: [lite, groq, whisper, speechServer] }))
    renderSettings('ai')
    await userEvent.click(await screen.findByRole('button', { name: /^whisper-large-v3(?!-)/ }))
    const sheet = await screen.findByRole('dialog', { name: 'whisper-large-v3' })
    await userEvent.click(within(sheet).getByRole('button', { name: 'Проверить' }))
    await waitFor(() => expect(tests.at(-1)).toEqual({ ids: ['s1'] }))
    await userEvent.click(within(sheet).getByRole('button', { name: 'Готово' }))
    await userEvent.click(screen.getByRole('button', { name: 'Изменить' }))
    // the first speech row cannot go above the text ones: it stays at the border
    screen.getByRole('button', { name: 'Переместить: whisper-large-v3' }).focus()
    await userEvent.keyboard('{ArrowUp}')
    await waitFor(() => expect(puts.length).toBeGreaterThan(0))
    expect(puts.at(-1)?.ai?.list.map((e) => e.id)).toEqual(['a1', 'b2', 's1', 'env-stt'])
    // within its kind it moves
    screen.getByRole('button', { name: 'Переместить: whisper-large-v3-turbo' }).focus()
    await userEvent.keyboard('{ArrowUp}')
    await waitFor(() => expect(puts.at(-1)?.ai?.list.map((e) => e.id)).toEqual(['a1', 'b2', 'env-stt', 's1']))
    expect(screen.queryByRole('button', { name: 'Убрать: whisper-large-v3-turbo' })).toBeNull()
    expect(screen.getByRole('button', { name: 'Убрать: whisper-large-v3' })).toBeInTheDocument()
  })

  it('the words: the speech mark, the key a speech provider wants', () => {
    expect(entryHint(t, whisper)).toBe('Распознавание\u00A0речи\u00A0· Groq\u00A0·\u00A0api.groq.com')
    expect(entryHint(t, speechServer)).toBe('Распознавание\u00A0речи\u00A0· Groq\u00A0·\u00A0с\u00A0сервера')
    expect(tiersLine(t, { ...whisper, tiers: ['light'] })).toBe('')
    expect(providerOf('https://generativelanguage.googleapis.com/v1beta')).toBe('gemini')
    expect(needsKey('https://api.openai.com/v1', 'speech')).toBe(true)
    expect(needsKey('https://openrouter.ai/api/v1', 'speech')).toBe(false)
    expect(needsKey('https://openrouter.ai/api/v1')).toBe(true)
    expect(byKind([whisper, lite, speechServer, groq]).map((e) => e.id)).toEqual(['a1', 'b2', 's1', 'env-stt'])
  })
})
