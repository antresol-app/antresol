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
