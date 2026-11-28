import { afterEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { AICheck, AIModel, AISettings, Settings as Values } from '../../../api/types'
import { providerOf } from '../AISettings'
import { entryHint, statusDetail, statusView, tiersLine } from '../aiModels'
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
const server: AIModel = { id: 'env', env: true, url: 'http://litellm:4000/v1', model: 'srv-model', keySet: true, tiers: [...ALL], status: { state: 'unchecked' } }

const withAI = (ai: AISettings, extra: Partial<Values> = {}): Values => ({ ...base, ai, ...extra })

/** The server as a test double: it keeps the list, gives new entries ids, drops keys. */
function stub(start: Values, checks: Record<string, AICheck['status']> = {}) {
  let cur = start
  const puts: Values[] = []
  const tests: { ids?: string[]; entry?: AIModel }[] = []
  const lists: { url?: string; key?: string; id?: string }[] = []
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
    expect(row('srv-model')).toHaveTextContent('Сервер · litellm:4000')
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

  it('the server’s own model: read-only, can be switched off, cannot be deleted', async () => {
    const { puts } = stub(withAI({ list: [server] }))
    renderSettings('ai')
    await userEvent.click(await screen.findByRole('button', { name: /^srv-model/ }))
    const sheet = await screen.findByRole('dialog', { name: 'srv-model' })
    expect(within(sheet).queryByLabelText('Адрес')).toBeNull()
    expect(within(sheet).queryByRole('button', { name: 'Удалить' })).toBeNull()
    expect(within(sheet).getByText(/VAULT_LLM_\*/)).toBeInTheDocument()
    await userEvent.click(within(sheet).getByRole('switch', { name: 'Использовать' }))
    await userEvent.click(within(sheet).getByRole('button', { name: 'Сохранить' }))
    await waitFor(() => expect(puts.at(-1)?.ai?.list[0]).toMatchObject({ id: 'env', env: true, off: true }))
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
