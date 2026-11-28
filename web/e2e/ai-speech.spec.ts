// «Настройки сервера → ИИ-помощник»: speech recognition is in the models list.
// The server's own speech service (VAULT_STT_*) is an entry of it, a speech
// model is added from the same sheet after choosing the kind, a check sends it
// a second of silence, «Расшифровка аудиокниг» warns while there is no speech
// model, and the assistant off dims and disables the whole list.
import { createServer, type Server } from 'node:http'
import type { AddressInfo } from 'node:net'
import type { APIRequestContext, Page } from '@playwright/test'
import { expect, setupAdmin, test } from './vault'

test.use({ library: 'empty' })

/** A speech service: transcribes for the key it knows, lists a few models, notes the model it was asked. */
async function fakeSpeech(good: string): Promise<{ url: string; lastModel: () => string; asked: () => number; close: () => Promise<void> }> {
  let last = ''
  let asked = 0
  const s: Server = createServer((req, res) => {
    const chunks: Buffer[] = []
    req.on('data', (c: Buffer) => chunks.push(c))
    req.on('end', () => {
      res.setHeader('content-type', 'application/json')
      if (req.headers.authorization !== `Bearer ${good}`) {
        res.statusCode = 401
        res.end('{"error":{"message":"bad key"}}')
      } else if (req.url?.endsWith('/models')) {
        res.end(JSON.stringify({ data: [{ id: 'whisper-1' }, { id: 'stt-large' }, { id: 'stt-small' }] }))
      } else {
        asked++
        last = /name="model"\r\n\r\n([^\r]*)/.exec(Buffer.concat(chunks).toString('latin1'))?.[1] ?? ''
        res.end('{"text":"","segments":[]}')
      }
    })
  })
  await new Promise<void>((r) => s.listen(0, '127.0.0.1', r))
  return { url: `http://127.0.0.1:${(s.address() as AddressInfo).port}/v1`, lastModel: () => last, asked: () => asked, close: () => new Promise((r) => s.close(() => r())) }
}

const settings = async (request: APIRequestContext) => (await (await request.get('/api/settings')).json()) as { ai: { list: { id: string; kind?: string; model: string; env?: boolean; off?: boolean }[]; features?: Record<string, boolean> } }

async function putAI(request: APIRequestContext, ai: unknown) {
  const cur = (await (await request.get('/api/settings')).json()) as Record<string, unknown>
  const r = await request.put('/api/settings', { data: { ...cur, ai } })
  expect(r.ok(), await r.text()).toBeTruthy()
}

/** Nothing of the page's rows is cut and the page does not scroll sideways. */
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

const speech = await fakeSpeech('stt-key')

test.describe('speech recognition in the models list', () => {
  test.use({ env: { VAULT_STT_URL: speech.url, VAULT_STT_MODEL: 'stt-large', VAULT_STT_KEY: 'stt-key' } })

  for (const width of [390, 1440]) {
    test(`at ${width}px: the server's own speech entry, a speech model from the sheet, the warning, the master switch`, async ({ page }) => {
      await page.setViewportSize({ width, height: width < 600 ? 844 : 900 })
      await setupAdmin(page.request)
      await page.goto('/manage/settings/ai')

      // VAULT_STT_* is an entry of the list, marked as speech; the separate section is gone
      const env = page.getByRole('button', { name: /^stt-large/ })
      await expect(env).toContainText('Распознавание речи · LiteLLM · с сервера')
      await expect(page.getByRole('heading', { name: 'Распознавание речи' })).toHaveCount(0)
      await expect(page.getByText('Нет модели распознавания речи')).toHaveCount(0)

      // its model is chosen from the service's list and stored; a check sends silence with the server's key
      await env.click()
      const sheet = page.getByRole('dialog', { name: 'stt-large' })
      await expect(sheet.getByLabel('Ключ API')).toHaveCount(0)
      await expect(sheet.getByRole('button', { name: 'Удалить' })).toHaveCount(0)
      await sheet.getByRole('button', { name: /^Модель/ }).click()
      await expect(page.getByRole('menuitemradio')).toHaveText(['stt-large', 'stt-small', 'whisper-1', 'Другая…'])
      await page.getByRole('menuitemradio', { name: 'stt-small' }).click()
      await sheet.getByRole('button', { name: 'Сохранить' }).click()
      await expect(page.getByRole('button', { name: /^stt-small/ })).toContainText(/\d+ мс/)
      expect(speech.lastModel()).toBe('stt-small')
      await expect.poll(async () => (await settings(page.request)).ai.list.map((e) => `${e.id}:${e.model}`)).toEqual(['env-stt:stt-small'])

      // switched off, the server's entry stays and the transcripts warn
      await page.getByRole('button', { name: /^stt-small/ }).click()
      await page.getByRole('dialog', { name: 'stt-small' }).getByRole('switch', { name: 'Использовать' }).click()
      await page.getByRole('dialog').getByRole('button', { name: 'Сохранить' }).click()
      await expect(page.getByText('Ни одна модель распознавания речи не может работать: включите её или добавьте ключ в «Модели»')).toBeVisible()
      await expect(page.getByRole('button', { name: /^stt-small/ })).toContainText('Выключена')

      // a new model: the kind is the first choice
      await page.getByRole('button', { name: 'Добавить модель…' }).click()
      await expect(page.getByRole('dialog', { name: 'Новая модель' })).toBeVisible()
      const add = page.getByRole('dialog') // (its title becomes the model's once it is saved)
      await expect(add.getByRole('switch', { name: 'Сложные задачи' })).toBeVisible()
      await add.getByRole('radio', { name: /Распознавание речи|Речь/ }).click()
      await expect(add.getByRole('switch', { name: 'Сложные задачи' })).toHaveCount(0)
      await expect(add.getByLabel('Адрес')).toHaveValue('https://api.groq.com/openai/v1')
      await add.getByRole('button', { name: /^Провайдер/ }).click()
      await page.getByRole('menuitemradio', { name: 'LiteLLM или свой сервер' }).click()
      await add.getByLabel('Адрес').fill(speech.url)
      await add.getByLabel('Название модели').fill('stt-large')
      await add.getByLabel('Ключ API').fill('stt-key')
      await add.getByLabel('Ключ API').press('Enter')
      await expect(add.getByText(/Работает: ответ за \d+\s+мс/)).toBeVisible()
      expect(speech.lastModel()).toBe('stt-large')
      await add.getByRole('button', { name: 'Готово' }).click()
      await expect(page.getByText('Нет модели распознавания речи')).toHaveCount(0)
      await expect.poll(async () => (await settings(page.request)).ai.list.map((e) => `${e.kind ?? 'text'}:${e.model}`)).toEqual(['speech:stt-small', 'speech:stt-large'])
      expect(JSON.stringify(await (await page.request.get('/api/settings')).json())).not.toContain('stt-key')
      await nothingCut(page)

      // «Проверить все» asks both speech entries that are on... the first is off: only the new one
      const before = speech.asked()
      await page.getByRole('button', { name: /^Проверить все/ }).click()
      await expect(page.getByRole('status').filter({ hasText: 'Проверено только что: 1 работает' })).toBeVisible()
      expect(speech.asked()).toBe(before + 1)

      // the master switch: the whole list is dimmed and disabled, the page stays whole
      await page.getByRole('switch', { name: 'ИИ-помощник' }).click()
      await expect(page.getByRole('button', { name: /^stt-large/ })).toBeDisabled()
      await expect(page.getByRole('button', { name: 'Добавить модель…' })).toBeDisabled()
      await expect(page.getByRole('button', { name: /^Проверить все/ })).toBeDisabled()
      await expect(page.getByRole('switch', { name: 'Расшифровка аудиокниг' })).toBeDisabled()
      await expect(page.getByRole('button', { name: 'Изменить' })).toBeDisabled()
      await nothingCut(page)
      await page.getByRole('switch', { name: 'ИИ-помощник' }).click()
      await expect(page.getByRole('button', { name: /^stt-large/ })).toBeEnabled()
    })
  }
})

test.describe('a text model and a speech model do each other no work', () => {
  test('the list keeps the kinds in blocks and «Изменить» never mixes them', async ({ page }) => {
    const fake = await fakeSpeech('k')
    try {
      await setupAdmin(page.request)
      await putAI(page.request, {
        list: [
          { url: fake.url, model: 'whisper-1', kind: 'speech', key: 'k', tiers: [] },
          { url: 'http://127.0.0.1:9/v1', model: 'chat-a', tiers: ['light', 'medium', 'powerful'] },
          { url: fake.url, model: 'stt-small', kind: 'speech', key: 'k', tiers: [] },
        ],
      })
      await page.goto('/manage/settings/ai')
      // stored in any order, shown in blocks: the text model, then the speech ones
      await expect(page.getByRole('button', { name: /^(chat-a|stt-small|whisper-1)/ })).toHaveText([/^chat-a/, /^whisper-1/, /^stt-small/])
      await page.getByRole('button', { name: 'Изменить', exact: true }).click()
      await page.getByRole('button', { name: 'Переместить: stt-small' }).focus()
      await page.keyboard.press('ArrowUp')
      await expect.poll(async () => (await settings(page.request)).ai.list.map((e) => e.model)).toEqual(['chat-a', 'stt-small', 'whisper-1'])
      await page.getByRole('button', { name: 'Переместить: stt-small' }).focus()
      await page.keyboard.press('ArrowUp')
      expect((await settings(page.request)).ai.list.map((e) => e.model)).toEqual(['chat-a', 'stt-small', 'whisper-1'])
    } finally {
      await fake.close()
    }
  })
})
