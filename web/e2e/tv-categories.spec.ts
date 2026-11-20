// «ТВ»'s categories on a real bin/vault with a playlist whose groups spell
// one topic many ways («Кино», «RU | Фильмы HD», «Movies»), an adult group
// and many small groups: the categories are one tag each («Кино» once), at
// most fifteen, «Избранное» first and «Для взрослых» last, and its channels
// are in no other list. A wide window has TiviMate's columns (the
// categories, the channels, the details) and the keys walk them; a phone
// keeps the categories as a level of their own. TV_CATS_SHOTS=<dir> saves
// the screenshots there.
import { createServer, type Server } from 'node:http'
import type { AddressInfo } from 'node:net'
import path from 'node:path'
import { devices, type APIRequestContext } from '@playwright/test'
import { expect, setupAdmin, test } from './vault'

test.use({ library: 'empty' })
test.setTimeout(120_000)

const GROUPS: [string, number][] = [
  ['Кино', 6],
  ['RU | Фильмы HD', 5],
  ['Movies', 4],
  ['Новости', 6],
  ['НОВОСТНЫЕ', 3],
  ['Спорт', 5],
  ['Детские', 4],
  ['Музыка', 3],
  ['Познавательные', 3],
  ['XXX', 3],
  ...Array.from({ length: 20 }, (_, i) => [`Пакет ${i + 1}`, 1] as [string, number]),
]

function playlist(): string {
  let s = '#EXTM3U\n'
  let n = 0
  for (const [g, k] of GROUPS) {
    for (let i = 0; i < k; i++) {
      n++
      s += `#EXTINF:-1 tvg-id="ch${n}" group-title="${g}",${g.replace('|', ' ').replace(/\s+/g, ' ')} ${i + 1}\nhttp://127.0.0.1:9/live/${n}.m3u8\n`
    }
  }
  return s
}

function guide(): string {
  const f = (t: number) => new Date(t).toISOString().replace(/[-:T]/g, '').slice(0, 14) + ' +0000'
  const parts = ['<?xml version="1.0" encoding="UTF-8"?><tv>']
  const total = GROUPS.reduce((a, [, k]) => a + k, 0)
  for (let i = 1; i <= total; i++) parts.push(`<channel id="ch${i}"><display-name>${i}</display-name></channel>`)
  const start = Math.floor((Date.now() - 2 * 3600_000) / 1800_000) * 1800_000
  for (let i = 1; i <= total; i++)
    for (let k = 0, t = start; t < Date.now() + 6 * 3600_000; k++, t += 45 * 60_000)
      parts.push(`<programme start="${f(t)}" stop="${f(t + 45 * 60_000)}" channel="ch${i}"><title>Передача ${k}</title><desc>Описание передачи ${k}.</desc></programme>`)
  parts.push('</tv>')
  return parts.join('')
}

let provider: Server
let base = ''
test.beforeAll(async () => {
  const m3u = playlist()
  const xml = guide()
  provider = createServer((req, res) => {
    const u = req.url ?? ''
    if (u.startsWith('/get.m3u')) return res.end(m3u)
    if (u.startsWith('/epg.xml')) return res.end(xml)
    res.statusCode = 404
    res.end()
  })
  await new Promise<void>((r) => provider.listen(0, '127.0.0.1', r))
  base = `http://127.0.0.1:${(provider.address() as AddressInfo).port}`
})
test.afterAll(() => new Promise<void>((r) => provider.close(() => r())))

async function addSource(request: APIRequestContext) {
  const r = await request.post('/api/tv/sources', { data: { name: 'Провайдер', url: `${base}/get.m3u`, defaultShown: true, presets: { timeshift: true, adult: false }, epg: [{ url: `${base}/epg.xml` }] } })
  expect(r.ok(), await r.text()).toBeTruthy()
  const src = (await r.json()) as { id: number; epg: { id: number }[] }
  await request.post(`/api/tv/sources/${src.id}/refresh`)
  await expect
    .poll(async () => {
      const l = (await (await request.get('/api/channels')).json()) as { channels: { now?: unknown }[] }
      if (l.channels.length > 0 && !l.channels[0]?.now) for (const e of src.epg) await request.post(`/api/tv/epg/${e.id}/refresh`)
      return l.channels.length >= 62 && Boolean(l.channels[0]?.now)
    }, { timeout: 60_000, intervals: [1000] })
    .toBe(true)
}

const shot = (name: string) => (process.env.TV_CATS_SHOTS ? path.join(process.env.TV_CATS_SHOTS, name) : test.info().outputPath(name))

test('categories: one tag per topic, capped, adult last and exclusive; TiviMate columns and keys', async ({ page, browserName, browser, vaultURL }) => {
  test.skip(browserName !== 'chromium', 'one engine')
  await setupAdmin(page.request)
  await addSource(page.request)

  const l = (await (await page.request.get('/api/channels')).json()) as { categories: { id: string; name: string; count: number; exclusive?: boolean }[]; channels: { name: string; categories?: string[] }[] }
  const names = l.categories.map((c) => c.name)
  expect(names.filter((n) => n === 'Кино')).toHaveLength(1)
  expect(l.categories.find((c) => c.name === 'Кино')?.count).toBe(15)
  expect(l.categories.find((c) => c.name === 'Новости')?.count).toBe(9)
  expect(l.categories.filter((c) => !c.exclusive).length).toBeLessThanOrEqual(15)
  expect(l.categories.at(-1)).toMatchObject({ id: 'ai:adult', exclusive: true, count: 3 })
  expect(l.channels.filter((c) => c.categories?.includes('ai:adult')).every((c) => c.categories?.length === 1)).toBe(true)

  // a wide window: the categories' column, the channels, the details
  await page.goto('/tv')
  const rail = page.getByRole('navigation', { name: 'Категории' })
  const list = page.getByRole('list', { name: 'Каналы' })
  await expect(rail).toBeVisible()
  await expect(list.getByRole('link').first()).toBeVisible()
  const rows = rail.getByRole('link')
  await expect(rows.first()).toContainText('Избранное')
  await expect(rows.last()).toContainText('Для взрослых')
  // «Все» leaves the adult channels out
  await expect(list.getByRole('link', { name: /XXX/ })).toHaveCount(0)
  await page.screenshot({ path: shot('tv-1440-all.png') })
  // the keys: ↓ opens the next category, → into the list, ↓ a row, ← back
  await rail.getByRole('link', { name: /Все каналы/ }).focus()
  await page.keyboard.press('ArrowDown')
  await expect(page).toHaveURL(/f=k%3Aai%3Amovies/)
  await expect(list.getByRole('listitem').first()).toHaveAttribute('aria-setsize', '15') // the list draws only the rows near the screen
  await page.keyboard.press('ArrowRight')
  await expect(list.getByRole('link').first()).toBeFocused()
  await page.keyboard.press('ArrowDown')
  await expect(list.getByRole('link').nth(1)).toBeFocused()
  await page.keyboard.press('ArrowLeft')
  await expect(rail.getByRole('link', { name: /Кино/ })).toBeFocused()
  // the rail is the way back: no «Категории» level link on a wide window
  await expect(page.getByRole('link', { name: 'Категории', exact: true })).toHaveCount(0)
  await page.screenshot({ path: shot('tv-1440-movies.png') })
  await rail.getByRole('link', { name: /Для взрослых/ }).click()
  await expect(list.getByRole('link')).toHaveCount(3)
  await page.screenshot({ path: shot('tv-1440-adult.png') })

  // a phone: the categories as a level of their own
  const { defaultBrowserType: _, ...phone } = devices['iPhone 13']
  const ctx = await browser.newContext({ ...phone, viewport: { width: 390, height: 844 }, baseURL: vaultURL, locale: 'ru-RU', colorScheme: 'dark', storageState: await otherDevice(page) })
  const p = await ctx.newPage()
  await p.goto('/tv')
  const cats = p.getByRole('navigation', { name: 'Категории' })
  await expect(cats).toBeVisible()
  await expect(cats.getByRole('link').last()).toContainText('Для взрослых')
  expect(await p.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390)
  await p.screenshot({ path: shot('tv-390-categories.png'), fullPage: true })
  await cats.getByRole('link', { name: /Кино/ }).click()
  await expect(p.getByRole('list', { name: 'Каналы' }).getByRole('link').first()).toBeVisible()
  expect(await p.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390)
  await p.screenshot({ path: shot('tv-390-movies.png') })
  await ctx.close()
})

// another device signed in to the same profile: the cookies, not where this one left «ТВ» (vault.tvPlace)
async function otherDevice(page: import('@playwright/test').Page) {
  const state = await page.context().storageState()
  for (const o of state.origins) o.localStorage = o.localStorage.filter((x) => x.name !== 'vault.tvPlace')
  return state
}
