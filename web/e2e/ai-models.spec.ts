// «Настройки сервера → ИИ-помощник» on a real server: the list of models with
// the status of each (a check finds it, a real call keeps it), «Изменить»
// reorders it (the order is the priority), a model is added from its sheet
// (Enter in the key field saves and checks it), and no text of a row is cut
// at a phone's width or a desktop's.
import { createServer, type Server } from 'node:http'
import type { AddressInfo } from 'node:net'
import type { APIRequestContext, Page } from '@playwright/test'
import { expect, setupAdmin, test } from './vault'

test.use({ library: 'empty' })

/** A model server that answers «ok» to the key it knows and 401 to any other. */
async function fakeModel(good: string): Promise<{ url: string; close: () => Promise<void> }> {
  const s: Server = createServer((req, res) => {
    req.resume()
    req.on('end', () => {
      res.setHeader('content-type', 'application/json')
      if (req.url?.endsWith('/models')) {
        res.end(JSON.stringify({ data: [{ id: 'auto' }, { id: 'groq-gpt-oss' }] }))
      } else if (req.headers.authorization !== `Bearer ${good}`) {
        res.statusCode = 401
        res.end('{"error":"bad key"}')
      } else {
        res.end(JSON.stringify({ choices: [{ message: { content: 'ok' } }] }))
      }
    })
  })
  await new Promise<void>((r) => s.listen(0, '127.0.0.1', r))
  return { url: `http://127.0.0.1:${(s.address() as AddressInfo).port}/v1`, close: () => new Promise((r) => s.close(() => r())) }
}

async function putAI(request: APIRequestContext, ai: unknown) {
  const cur = (await (await request.get('/api/settings')).json()) as Record<string, unknown>
  const r = await request.put('/api/settings', { data: { ...cur, ai } })
  expect(r.ok(), await r.text()).toBeTruthy()
}

const order = async (request: APIRequestContext) =>
  ((await (await request.get('/api/settings')).json()) as { ai: { list: { model: string }[] } }).ai.list.map((e) => e.model)

/** Nothing of the page's rows is cut: every text fits its box (an ellipsis would have scrollWidth > clientWidth). */
async function nothingCut(page: Page) {
  const cut = await page.evaluate(() => {
    const bad: string[] = []
    for (const el of document.querySelectorAll<HTMLElement>('#main li *')) {
      if (!el.classList.contains('visually-hidden') && el.children.length === 0 && el.textContent?.trim() && el.scrollWidth > el.clientWidth + 1) bad.push(el.textContent.trim())
    }
    return bad
  })
  expect(cut, 'texts that are cut').toEqual([])
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
}

for (const width of [390, 1440]) {
  test.describe(`at ${width}px`, () => {
    test.use({ viewport: { width, height: width < 600 ? 844 : 900 } })

    test('the list: statuses by a check, edit mode reorders, a model is added from its sheet', async ({ page }) => {
      const fake = await fakeModel('sk-good')
      try {
        await setupAdmin(page.request)
        await putAI(page.request, {
          list: [
            { url: fake.url, model: 'gemini-flash-lite', key: 'sk-good', tiers: ['light', 'medium', 'powerful'] },
            { url: fake.url, model: 'openai/gpt-oss-120b', key: 'sk-bad', tiers: ['light', 'medium'] },
            { url: 'https://api.anthropic.com', model: 'claude-haiku-5-5', tiers: ['medium', 'powerful'] },
          ],
        })
        await page.goto('/manage/settings/ai')
        const row = (name: string) => page.getByRole('button', { name: new RegExp(`^${name}`) })
        await expect(row('gemini-flash-lite')).toContainText('Не проверена')
        await expect(row('claude-haiku-5-5')).toContainText('Нет ключа')
        await page.getByRole('button', { name: /^Проверить все/ }).click()
        await expect(row('gemini-flash-lite')).toContainText(/\d+ мс/)
        await expect(row('openai/gpt-oss-120b')).toContainText('Ошибка 401')
        await expect(page.getByRole('status').filter({ hasText: 'Проверено только что: 1 работает, 1 с ошибкой, 1 без ключа' })).toBeVisible()
        await nothingCut(page)

        // «Изменить»: the second model moves up (the order is saved at once and is the priority)
        await page.getByRole('button', { name: 'Изменить', exact: true }).click()
        await expect(page.getByRole('button', { name: 'Готово', exact: true })).toBeVisible()
        await nothingCut(page)
        await page.getByRole('button', { name: 'Переместить: openai/gpt-oss-120b' }).focus()
        await page.keyboard.press('ArrowUp')
        await expect.poll(() => order(page.request)).toEqual(['openai/gpt-oss-120b', 'gemini-flash-lite', 'claude-haiku-5-5'])
        await page.getByRole('button', { name: 'Готово', exact: true }).click()
        await page.reload()
        await expect(page.getByRole('button', { name: /·/ }).first()).toContainText('openai/gpt-oss-120b')
        // the status survived the save (it is stored with the entry)
        await expect(row('openai/gpt-oss-120b')).toContainText('Ошибка 401')

        // a model from its sheet: the address and the key, Enter saves and checks it
        await page.getByRole('button', { name: 'Добавить модель…' }).click()
        await expect(page.getByRole('dialog', { name: 'Новая модель' })).toBeVisible()
        const sheet = page.getByRole('dialog') // (its title becomes the model's once it is saved)
        await sheet.getByLabel('Адрес').fill(fake.url)
        await sheet.getByLabel('Ключ API').fill('sk-good')
        await sheet.getByLabel('Ключ API').press('Tab')
        await sheet.getByRole('button', { name: /^Модель/ }).click()
        await page.getByRole('menuitemradio', { name: 'groq-gpt-oss' }).click()
        await sheet.getByLabel('Ключ API').press('Enter')
        await expect(sheet.getByText(/Работает: ответ за \d+\s+мс/)).toBeVisible()
        await expect.poll(() => order(page.request)).toEqual(['openai/gpt-oss-120b', 'gemini-flash-lite', 'claude-haiku-5-5', 'groq-gpt-oss'])
        // the key is never answered back
        expect(JSON.stringify(await (await page.request.get('/api/settings')).json())).not.toContain('sk-good')
        await sheet.getByRole('button', { name: 'Готово' }).click()
        await expect(row('groq-gpt-oss')).toContainText(/\d+ мс/)
        await nothingCut(page)
      } finally {
        await fake.close()
      }
    })
  })
}

/** The server's own LiteLLM: lists two models, wants the server's key, notes the model it was asked. */
async function serverModel(key: string): Promise<{ url: string; lastModel: () => string; close: () => Promise<void> }> {
  let last = ''
  const s: Server = createServer((req, res) => {
    let raw = ''
    req.on('data', (c) => (raw += c))
    req.on('end', () => {
      res.setHeader('content-type', 'application/json')
      if (req.headers.authorization !== `Bearer ${key}`) {
        res.statusCode = 401
        res.end('{"error":"bad key"}')
      } else if (req.url?.endsWith('/models')) {
        res.end(JSON.stringify({ data: [{ id: 'gemini-flash-lite' }, { id: 'groq-gpt-oss' }, { id: 'local-gemma' }] }))
      } else {
        last = (JSON.parse(raw || '{}') as { model?: string }).model ?? ''
        res.end(JSON.stringify({ choices: [{ message: { content: '{"ok":true}' } }] }))
      }
    })
  })
  await new Promise<void>((r) => s.listen(0, '127.0.0.1', r))
  return { url: `http://127.0.0.1:${(s.address() as AddressInfo).port}/v1`, lastModel: () => last, close: () => new Promise((r) => s.close(() => r())) }
}

const server = await serverModel('env-key')

test.describe('the server’s own LiteLLM and the AI features', () => {
  test.use({ env: { VAULT_LLM_URL: server.url, VAULT_LLM_MODEL: 'gemini-flash-lite', VAULT_LLM_KEY: 'env-key' } })

  for (const width of [390, 1440]) {
    test(`at ${width}px: its model is chosen, a LiteLLM is added from it, features switch`, async ({ page }) => {
      await page.setViewportSize({ width, height: width < 600 ? 844 : 900 })
      await setupAdmin(page.request)
      await page.goto('/manage/settings/ai')

      // the server's own model reads as a provider: LiteLLM, from the server
      const env = page.getByRole('button', { name: /^gemini-flash-lite/ })
      await expect(env).toContainText('LiteLLM · с сервера')
      await env.click()
      let sheet = page.getByRole('dialog', { name: 'gemini-flash-lite' })
      await expect(sheet.getByLabel('Ключ API')).toHaveCount(0)
      await sheet.getByRole('button', { name: /^Модель/ }).click()
      await expect(page.getByRole('menuitemradio')).toHaveText(['gemini-flash-lite', 'groq-gpt-oss', 'local-gemma', 'Другая…'])
      await page.getByRole('menuitemradio', { name: 'groq-gpt-oss' }).click()
      await sheet.getByRole('button', { name: 'Сохранить' }).click()
      await expect(page.getByRole('button', { name: /^groq-gpt-oss/ })).toBeVisible()
      await expect.poll(() => order(page.request)).toEqual(['groq-gpt-oss'])
      // kept over a reload; a check asks that model, with the server's key
      await page.reload()
      await expect(page.getByRole('button', { name: /^groq-gpt-oss/ })).toContainText('LiteLLM · с сервера')
      await page.getByRole('button', { name: /^Проверить все/ }).click()
      await expect(page.getByRole('button', { name: /^groq-gpt-oss/ })).toContainText(/\d+ мс/)
      expect(server.lastModel()).toBe('groq-gpt-oss')

      // another model of the same LiteLLM: its address, the server's key, no key field
      await page.getByRole('button', { name: 'Добавить модель…' }).click()
      sheet = page.getByRole('dialog', { name: 'Новая модель' })
      await expect(sheet.getByLabel('Адрес')).toHaveValue(server.url)
      await expect(sheet.getByLabel('Ключ API')).toHaveCount(0)
      await expect(sheet.getByText(/у самого сервера/)).toBeVisible()
      await sheet.getByRole('button', { name: /^Модель/ }).click()
      await page.getByRole('menuitemradio', { name: 'local-gemma' }).click()
      await sheet.getByRole('button', { name: 'Сохранить' }).click()
      await expect.poll(() => order(page.request)).toEqual(['groq-gpt-oss', 'local-gemma'])
      await expect(page.getByRole('button', { name: /^local-gemma/ })).toContainText(/\d+ мс/) // saved, then checked: the server's key is used
      expect(JSON.stringify(await (await page.request.get('/api/settings')).json())).not.toContain('env-key')
      await nothingCut(page)

      // the features: all on, one off survives a reload, the master switch disables them
      const feature = page.getByRole('switch', { name: 'Главы аудиокниг' })
      await expect(page.getByRole('heading', { name: 'Функции ИИ' })).toBeVisible()
      await expect(feature).toBeChecked()
      await feature.click()
      await expect.poll(async () => ((await (await page.request.get('/api/settings')).json()) as { ai: { features?: Record<string, boolean> } }).ai.features).toEqual({ chapters: false })
      await page.reload()
      await expect(page.getByRole('switch', { name: 'Главы аудиокниг' })).not.toBeChecked()
      await expect(page.getByRole('switch', { name: 'Описание канала' })).toBeChecked()
      await page.getByRole('switch', { name: 'ИИ-помощник' }).click()
      await expect(page.getByRole('switch', { name: 'Описание канала' })).toBeDisabled()
      await expect(page.getByText(/Пока ИИ-помощник выключен/)).toBeVisible()
      await nothingCut(page)
    })
  }
})
